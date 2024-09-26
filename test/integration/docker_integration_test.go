package integration_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	helpers "github.com/NethermindEth/docker-ram-dumper/internal/_helpers"
)

const (
	testDumpsDir = "/tmp/test-dumps"
	dirPerms     = 0o755
)

func TestMain(m *testing.M) {
	// Clean up
	defer helpers.RemoveTestContainer()

	// Ensure dump directory exists
	err := os.MkdirAll(testDumpsDir, dirPerms)
	if err != nil {
		fmt.Println("Error creating dump directory:", err)
		return
	}

	// Run the tests
	code := m.Run()

	os.Exit(code)
}

func TestMemoryDumper(t *testing.T) {
	// Start the test container
	containerID := helpers.StartTestContainer(t)
	defer helpers.StopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll(testDumpsDir)

	// Run the memory stress command in the background
	errCh := make(chan error, 1)
	go func() {
		errCh <- helpers.RunStressCommand(containerID, "50%", "15s")
	}()
	time.Sleep(5 * time.Second)

	// Run your main program inside a Docker container in parallel
	outputCh := make(chan []byte, 1)
	go func() {
		output, err := helpers.RunDockerRamDumper(map[string]string{
			"threshold": "1",
			"process":   "stress-ng-vm",
			"container": helpers.TestContainerName,
		})
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

	// Check if the dump file was created
	dumpFiles, err := os.ReadDir(testDumpsDir)
	if err != nil {
		t.Fatalf("Failed to read dump directory: %v", err)
	}

	if len(dumpFiles) == 0 {
		t.Errorf("No dump files were created")
	} else {
		t.Logf("Dump file created: %s", dumpFiles[0].Name())
	}
}
