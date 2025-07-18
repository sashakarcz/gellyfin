package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

type Config struct {
	Port         string
	NomadAddr    string
	NomadBinary  string
	JobName      string
	ServiceURL   string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type HealthStatus struct {
	Time          string `json:"time"`
	GoVersion     string `json:"go_version"`
	NomadStatus   string `json:"nomad_status"`
	ServiceStatus string `json:"service_status"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var (
	config  *Config
	logger  *slog.Logger
	limiter *rate.Limiter
	server  *http.Server

	// Prometheus metrics
	restartTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jellyfin_restarts_total",
			Help: "Total number of Jellyfin restart attempts",
		},
		[]string{"status"},
	)
	restartDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name: "jellyfin_restart_duration_seconds",
			Help: "Time spent restarting Jellyfin job",
		},
	)
	healthCheckTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "health_checks_total",
			Help: "Total number of health checks",
		},
		[]string{"endpoint", "status"},
	)
)

func main() {
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	config = loadConfig()
	limiter = rate.NewLimiter(rate.Every(time.Second), 10)

	prometheus.MustRegister(restartTotal, restartDuration, healthCheckTotal)

	mux := http.NewServeMux()
	mux.HandleFunc("/", rateLimitMiddleware(homeHandler))
	mux.HandleFunc("/restart", rateLimitMiddleware(restartHandler))
	mux.HandleFunc("/health", rateLimitMiddleware(healthHandler))
	mux.HandleFunc("/healthz", rateLimitMiddleware(healthzHandler))
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	server = &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mux,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	go func() {
		logger.Info("Starting server", "port", config.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Server exited")
}

func loadConfig() *Config {
	return &Config{
		Port:         getEnv("PORT", "8888"),
		NomadAddr:    getEnv("NOMAD_ADDR", "http://consul.service.starnix.net:4646"),
		NomadBinary:  getEnv("NOMAD_BINARY", "/usr/local/bin/nomad"),
		JobName:      getEnv("JOB_NAME", "jellyfin"),
		ServiceURL:   getEnv("SERVICE_URL", "https://jellyfin.service.starnix.net"),
		ReadTimeout:  parseDuration(getEnv("READ_TIMEOUT", "10s")),
		WriteTimeout: parseDuration(getEnv("WRITE_TIMEOUT", "10s")),
		IdleTimeout:  parseDuration(getEnv("IDLE_TIMEOUT", "60s")),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		logger.Warn("Invalid duration, using default", "input", s, "default", "10s")
		return 10 * time.Second
	}
	return d
}

func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			logger.Warn("Rate limit exceeded", "remote_addr", r.RemoteAddr)
			sendErrorResponse(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func sendErrorResponse(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error:   http.StatusText(code),
		Code:    code,
		Message: message,
	})
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.ServeFile(w, r, "static/index.html")
}

func restartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendErrorResponse(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	logger.Info("Received request to restart job", "job", config.JobName, "remote_addr", r.RemoteAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	restartCmd := exec.CommandContext(ctx, config.NomadBinary, "job", "restart", "-yes", "-verbose", config.JobName)
	restartCmd.Env = append(os.Environ(), "NOMAD_ADDR="+config.NomadAddr)

	output, err := restartCmd.CombinedOutput()
	duration := time.Since(start)
	restartDuration.Observe(duration.Seconds())

	if err != nil {
		restartTotal.WithLabelValues("error").Inc()
		logger.Error("Failed to restart job", "job", config.JobName, "error", err, "output", string(output), "duration", duration)
		sendErrorResponse(w, "Failed to restart job", http.StatusInternalServerError)
		return
	}

	restartTotal.WithLabelValues("success").Inc()
	logger.Info("Job restarted successfully", "job", config.JobName, "duration", duration)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  "Job restarted successfully",
		"job":      config.JobName,
		"output":   string(output),
		"duration": duration.String(),
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	healthCheckTotal.WithLabelValues("health", "success").Inc()
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "OK")
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Second}

	nomadURL := config.NomadAddr + "/v1/status/leader"
	nomadStatus := checkEndpoint(ctx, client, nomadURL, "nomad")
	serviceStatus := checkEndpoint(ctx, client, config.ServiceURL, "service")

	data := HealthStatus{
		Time:          time.Now().Format(time.RFC3339),
		GoVersion:     runtime.Version(),
		NomadStatus:   nomadStatus,
		ServiceStatus: serviceStatus,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func checkEndpoint(ctx context.Context, client *http.Client, url, name string) string {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		healthCheckTotal.WithLabelValues("healthz_"+name, "error").Inc()
		return fmt.Sprintf("%s is not reachable (request error)", strings.Title(name))
	}

	resp, err := client.Do(req)
	if err != nil {
		healthCheckTotal.WithLabelValues("healthz_"+name, "error").Inc()
		return fmt.Sprintf("%s is not reachable (connection error)", strings.Title(name))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		healthCheckTotal.WithLabelValues("healthz_"+name, "success").Inc()
		return fmt.Sprintf("%s is reachable (HTTP %d)", strings.Title(name), resp.StatusCode)
	}

	healthCheckTotal.WithLabelValues("healthz_"+name, "error").Inc()
	return fmt.Sprintf("%s is not reachable (HTTP %d)", strings.Title(name), resp.StatusCode)
}