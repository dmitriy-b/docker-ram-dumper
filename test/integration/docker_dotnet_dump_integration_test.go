package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	helpers "github.com/NethermindEth/docker-ram-dumper/internal/_helpers"
)

const (
	testContainerName = "ram-dumper-test-container"
	testImageName     = "ram-dumper-test-image"
)

// - `-threshold float`: Memory usage threshold percentage (default 90.0)
// - `-process string`: Name of the process to monitor (default "dotnet")
// - `-dumpdir-container string`: Directory to store memory dumps inside the container (default "/tmp/dumps")
// - `-dumpdir-host string`: Directory to store memory dumps on the host (default "/tmp/dumps")
// - `-container string`: Name of the container to monitor (default "sedge-node")
// - `-interval duration`: Interval between memory checks (default 30s)
// - `-monitor`: Continuously monitor memory usage (default false)
// - `-dumps-count int`: Number of memory dumps to create before stopping (default 1)
// - `-cleanup`: Clean up dumps in container after copying memory dump to host (default false)
// - `-base-docker-url string`: Base Docker URL (default "http://localhost")
// - `-dump-tool string`: Tool to use for memory dump, `procdump` or `dotnet-dump` (default "procdump")
// - `-timeout duration`: Global timeout for the tool to exit (default 0 or 10 minutes if -monitor is set)

func setupIntegrationTest(t *testing.T) (context.Context, context.CancelFunc) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	return context.WithTimeout(context.Background(), 3*time.Minute)
}

func TestDotnetDumpMemoryDumper(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set a longer timeout for this test
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create a test context with the desired container name
	testCtx := helpers.NewTestContext(t, testContainerName, testImageName)

	// Start the test container
	containerID := helpers.StartTestContainer(testCtx)
	defer helpers.StopAndRemoveContainer(t, containerID)
	defer os.RemoveAll(helpers.TestDumpsDir)

	// Run memory stress in the background
	go func() {
		// Check if run-memory-stress exists and is executable
		cmd := exec.Command("docker", "exec", containerID, "ls", "-l", "/usr/local/bin/run-memory-stress")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("Failed to check run-memory-stress: %v\nOutput: %s", err, output)
			return
		}
		t.Logf("run-memory-stress permissions: %s", output)

		// Check if MemoryStress exists
		cmd = exec.Command("docker", "exec", containerID, "ls", "-l", "/root/MemoryStress/bin/Release/net7.0/MemoryStress")
		output, err = cmd.CombinedOutput()
		if err != nil {
			t.Errorf("Failed to check MemoryStress: %v\nOutput: %s", err, output)
			return
		}
		t.Logf("MemoryStress permissions: %s", output)

		// Run the stress test
		output, err = helpers.RunStressCommand(containerID, "90%", "60s")
		if err != nil {
			t.Errorf("Failed to run stress command: %v\nOutput: %s", err, output)
			return
		}
		t.Logf("Memory stress output: %s", output)
	}()

	// Wait for memory usage to increase and verify it's running
	time.Sleep(5 * time.Second)
	cmd := exec.Command("docker", "exec", containerID, "ps", "aux")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("Failed to check processes: %v\nOutput: %s", err, output)
	}
	t.Logf("Processes after starting memory stress:\n%s", output)

	// Only proceed if we see the MemoryStress process
	if !strings.Contains(string(output), "MemoryStress") {
		t.Fatal("MemoryStress process not found")
	}

	flags := map[string]string{
		"threshold":         "80",
		"process":           "MemoryStress",
		"container":         testContainerName,
		"dumpdir-container": "/tmp/dumps",
		"dump-tool":         "dotnet-dump",
	}
	runDotnetDumpAsync(ctx, flags, t)
	// Check if the dump file was created
	checkDotnetDumpFiles(t, 1)

	// Add this function to help with debugging
	getDotnetDumpAnalyzeLogs(t, containerID)
}

func TestDotnetDumpNegativeScenario(t *testing.T) {
	ctx, cancel := setupIntegrationTest(t)
	defer cancel()

	// Create a test context with the desired container name
	testCtx := helpers.NewTestContext(t, testContainerName, testImageName)

	// Start the test container
	containerID := helpers.StartTestContainer(testCtx)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(helpers.TestDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "60%", "15s")

	flags := map[string]string{
		"threshold": "60",
		"process":   "MemoryStress",
		"container": testContainerName,
	}
	runDotnetDumpAsync(ctx, flags, t)

	// Check if the dump file was created
	checkDumpFiles(t, 0)

	// Add this function to help with debugging
	getDotnetDumpAnalyzeLogs(t, containerID)
}

func TestDotnetDumpDefaultThreasholdScenario(t *testing.T) {
	ctx, cancel := setupIntegrationTest(t)
	defer cancel()

	// Create a test context with the desired container name
	testCtx := helpers.NewTestContext(t, testContainerName, testImageName)

	// Start the test container
	containerID := helpers.StartTestContainer(testCtx)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(helpers.TestDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "50%", "15s")

	flags := map[string]string{
		"process":   "MemoryStress",
		"container": testContainerName,
	}
	runDotnetDumpAsync(ctx, flags, t)

	// Check if the dump file was created
	// Shold not create dump file because threshold is 90% by default
	checkDumpFiles(t, 0)

	// Add this function to help with debugging
	getDotnetDumpAnalyzeLogs(t, containerID)
}

func TestDotnetDumpThreasholdMBScenario(t *testing.T) {
	ctx, cancel := setupIntegrationTest(t)
	defer cancel()

	// Create a test context with the desired container name
	testCtx := helpers.NewTestContext(t, testContainerName, testImageName)

	// Start the test container
	containerID := helpers.StartTestContainer(testCtx)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(helpers.TestDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "10%", "15s")

	flags := map[string]string{
		"threshold": "10MB",
		"process":   "MemoryStress",
		"container": testContainerName,
	}
	runDotnetDumpAsync(ctx, flags, t)

	// Check if the dump file was created
	checkDumpFiles(t, 1)

	// Add this function to help with debugging
	getDotnetDumpAnalyzeLogs(t, containerID)
}

func checkDotnetDumpFiles(t *testing.T, filesCount int) {
	dumpFiles, err := os.ReadDir(helpers.TestDumpsDir)
	if err != nil {
		t.Fatalf("Failed to read dump directory: %v", err)
	}

	if len(dumpFiles) != filesCount {
		t.Errorf("Expected %d dump files, but found %d", filesCount, len(dumpFiles))
	}
}

func runDotnetDumpAsync(ctx context.Context, flags map[string]string, t *testing.T) (chan []byte, chan error) {
	outputCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	// Run docker-ram-dumper
	go func() {
		output, err := helpers.RunDockerRamDumper(flags)
		if err != nil {
			t.Logf("docker-ram-dumper error: %v", err)
			errCh <- fmt.Errorf("Failed to run main program: %v\nOutput: %s", err, output)
			return
		}
		t.Logf("docker-ram-dumper output: %s", output)
		outputCh <- output
	}()

	// Wait for dump file to appear
	dumpTimeout := time.After(60 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("runStressCommand or main program failed: %v", err)
			}
		case output := <-outputCh:
			t.Logf("docker-ram-dumper output: %s", output)
			return outputCh, errCh
		case <-ticker.C:
			// Check if dump file exists
			files, _ := os.ReadDir(helpers.TestDumpsDir)
			if len(files) > 0 {
				t.Logf("Found dump file: %s", files[0].Name())
				return outputCh, errCh
			}
		case <-dumpTimeout:
			t.Log("Getting container logs after timeout...")
			getDotnetDumpAnalyzeLogs(t, flags["container"])
			t.Fatal("Timeout waiting for dump file")
		case <-ctx.Done():
			t.Fatal("Context cancelled")
		}
	}
}

// Add this function to help with debugging
func getDotnetDumpAnalyzeLogs(t *testing.T, containerName string) {
	t.Helper()

	// Get all container logs
	cmd := exec.Command("docker", "logs", containerName)
	logs, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Error getting container logs: %v", err)
	}
	t.Logf("Container logs:\n%s", string(logs))

	// Get the output of specific diagnostic commands
	diagnosticCommands := []string{
		"ps aux",
		"ls -la /tmp",
		"ls -la /var/dumps",
		"ls -la /tmp/diagnostics",
		"cat /tmp/strace_*.log",
	}

	for _, cmd := range diagnosticCommands {
		output, err := exec.Command("docker", "exec", containerName, "sh", "-c", cmd).CombinedOutput()
		if err != nil {
			t.Logf("Error running '%s': %v", cmd, err)
		}
		t.Logf("Output of '%s':\n%s", cmd, string(output))
	}
}
