package helpers

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	TestImageName     = "ram-dumper-test-image"
	TestContainerName = "ram-dumper-test-container"
)

func StartTestContainer(t *testing.T) string {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: TestImageName,
		Cmd:   []string{"sleep", "infinity"},
	}, nil, nil, nil, TestContainerName)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	return resp.ID
}

func RunStressCommand(containerID string, vmBytes string, timeout string) error {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %v", err)
	}

	cmd := []string{
		"stress-ng",
		"--vm", "1",
		"--vm-bytes", vmBytes,
		"--vm-hang", "0",
		"--timeout", timeout,
	}
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec : %v", err)
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec: %v", err)
	}
	defer resp.Close()

	_, err = io.ReadAll(resp.Reader)
	if err != nil {
		return fmt.Errorf("failed to read exec output: %v", err)
	}

	err = cli.ContainerExecStart(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("failed to start exec: %v", err)
	}

	time.Sleep(5 * time.Second)
	return nil
}

func StopAndRemoveContainer(t *testing.T, containerID string) {
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

func RemoveTestContainer() {
	exec.Command("docker", "rm", "-f", TestContainerName).Run()
}

func RunDockerRamDumper(args map[string]string) ([]byte, error) {
	baseCmd := []string{
		"docker", "run",
		"--cap-add=SYS_PTRACE",
		"--security-opt", "seccomp=unconfined",
		"--user=root",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", "/tmp/test-dumps:/tmp/dumps",
		"--net=host",
		"-i", "docker-ram-dumper",
	}

	for key, value := range args {
		baseCmd = append(baseCmd, fmt.Sprintf("-%s=%s", key, value))
	}

	cmd := exec.Command(baseCmd[0], baseCmd[1:]...)
	return cmd.CombinedOutput()
}
