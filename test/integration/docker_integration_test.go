package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	helpers "github.com/NethermindEth/docker-ram-dumper/internal/_helpers"
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

const (
	testDumpsDir = "/tmp/test-dumps"
	dirPerms     = 0o755
)

func TestMain(m *testing.M) {
	// Setup
	cleanup()

	// Ensure dump directory exists
	err := os.MkdirAll(testDumpsDir, dirPerms)
	if err != nil {
		fmt.Println("Error creating dump directory:", err)
		return
	}

	// Run the tests
	code := m.Run()
	// Teardown
	cleanup()

	os.Exit(code)
}

func cleanup() {
	// Remove the test container if it exists
	cmd := exec.Command("docker", "rm", "-f", helpers.TestContainerName)
	cmd.Run() // Ignore errors, as the container might not exist
}

func TestMemoryDumper(t *testing.T) {
	// Start the test container
	containerID := helpers.StartTestContainer(t)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(testDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "50%", "15s")

	flags := map[string]string{
		"threshold": "1",
		"process":   "stress-ng-vm",
		"container": helpers.TestContainerName,
	}
	runDockerRamDumperAsync(flags, t)
	// Check if the dump file was created
	checkDumpFiles(t, 1)
}

func TestMemoryDumperNegativeScenario(t *testing.T) {
	// Start the test container
	containerID := helpers.StartTestContainer(t)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(testDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "50%", "15s")

	flags := map[string]string{
		"threshold": "60",
		"process":   "stress-ng-vm",
		"container": helpers.TestContainerName,
	}
	runDockerRamDumperAsync(flags, t)

	// Check if the dump file was created
	checkDumpFiles(t, 0)
}

func TestDefaultThreasholdScenario(t *testing.T) {
	// Start the test container
	containerID := helpers.StartTestContainer(t)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(testDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "50%", "15s")

	flags := map[string]string{
		"process":   "stress-ng-vm",
		"container": helpers.TestContainerName,
	}
	runDockerRamDumperAsync(flags, t)

	// Check if the dump file was created
	// Shold not create dump file because threshold is 90% by default
	checkDumpFiles(t, 0)
}

func TestThreasholdMBScenario(t *testing.T) {
	// Start the test container
	containerID := helpers.StartTestContainer(t)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(testDumpsDir)

	// Run the memory stress command in the background
	runDockerStressCommandAsync(containerID, "10%", "15s")

	flags := map[string]string{
		"threshold": "10MB",
		"process":   "stress-ng-vm",
		"container": helpers.TestContainerName,
	}
	runDockerRamDumperAsync(flags, t)

	// Check if the dump file was created
	checkDumpFiles(t, 1)
}

func checkDumpFiles(t *testing.T, filesCount int) {
	dumpFiles, err := os.ReadDir(testDumpsDir)
	if err != nil {
		t.Fatalf("Failed to read dump directory: %v", err)
	}

	if len(dumpFiles) != filesCount {
		t.Errorf("Expected %d dump files, but found %d", filesCount, len(dumpFiles))
	}
}

func runDockerRamDumperAsync(flags map[string]string, t *testing.T) (chan []byte, chan error) {
	outputCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		output, err := helpers.RunDockerRamDumper(flags)
		if err != nil {
			errCh <- fmt.Errorf("Failed to run main program: %v\nOutput: %s", err, output)
			return
		}
		outputCh <- output
	}()
	// Check for errors from the goroutines
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runStressCommand or main program failed: %v", err)
		}
	case output := <-outputCh:
		t.Logf("docker-ram-dumper output: %s", output)
	}

	return outputCh, errCh
}

func runDockerStressCommandAsync(containerID string, vmBytes string, timeout string) (chan []byte, chan error) {
	errCh := make(chan error, 1)
	var output []byte
	go func() {
		var err error
		output, err = helpers.RunStressCommand(containerID, vmBytes, timeout)
		errCh <- err
	}()

	outputCh := make(chan []byte, 1)
	go func() {
		outputCh <- output
	}()

	return outputCh, errCh
}
