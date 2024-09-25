package integration_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	testImageName     = "ram-dumper-test-image"
	testContainerName = "ram-dumper-test-container"
)

func TestMain(m *testing.M) {
	// Clean up
	defer removeTestContainer()

	// Ensure dump directory exists
	err := os.MkdirAll("/tmp/test-dumps", 0o755)
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
	containerID := startTestContainer(t)
	defer stopAndRemoveContainer(t, containerID)
	// Clean up the test dumps
	defer os.RemoveAll("/tmp/test-dumps")

	// Run the memory stress command in the background
	errCh := make(chan error, 1)
	go func() {
		errCh <- runStressCommand(containerID)
	}()
	time.Sleep(5 * time.Second)
	// Run your main program inside a Docker container in parallel
	outputCh := make(chan []byte, 1)
	go func() {
		cmd := exec.Command("docker", "run",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-v", "/tmp/test-dumps:/tmp/dumps",
			"--net=host",
			"-i", "docker-ram-dumper", // Removed the -it flag
			"-threshold=1",
			"-process=stress-ng-vm",
			"-container=ram-dumper-test-container")

		output, err := cmd.CombinedOutput()
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
	dumpFiles, err := os.ReadDir("/tmp/test-dumps")
	if err != nil {
		t.Fatalf("Failed to read dump directory: %v", err)
	}

	if len(dumpFiles) == 0 {
		t.Errorf("No dump files were created")
	} else {
		t.Logf("Dump file created: %s", dumpFiles[0].Name())
	}
}

func startTestContainer(t *testing.T) string {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: testImageName,
		Cmd:   []string{"sleep", "infinity"},
	}, nil, nil, nil, testContainerName)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	return resp.ID
}

func runStressCommand(containerID string) error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %v", err)
	}

	// Updated command to use 90% of memory
	cmd := []string{"stress-ng", "--vm", "1", "--vm-bytes", "50%", "--vm-hang", "0", "--timeout", "15s"}
	execConfig := types.ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec : %v", err)
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec: %v", err)
	}
	defer resp.Close()

	// Read the output of the command
	_, err = io.ReadAll(resp.Reader)
	if err != nil {
		return fmt.Errorf("failed to read exec output: %v", err)
	}
	// t.Logf("stress-ng output: %s", output)

	err = cli.ContainerExecStart(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("failed to start exec: %v", err)
	}

	// Wait for the stress command to take effect
	time.Sleep(5 * time.Second)
	return nil // or the actual error
}

func stopAndRemoveContainer(t *testing.T, containerID string) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}

	err = cli.ContainerStop(ctx, containerID, container.StopOptions{})
	if err != nil {
		t.Fatalf("Failed to stop container: %v", err)
	}

	err = cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	if err != nil {
		t.Fatalf("Failed to remove container: %v", err)
	}
}

func removeTestContainer() {
	exec.Command("docker", "rm", "-f", testContainerName).Run()
}
