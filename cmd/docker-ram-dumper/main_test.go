package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	helpers "github.com/NethermindEth/docker-ram-dumper/internal/_helpers"
)

var testBodyOutput []byte

func mockExecInContainer(output string) (*httptest.Server, *http.Client) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/test-container/exec":
			w.WriteHeader(http.StatusCreated)

			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Error reading body", http.StatusInternalServerError)
				return
			}
			testBodyOutput = body

			// Decode the body into execConfig
			var execConfig struct {
				Cmd []string `json:"Cmd"`
			}
			if err := json.Unmarshal(body, &execConfig); err != nil {
				http.Error(w, "Failed to decode JSON", http.StatusBadRequest)
				return
			}
			// Store the Cmd as a JSON string in testBodyOutput
			testBodyOutput = []byte(strings.Join(execConfig.Cmd, " "))
			w.Write([]byte(`{"Id":"test-exec-id"}`))
		case "/exec/test-exec-id/start":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(output))
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}))
	client := server.Client()

	return server, client
}

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
	memUsage, totalMemory, err := helpers.GetContainerMemoryUsage(client, "test-container", dockerAPIEndpoint, true)
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
	server, client := mockExecInContainer("1234 root      0:00 test-process\n")
	defer server.Close()

	// Use the mock server URL instead of the default Docker API endpoint
	dockerAPIEndpoint := server.URL

	// Test the function
	pid, err := helpers.GetPIDInContainer(client, "test-container", "test-process", dockerAPIEndpoint)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expectedPID := 1234
	if pid != expectedPID {
		t.Errorf("Expected PID %d, got %d", expectedPID, pid)
	}
}

func TestCleanupDumps(t *testing.T) {
	server, client := mockExecInContainer("Command output")
	defer server.Close()

	containerName := "test-container"
	dumpDirContainer := "/tmp/dumps"
	baseDockerURL := server.URL

	err := cleanupDumps(client, containerName, dumpDirContainer, baseDockerURL)
	if err != nil {
		t.Errorf("cleanupDumps failed: %v", err)
	}
}

func TestExecInContainer(t *testing.T) {
	server, client := mockExecInContainer("Command output")
	defer server.Close()

	// Use the test server URL as the baseDockerURL
	result, err := helpers.ExecInContainer(client, "test-container", server.URL, "test", "command")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result != "Command output" {
		t.Errorf("Expected 'Command output', got %s", result)
	}
}

func TestCreateDotnetDump(t *testing.T) {
	server, client := mockExecInContainer("Dotnet-dump output")
	defer server.Close()

	// Mock GetContainerMemoryUsage function
	originalGetContainerMemoryUsage := helpers.GetContainerMemoryUsage
	helpers.GetContainerMemoryUsage = func(client *http.Client, containerName, baseDockerURL string, getTotalMemory bool) (float64, uint64, error) {
		return 95.0, 1900, nil // Simulating memory usage above threshold
	}
	defer func() {
		helpers.GetContainerMemoryUsage = originalGetContainerMemoryUsage
	}()

	containerName := "test-container"
	pid := 1234
	dumpFile := "/tmp/dumps/test.dmp"
	totalMemoryThreshold := 1800.0
	checkInterval := 1 * time.Second

	output, err := createDotnetDump(client, containerName, pid, dumpFile, totalMemoryThreshold, server.URL, checkInterval)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expectedOutput := "Dotnet-dump output"
	if output != expectedOutput {
		t.Errorf("Unexpected output: got %q, want %q", output, expectedOutput)
	}
}

func TestCreateMemoryDumpProcdump(t *testing.T) {
	server, client := mockExecInContainer("procdump output")
	defer server.Close()

	originalExecInContainer := helpers.ExecInContainer
	helpers.ExecInContainer = func(client *http.Client, containerName, baseDockerURL string, command ...string) (string, error) {
		return strings.Join(command, " "), nil
	}
	defer func() {
		helpers.ExecInContainer = originalExecInContainer
	}()

	containerName := "test-container"
	pid := 1234
	dumpFile := "/tmp/dumps/test.dmp"
	totalMemoryThreshold := 1800.0
	checkInterval := 1 * time.Second

	output, err := createMemoryDump(client, containerName, "procdump", pid, dumpFile, totalMemoryThreshold, server.URL, checkInterval)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expectedOutput := fmt.Sprintf("procdump -d -n 1 -s 1 -M %d -p %d -o %s", int(totalMemoryThreshold), int(pid), dumpFile)
	if output != expectedOutput {
		t.Errorf("Unexpected output: got %q, want %q", output, expectedOutput)
	}
}

func TestCreateMemoryDumpDotnetMemory(t *testing.T) {
	server, client := mockExecInContainer("Dotnet-dump output")
	defer server.Close()
	// Mock GetContainerMemoryUsage function
	originalGetContainerMemoryUsage := helpers.GetContainerMemoryUsage
	helpers.GetContainerMemoryUsage = func(client *http.Client, containerName, baseDockerURL string, getTotalMemory bool) (float64, uint64, error) {
		return 95.0, 1900, nil // Simulating memory usage above threshold
	}

	originalExecInContainer := helpers.ExecInContainer
	helpers.ExecInContainer = func(client *http.Client, containerName, baseDockerURL string, command ...string) (string, error) {
		return strings.Join(command, " "), nil
	}
	defer func() {
		helpers.GetContainerMemoryUsage = originalGetContainerMemoryUsage
		helpers.ExecInContainer = originalExecInContainer
	}()

	containerName := "test-container"
	pid := 1234
	dumpFile := "/tmp/dumps/test.dmp"
	totalMemoryThreshold := 1800.0
	checkInterval := 1 * time.Second

	output, err := createMemoryDump(client, containerName, "dotnet-dump", pid, dumpFile, totalMemoryThreshold, server.URL, checkInterval)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expectedOutput := fmt.Sprintf("/root/.dotnet/tools/dotnet-dump collect -p %d -o %s", pid, dumpFile)
	if output != expectedOutput {
		t.Errorf("Unexpected output: got %q, want %q", output, expectedOutput)
	}
}

func TestInstallDumpToolProcdumpNotInstalled(t *testing.T) {
	server, client := mockExecInContainer("")
	defer server.Close()

	originalExecInContainer := helpers.ExecInContainer
	helpers.ExecInContainer = func(client *http.Client, containerName, baseDockerURL string, command ...string) (string, error) {
		return strings.Join(command, " "), nil
	}
	defer func() {
		helpers.ExecInContainer = originalExecInContainer
	}()

	containerName := "test-container"

	output, err := installDumpTool(client, containerName, "procdump", server.URL)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expectedOutput := "which procdump"

	if output != expectedOutput {
		t.Errorf("Unexpected output: got %q, want %q", output, expectedOutput)
	}
}

func TestInstallDumpToolProcdumpInstalled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/test-container/exec":
			w.WriteHeader(http.StatusCreated)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Error reading body", http.StatusInternalServerError)
				return
			}
			if strings.Contains(string(body), "which") {
				http.Error(w, "Not found", http.StatusNotFound)
				return
			}
			w.Write([]byte(`{"Id":"test-exec-id"}`))
			// Store the raw body in testBodyOutput
			testBodyOutput = body

			// Decode the body into execConfig
			var execConfig struct {
				Cmd []string `json:"Cmd"`
			}
			if err := json.Unmarshal(body, &execConfig); err != nil {
				http.Error(w, "Failed to decode JSON", http.StatusBadRequest)
				return
			}
			// Store the Cmd as a JSON string in testBodyOutput
			testBodyOutput = []byte(strings.Join(execConfig.Cmd, " "))
		case "/exec/test-exec-id/start":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := server.Client()

	containerName := "test-container"

	_, err := installDumpTool(client, containerName, "procdump", server.URL)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expectedOutput := "sh -c apk add --no-cache procdump || apt-get update && apt-get install -y procdump"
	if string(testBodyOutput) != expectedOutput {
		t.Errorf("Unexpected output: got %q, want %q", string(testBodyOutput), expectedOutput)
	}
}
