package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	var (
		threshold        float64
		processName      string
		dumpDirContainer string
		dumpDirHost      string
		containerName    string
		checkInterval    time.Duration
		monitor          bool
		dumpsCount       int
		cleanup          bool
		baseDockerURL    string
	)

	flag.Float64Var(&threshold, "threshold", 90.0, "Memory usage threshold percentage")
	flag.StringVar(&processName, "process", "dotnet", "Name of the process to monitor")
	flag.StringVar(&dumpDirContainer, "dumpdir-container", "/tmp/dumps", "Directory to store memory dumps inside the container")
	flag.StringVar(&dumpDirHost, "dumpdir-host", "/tmp/dumps", "Directory to store memory dumps on the host")
	flag.StringVar(&containerName, "container", "sedge-node", "Name of the container to monitor")
	flag.DurationVar(&checkInterval, "interval", 30*time.Second, "Interval between memory checks")
	flag.BoolVar(&monitor, "monitor", false, "Continuously monitor memory usage")
	flag.IntVar(&dumpsCount, "dumps-count", 1, "Number of memory dumps to create before stopping")
	flag.BoolVar(&cleanup, "cleanup", false, "Clean up dumps in container after a memory dump")
	flag.StringVar(&baseDockerURL, "docker-url", "http://localhost", "Base URL for Docker API")

	flag.Parse()

	// Create a Unix socket HTTP client
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/docker.sock")
			},
		},
	}
	defer client.CloseIdleConnections()

	if cleanup {
		defer cleanupDumps(client, containerName, dumpDirContainer, baseDockerURL)
	}

	// Ensure dump directory exists
	err := os.MkdirAll(dumpDirHost, 0o755)
	if err != nil {
		fmt.Println("Error creating dump directory:", err)
		return
	}

	dumpCounter := 0

	for {
		// Get memory usage
		memUsagePercent, totalMemory, err := getContainerMemoryUsage(client, containerName, baseDockerURL)
		totalMemoryThreshold := float64(totalMemory) * threshold / 100
		fmt.Printf("Total memory threshold: %.0f%% (%.0f MB)\n", threshold, totalMemoryThreshold)
		if err != nil {
			fmt.Println("Error getting memory usage:", err)
			if !monitor {
				return
			}
			time.Sleep(checkInterval)
			continue
		}

		fmt.Printf("Memory usage is %.2f%%\n", memUsagePercent)

		if memUsagePercent >= threshold {
			fmt.Println("Memory usage threshold exceeded. Initiating memory dump...")

			// Install dependencies inside the target container
			// Check if procdump is already installed
			_, err = execInContainer(client, containerName, baseDockerURL, "which", "procdump")
			if err != nil {
				fmt.Println("Procdump not found. Installing...")
				_, err = execInContainer(client, containerName, baseDockerURL, "sh", "-c", "apk add --no-cache procdump || apt-get update && apt-get install -y procdump")
				if err != nil {
					fmt.Println("Error installing procdump:", err)
					time.Sleep(checkInterval)
					continue
				}
				fmt.Println("Procdump installed successfully.")
			} else {
				fmt.Println("Procdump is already installed.")
			}

			// Get the PID of the processName process inside the target container
			pid, err := getPIDInContainer(client, containerName, processName, baseDockerURL)
			if err != nil {
				fmt.Println("Error getting PID:", err)
				time.Sleep(checkInterval)
				continue
			} else {
				fmt.Printf("PID of %s is %d\n", processName, pid)
			}

			// Create a dump directory inside the container
			_, err = execInContainer(client, containerName, baseDockerURL, "mkdir", "-p", "/tmp/dumps")
			if err != nil {
				fmt.Println("Error creating dump directory in container:", err)
				time.Sleep(checkInterval)
				return
			}

			// Run ProcDump inside the target container
			dumpFile := fmt.Sprintf("%s/core_%d_%d.dmp", dumpDirContainer, pid, time.Now().Unix())
			dumpOutput, err := execInContainer(client, containerName, baseDockerURL, "procdump", "-d", "-n", "1", "-s", "1", "-M", fmt.Sprintf("%.0f", totalMemoryThreshold), "-p", fmt.Sprintf("%d", pid), "-o", dumpFile)
			if err != nil {
				fmt.Println("Error creating dump:", err)
				time.Sleep(checkInterval)
				continue
			}

			fmt.Printf("Memory dump saved to %s inside the target container.\n", dumpFile)

			// Copy the dump file from the target container to the host
			hostDumpFile := filepath.Join(dumpDirHost, filepath.Base(dumpFile))
			err = copyFromContainer(client, containerName, dumpFile+"_0."+strconv.Itoa(pid), hostDumpFile, baseDockerURL)
			if err != nil {
				fmt.Println("Error copying dump file from container:", err)
				fmt.Printf("Executed command: docker exec %s procdump -d -n 1 -s 1 -M %.0f -p %d %s\n", containerName, totalMemoryThreshold, pid, dumpFile)
				fmt.Printf("Command output: %s\n", dumpOutput)
			} else {
				fmt.Printf("Dump file copied to host: %s\n", hostDumpFile)
			}

			dumpCounter++
			if dumpCounter >= dumpsCount {
				fmt.Printf("Reached the limit of %d dumps. Stopping.\n", dumpsCount)
				return
			}
		}

		if !monitor {
			fmt.Println("Dumping only once. Stopping.")
			return
		}

		time.Sleep(checkInterval)
	}
}

func cleanupDumps(client *http.Client, containerName, dumpDirContainer, baseDockerURL string) error {
	_, err := execInContainer(client, containerName, baseDockerURL, "rm", "-rf", dumpDirContainer)
	if err != nil {
		return fmt.Errorf("error cleaning up dumps in container: %v", err)
	} else {
		fmt.Println("Successfully cleaned up dumps in container.")
	}
	return nil
}

func execInContainer(client *http.Client, containerName, baseDockerURL string, command ...string) (string, error) {
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

func getPIDInContainer(client *http.Client, containerName, processName, baseDockerURL string) (int, error) {
	command := []string{"sh", "-c", fmt.Sprintf("ps -ef | grep '%s' | grep -v grep | head -n1", processName)}
	output, err := execInContainer(client, containerName, baseDockerURL, command...)
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

func copyFromContainer(client *http.Client, containerName, srcPath, dstPath, baseDockerURL string) error {
	// Docker API endpoint for copying files from a container
	url := fmt.Sprintf("%s/containers/%s/archive?path=%s", baseDockerURL, containerName, srcPath)

	// Send GET request to Docker API
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to send request to Docker API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to copy file from container: HTTP status %d", resp.StatusCode)
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

	return nil
}

func getContainerMemoryUsage(client *http.Client, containerID, baseDockerURL string) (float64, uint64, error) {
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
	fmt.Printf("Container memory usage: %d MB\n", stats.MemoryStats.Usage/1024/1024)
	fmt.Printf("Docker RAM limit: %d MB\n", stats.MemoryStats.Limit/1024/1024)
	return memUsage, stats.MemoryStats.Limit / 1024 / 1024, nil
}

// Define structs to parse Docker stats JSON response
type DockerStats struct {
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
}
