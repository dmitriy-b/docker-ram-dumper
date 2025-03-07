package helpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	TestImageName     = "ram-dumper-test-image"
	TestContainerName = "ram-dumper-test-container"
	TestDumpsDir      = "/tmp/test-dumps"
)

type testContext struct {
	*testing.T
	containerName string
	imageName     string
	context       context.Context
}

func NewTestContext(t *testing.T, containerName string, imageName string) *testContext {
	return &testContext{t, containerName, imageName, context.Background()}
}

func (ctx *testContext) Context() context.Context {
	return ctx.context
}

func StartTestContainer(ctx *testContext) string {
	t := ctx.T
	containerName := ctx.containerName
	imageName := ctx.imageName
	if containerName == "" {
		containerName = TestContainerName
	}
	if imageName == "" {
		imageName = TestImageName
	}
	dockerCtx := ctx.Context()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}

	fmt.Println("Creating container:", containerName)
	hostConfig := &container.HostConfig{
		Privileged: true,
		SecurityOpt: []string{
			"seccomp=unconfined",
			"apparmor=unconfined",
		},
		CapAdd: []string{
			"SYS_PTRACE",
			"SYS_ADMIN",
		},
		Binds: []string{
			fmt.Sprintf("%s:/tmp/dumps", TestDumpsDir),
		},
	}

	resp, err := cli.ContainerCreate(dockerCtx, &container.Config{
		Image: imageName,
		Cmd:   []string{"sleep", "infinity"},
	}, hostConfig, nil, nil, containerName)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	if err := cli.ContainerStart(dockerCtx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	return resp.ID
}

func RunStressCommand(containerID string, vmBytes string, timeout string) ([]byte, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %v", err)
	}

	cmd := []string{
		"run-memory-stress",
		vmBytes,
		strings.TrimSuffix(timeout, "s"),
	}
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := cli.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec : %v", err)
	}

	resp, err := cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach to exec: %v", err)
	}
	defer resp.Close()

	outputCh := make(chan []byte)
	errCh := make(chan error)
	go func() {
		output, err := io.ReadAll(resp.Reader)
		if err != nil {
			errCh <- fmt.Errorf("failed to read exec output: %v", err)
			return
		}
		outputCh <- output
	}()

	err = cli.ContainerExecStart(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to start exec: %v", err)
	}

	select {
	case output := <-outputCh:
		return output, nil
	case err := <-errCh:
		return nil, err
	}
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
	fmt.Println("Removing test container:", TestContainerName)
	exec.Command("docker", "rm", "-f", TestContainerName).Run()
}

func RunDockerRamDumper(flags map[string]string) ([]byte, error) {
	baseCmd := []string{
		"docker", "run",
		"--cap-add=SYS_PTRACE",
		"--cap-add=SYS_ADMIN",
		"--security-opt", "seccomp=unconfined",
		"--security-opt", "apparmor=unconfined",
		"--privileged",
		"--user=root",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", fmt.Sprintf("%s:/tmp/dumps", TestDumpsDir),
		"-v", "/tmp/diagnostics:/tmp/diagnostics",
		"--net=host",
		"-i", "docker-ram-dumper",
	}

	for key, value := range flags {
		baseCmd = append(baseCmd, fmt.Sprintf("-%s=%s", key, value))
	}

	cmd := exec.Command(baseCmd[0], baseCmd[1:]...)
	return cmd.CombinedOutput()
}

// DockerStats struct to parse Docker stats JSON response
type DockerStats struct {
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
}

var GetContainerMemoryUsage = func(client *http.Client, containerID, baseDockerURL string, printStats bool) (float64, uint64, error) {
	// Docker API endpoint for container stats
	url := fmt.Sprintf("%s/containers/%s/stats?stream=false", baseDockerURL, containerID)

	// Send the request
	resp, err := client.Get(url)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	// Parse the JSON response
	var stats DockerStats
	err = json.Unmarshal(body, &stats)
	if err != nil {
		return 0, 0, err
	}

	// Calculate memory usage percentage
	memUsage := float64(stats.MemoryStats.Usage) / float64(stats.MemoryStats.Limit) * 100
	if printStats {
		fmt.Printf("Docker RAM limit: %d MB\n", stats.MemoryStats.Limit/1024/1024)
	}
	fmt.Printf("Container memory usage: %d MB\n", stats.MemoryStats.Usage/1024/1024)
	return memUsage, stats.MemoryStats.Limit / 1024 / 1024, nil
}

var ExecInContainer = func(client *http.Client, containerName, baseDockerURL string, command ...string) (string, error) {
	// Prepare the command execution request
	execConfig := map[string]interface{}{
		"AttachStdout": true,
		"AttachStderr": true,
		"Cmd":          command,
	}
	jsonData, err := json.Marshal(execConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal exec config: %v", err)
	}

	// Create exec instance
	createURL := fmt.Sprintf("%s/containers/%s/exec", baseDockerURL, containerName)
	resp, err := client.Post(createURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create exec instance: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to create exec instance: HTTP status %d", resp.StatusCode)
	}

	var execResponse struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&execResponse); err != nil {
		return "", fmt.Errorf("failed to decode exec response: %v", err)
	}

	// Start exec instance
	startURL := fmt.Sprintf("%s/exec/%s/start", baseDockerURL, execResponse.ID)
	startConfig := map[string]interface{}{"Detach": false}
	jsonData, _ = json.Marshal(startConfig)
	resp, err = client.Post(startURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to start exec instance: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to start exec instance: HTTP status %d", resp.StatusCode)
	}

	var output bytes.Buffer
	_, err = io.Copy(&output, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read exec output: %v", err)
	}

	return output.String(), nil
}

func GetPIDInContainer(client *http.Client, containerName, processName, baseDockerURL string) (int, error) {
	command := []string{"sh", "-c", fmt.Sprintf("ps -ef | grep '%s' | grep -v grep | tail -n1", processName)}
	output, err := ExecInContainer(client, containerName, baseDockerURL, command...)
	if err != nil {
		return 0, fmt.Errorf("failed to execute command in container: %v", err)
	}

	// Trim any whitespace and non-printable characters
	fields := strings.Fields(output)
	var pidStr string
	for _, field := range fields {
		if _, err := strconv.Atoi(field); err == nil {
			pidStr = field
			break
		}
	}
	if pidStr == "" {
		return 0, fmt.Errorf("no process found with name: %s. Output: %s", processName, output)
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID (%q): %v", pidStr, err)
	}

	return pid, nil
}

func CopyFromContainer(client *http.Client, containerName, srcPath, dstPath, baseDockerURL string) error {
	// Docker API endpoint for copying files from a container
	url := fmt.Sprintf("%s/containers/%s/archive?path=%s", baseDockerURL, containerName, srcPath)

	// Send GET request to Docker API
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to send request to Docker API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to copy file from container: %s. (HTTP status %d)", srcPath, resp.StatusCode)
	}

	// Create the destination file
	dstFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer dstFile.Close()

	// Copy the content from the response body to the destination file
	_, err = io.Copy(dstFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to copy file content: %v", err)
	}
	fmt.Printf("Copied file from container: %s to host: %s\n", srcPath, dstPath)
	return nil
}

func RunCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.CombinedOutput()
}
