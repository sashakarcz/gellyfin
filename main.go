package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

type Allocation struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	NodeID string `json:"NodeID"`
}

func main() {
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/restart", restartHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/healthz", healthzHandler)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	log.Println("Starting server on :8888")
	log.Fatal(http.ListenAndServe(":8888", nil))
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/index.html")
}

func restartHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request to restart Jellyfin job")
	fmt.Fprintf(w, "Restarting Jellyfin job...\n")
	nomadAddr := "http://consul.service.starnix.net:4646"
	os.Setenv("NOMAD_ADDR", nomadAddr)

	// Restart the job
	restartCmd := exec.Command("/usr/local/bin/nomad", "job", "restart", "-yes", "-verbose", "jellyfin")
	restartCmd.Env = append(os.Environ(), "NOMAD_ADDR="+nomadAddr)
	log.Println("Restarting Jellyfin job")
	output, err := restartCmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to restart job: %v\nOutput: %s\n", err, output)
		fmt.Fprintf(w, "Failed to restart job: %v\nOutput: %s\n", err, output)
		return
	}

	log.Printf("Jellyfin job restarted successfully\nOutput: %s\n", output)
	fmt.Fprintf(w, "Jellyfin job restarted successfully.\nOutput: %s\n", output)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	nomadAddr := "http://consul.service.starnix.net:4646/v1/status/leader"
	resp, err := http.Get(nomadAddr)
	nomadStatus := "Nomad API is reachable"
	if err != nil || resp.StatusCode != http.StatusOK {
		nomadStatus = "Nomad API is not reachable"
	}

	serviceURL := "https://jellyfin.service.starnix.net"
	serviceResp, err := http.Get(serviceURL)
	serviceStatus := "Service is reachable"
	if err != nil || serviceResp.StatusCode != http.StatusOK {
		serviceStatus = fmt.Sprintf("Service is not reachable, HTTP status code: %d", serviceResp.StatusCode)
	} else {
		serviceStatus = fmt.Sprintf("Service is reachable, HTTP status code: %d", serviceResp.StatusCode)
	}

	data := struct {
		Time          string `json:"time"`
		GoVersion     string `json:"go_version"`
		NomadStatus   string `json:"nomad_status"`
		ServiceStatus string `json:"service_status"`
	}{
		Time:          time.Now().Format(time.RFC1123),
		GoVersion:     runtime.Version(),
		NomadStatus:   nomadStatus,
		ServiceStatus: serviceStatus,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}
