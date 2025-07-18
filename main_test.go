package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
)

func TestMain(m *testing.M) {
	// Setup test environment
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	
	config = &Config{
		Port:         "8888",
		NomadAddr:    "http://test-nomad:4646",
		NomadBinary:  "/bin/echo", // Use echo for testing
		JobName:      "test-job",
		ServiceURL:   "http://test-service",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	
	limiter = rate.NewLimiter(rate.Every(time.Second), 10)
	
	// Reset metrics for each test
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	prometheus.MustRegister(restartTotal, restartDuration, healthCheckTotal)
	
	code := m.Run()
	os.Exit(code)
}

func TestLoadConfig(t *testing.T) {
	// Save original env vars
	originalVars := map[string]string{
		"PORT":          os.Getenv("PORT"),
		"NOMAD_ADDR":    os.Getenv("NOMAD_ADDR"),
		"NOMAD_BINARY":  os.Getenv("NOMAD_BINARY"),
		"JOB_NAME":      os.Getenv("JOB_NAME"),
		"SERVICE_URL":   os.Getenv("SERVICE_URL"),
		"READ_TIMEOUT":  os.Getenv("READ_TIMEOUT"),
		"WRITE_TIMEOUT": os.Getenv("WRITE_TIMEOUT"),
		"IDLE_TIMEOUT":  os.Getenv("IDLE_TIMEOUT"),
	}
	
	// Clean up after test
	defer func() {
		for key, val := range originalVars {
			if val == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, val)
			}
		}
	}()
	
	// Test with environment variables
	os.Setenv("PORT", "9999")
	os.Setenv("JOB_NAME", "test-env-job")
	os.Setenv("READ_TIMEOUT", "5s")
	
	cfg := loadConfig()
	
	if cfg.Port != "9999" {
		t.Errorf("Expected Port 9999, got %s", cfg.Port)
	}
	if cfg.JobName != "test-env-job" {
		t.Errorf("Expected JobName test-env-job, got %s", cfg.JobName)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("Expected ReadTimeout 5s, got %v", cfg.ReadTimeout)
	}
	
	// Test defaults
	os.Unsetenv("PORT")
	os.Unsetenv("JOB_NAME")
	os.Unsetenv("READ_TIMEOUT")
	
	cfg = loadConfig()
	
	if cfg.Port != "8888" {
		t.Errorf("Expected default Port 8888, got %s", cfg.Port)
	}
	if cfg.JobName != "jellyfin" {
		t.Errorf("Expected default JobName jellyfin, got %s", cfg.JobName)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"10s", 10 * time.Second},
		{"1m", 60 * time.Second},
		{"invalid", 10 * time.Second}, // Should fallback to default
	}
	
	for _, tt := range tests {
		result := parseDuration(tt.input)
		if result != tt.expected {
			t.Errorf("parseDuration(%s) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	
	healthHandler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	if w.Body.String() != "OK" {
		t.Errorf("Expected body 'OK', got %s", w.Body.String())
	}
	
	// Test wrong method
	req = httptest.NewRequest("POST", "/health", nil)
	w = httptest.NewRecorder()
	
	healthHandler(w, req)
	
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHomeHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	
	homeHandler(w, req)
	
	// Should serve the static file, expect status OK if file exists
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("Expected status 200 or 404, got %d", w.Code)
	}
	
	// Test wrong method
	req = httptest.NewRequest("POST", "/", nil)
	w = httptest.NewRecorder()
	
	homeHandler(w, req)
	
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestRestartHandler(t *testing.T) {
	// Test successful restart
	req := httptest.NewRequest("POST", "/restart", bytes.NewBuffer([]byte{}))
	w := httptest.NewRecorder()
	
	restartHandler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}
	
	if success, ok := response["success"].(bool); !ok || !success {
		t.Errorf("Expected success=true in response")
	}
	
	// Test wrong method
	req = httptest.NewRequest("GET", "/restart", nil)
	w = httptest.NewRecorder()
	
	restartHandler(w, req)
	
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}
}

func TestHealthzHandler(t *testing.T) {
	// Create test server for mocking external services
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()
	
	// Update config to use test server
	originalNomadAddr := config.NomadAddr
	originalServiceURL := config.ServiceURL
	config.NomadAddr = testServer.URL
	config.ServiceURL = testServer.URL
	
	defer func() {
		config.NomadAddr = originalNomadAddr
		config.ServiceURL = originalServiceURL
	}()
	
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	
	healthzHandler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	var response HealthStatus
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}
	
	if response.GoVersion == "" {
		t.Error("Expected GoVersion to be set")
	}
	
	if !strings.Contains(response.NomadStatus, "reachable") {
		t.Errorf("Expected Nomad to be reachable, got: %s", response.NomadStatus)
	}
}

func TestCheckEndpoint(t *testing.T) {
	// Test successful endpoint
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()
	
	ctx := context.Background()
	client := &http.Client{Timeout: 5 * time.Second}
	
	result := checkEndpoint(ctx, client, testServer.URL, "test")
	if !strings.Contains(result, "reachable") {
		t.Errorf("Expected endpoint to be reachable, got: %s", result)
	}
	
	// Test unreachable endpoint
	result = checkEndpoint(ctx, client, "http://invalid-url-12345", "test")
	if !strings.Contains(result, "not reachable") {
		t.Errorf("Expected endpoint to be unreachable, got: %s", result)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	// Create a simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	
	// Create rate limited handler with very low limit
	testLimiter := rate.NewLimiter(rate.Every(time.Hour), 1) // Only 1 request per hour
	middleware := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !testLimiter.Allow() {
				sendErrorResponse(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next(w, r)
		}
	}
	
	rateLimitedHandler := middleware(handler)
	
	// First request should succeed
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	rateLimitedHandler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("First request should succeed, got status %d", w.Code)
	}
	
	// Second request should be rate limited
	req = httptest.NewRequest("GET", "/test", nil)
	w = httptest.NewRecorder()
	rateLimitedHandler(w, req)
	
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Second request should be rate limited, got status %d", w.Code)
	}
}

func TestSendErrorResponse(t *testing.T) {
	w := httptest.NewRecorder()
	sendErrorResponse(w, "Test error", http.StatusBadRequest)
	
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
	
	var response ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}
	
	if response.Message != "Test error" {
		t.Errorf("Expected message 'Test error', got %s", response.Message)
	}
	
	if response.Code != http.StatusBadRequest {
		t.Errorf("Expected code 400, got %d", response.Code)
	}
}