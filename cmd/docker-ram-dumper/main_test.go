package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetContainerMemoryUsage(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate Docker API response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"memory_stats": {
				"usage": 104857600,
				"limit": 1073741824
			}
		}`))
	}))
	defer server.Close()

	// Create a client that uses the mock server
	client := server.Client()

	// Use the mock server URL instead of the default Docker API endpoint
	dockerAPIEndpoint := server.URL

	// Test the function
	memUsage, totalMemory, err := getContainerMemoryUsage(client, "test-container", dockerAPIEndpoint, true)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedMemUsage := 9.765625 // (104857600 / 1073741824) * 100
	if memUsage != expectedMemUsage {
		t.Errorf("Expected memory usage %.6f%%, got %.6f%%", expectedMemUsage, memUsage)
	}

	expectedTotalMemory := uint64(1024) // 1073741824 / 1024 / 1024
	if totalMemory != expectedTotalMemory {
		t.Errorf("Expected total memory %d MB, got %d MB", expectedTotalMemory, totalMemory)
	}
}

func TestGetPIDInContainer(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate Docker API response for exec creation
		if r.URL.Path == "/containers/test-container/exec" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Id": "exec123"}`))
		} else if r.URL.Path == "/exec/exec123/start" {
			// Simulate Docker API response for exec start
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("1234 root      0:00 test-process\n"))
		}
	}))
	defer server.Close()

	// Create a client that uses the mock server
	client := server.Client()

	// Use the mock server URL instead of the default Docker API endpoint
	dockerAPIEndpoint := server.URL

	// Test the function
	pid, err := getPIDInContainer(client, "test-container", "test-process", dockerAPIEndpoint)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedPID := 1234
	if pid != expectedPID {
		t.Errorf("Expected PID %d, got %d", expectedPID, pid)
	}
}

func TestCleanupDumps(t *testing.T) {
	// Mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/test-container/exec":
			// Simulate Docker API response for exec creation
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Id":"test-exec-id"}`))
		case "/exec/test-exec-id/start":
			// Simulate Docker API response for exec start
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// The actual response body is not used in the cleanupDumps function
			w.Write([]byte(`{}`))
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := server.Client()
	containerName := "test-container"
	dumpDirContainer := "/tmp/dumps"
	baseDockerURL := server.URL

	err := cleanupDumps(client, containerName, dumpDirContainer, baseDockerURL)
	if err != nil {
		t.Errorf("cleanupDumps failed: %v", err)
	}
}

func TestExecInContainer(t *testing.T) {
	// Mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/test-container/exec":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Id":"test-exec-id"}`))
		case "/exec/test-exec-id/start":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Command output"))
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := server.Client()
	containerName := "test-container"
	baseDockerURL := server.URL

	output, err := execInContainer(client, containerName, baseDockerURL, "test", "command")
	if err != nil {
		t.Errorf("execInContainer failed: %v", err)
	}
	if output != "Command output" {
		t.Errorf("Unexpected output: got %q, want %q", output, "Command output")
	}
}
