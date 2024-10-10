package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	helpers "github.com/NethermindEth/docker-ram-dumper/internal/_helpers"
)

func main() {
	var (
		threshold        string
		thresholdValue   float64
		isPercentage     bool
		processName      string
		dumpDirContainer string
		dumpDirHost      string
		containerName    string
		checkInterval    time.Duration
		monitor          bool
		dumpsCount       int
		cleanup          bool
		baseDockerURL    string
		dumpTool         string
		globalTimeout    time.Duration
	)

	flag.StringVar(&threshold, "threshold", "90%", "Memory usage threshold (e.g., '90%' or '1000MB')")
	flag.StringVar(&processName, "process", "dotnet", "Name of the process to monitor")
	flag.StringVar(&dumpDirContainer, "dumpdir-container", "/tmp/dumps", "Directory to store memory dumps inside the container")
	flag.StringVar(&dumpDirHost, "dumpdir-host", "/tmp/dumps", "Directory to store memory dumps on the host")
	flag.StringVar(&containerName, "container", "sedge-node", "Name of the container to monitor")
	flag.DurationVar(&checkInterval, "interval", 30*time.Second, "Interval between memory checks")
	flag.BoolVar(&monitor, "monitor", false, "Continuously monitor memory usage")
	flag.IntVar(&dumpsCount, "dumps-count", 1, "Number of memory dumps to create before stopping")
	flag.BoolVar(&cleanup, "cleanup", false, "Clean up dumps in container after a memory dump")
	flag.StringVar(&baseDockerURL, "docker-url", "http://localhost", "Base URL for Docker API")
	flag.StringVar(&dumpTool, "dump-tool", "procdump", "Tool to use for memory dump (procdump or dotnet-dump)")
	flag.DurationVar(&globalTimeout, "timeout", 0, "Global timeout for the application (e.g., 1h, 30m, 1h30m)")

	flag.Parse()

	isPercentage = !strings.HasSuffix(strings.ToLower(threshold), "mb")
	thresholdStr := strings.TrimSuffix(strings.ToLower(threshold), "%")
	thresholdStr = strings.TrimSuffix(thresholdStr, "mb")
	thresholdValue, _ = strconv.ParseFloat(thresholdStr, 64)

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
	_, totalMemory, _ := helpers.GetContainerMemoryUsage(client, containerName, baseDockerURL, true)
	var totalMemoryThreshold float64
	if isPercentage {
		totalMemoryThreshold = float64(totalMemory) * thresholdValue / 100
	} else {
		totalMemoryThreshold = thresholdValue
		thresholdValue = thresholdValue / float64(totalMemory) * 100
	}
	fmt.Printf("Total memory threshold: %.0f%% (%.0f MB)\n", thresholdValue, totalMemoryThreshold)

	if monitor && globalTimeout == 0 {
		fmt.Println("Global timeout is not set. Setting it to 10 minutes. Use -timeout flag to set a different timeout.")
		globalTimeout = 10 * time.Minute
	}

	// Create a context with the global timeout
	ctx := context.Background()
	if globalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, globalTimeout)
		defer cancel()
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("Global timeout: %v reached. Use -timeout flag to set a different timeout. Exiting. \n", globalTimeout)
			return
		default:
			// Get memory usage
			memUsagePercent, _, err := helpers.GetContainerMemoryUsage(client, containerName, baseDockerURL, false)

			if err != nil {
				fmt.Println("Error getting memory usage:", err)
				if !monitor {
					fmt.Println("'-monitor' flag is set to false. Stopping.")
					return
				}
				time.Sleep(checkInterval)
				continue
			}

			fmt.Printf("Memory usage is %.2f%%\n", memUsagePercent)

			if memUsagePercent >= thresholdValue {
				fmt.Println("Memory usage threshold exceeded. Initiating memory dump...")

				// Install dependencies inside the target container
				err := installDumpTool(client, containerName, dumpTool, baseDockerURL)
				if err != nil {
					fmt.Println("Error installing dump tool:", err)
					time.Sleep(checkInterval)
					continue
				}

				// Get the PID of the processName process inside the target container
				pid, err := helpers.GetPIDInContainer(client, containerName, processName, baseDockerURL)
				if err != nil {
					fmt.Println("Error getting PID:", err)
					time.Sleep(checkInterval)
					continue
				} else {
					fmt.Printf("PID of %s is %d\n", processName, pid)
				}

				// Create a dump directory inside the container
				_, err = helpers.ExecInContainer(client, containerName, baseDockerURL, "mkdir", "-p", "/tmp/dumps")
				if err != nil {
					fmt.Println("Error creating dump directory in container:", err)
					time.Sleep(checkInterval)
					return
				}

				// Run the selected dump tool inside the target container
				dumpFile := fmt.Sprintf("%s/core_%d_%d.dmp", dumpDirContainer, pid, time.Now().Unix())
				dumpOutput, err := createMemoryDump(client, containerName, dumpTool, pid, dumpFile, totalMemoryThreshold, baseDockerURL, checkInterval)
				if err != nil {
					fmt.Println("Error creating dump:", err)
					fmt.Printf("Command output: %s\n", dumpOutput)
					time.Sleep(checkInterval)
					continue
				}

				fmt.Printf("Memory dump saved to %s inside the target container.\n", dumpFile)

				// Copy the dump file from the target container to the host
				hostDumpFile := filepath.Join(dumpDirHost, filepath.Base(dumpFile))
				if dumpTool == "procdump" {
					dumpFile = dumpFile + "_0." + strconv.Itoa(pid)
				}
				err = helpers.CopyFromContainer(client, containerName, dumpFile, hostDumpFile, baseDockerURL)
				if err != nil {
					fmt.Println("Error copying dump file (dumpFile) to host:", err)
					fmt.Printf("Command output: %s\n", dumpOutput)
				} else {
					fmt.Printf("Dump file copied to host: %s\n", hostDumpFile)
				}

				dumpCounter++
				if dumpCounter >= dumpsCount {
					fmt.Printf("Reached the limit of %d dumps. Stopping.\n", dumpsCount)
					return
				}
			} else {
				fmt.Printf("Memory usage (%.2f%%) is below the threshold (%.2f%%).\n", memUsagePercent, thresholdValue)
				if !monitor {
					fmt.Println("'-monitor' flag is set to false. Dumping only once. Stopping.")
					return
				}
				fmt.Println("Waiting for memory usage to exceed the threshold...")
			}

			time.Sleep(checkInterval)
		}
	}
}

func cleanupDumps(client *http.Client, containerName, dumpDirContainer, baseDockerURL string) error {
	_, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "rm", "-rf", dumpDirContainer)
	if err != nil {
		return fmt.Errorf("error cleaning up dumps in container: %v", err)
	} else {
		fmt.Println("Successfully cleaned up dumps in container.")
	}
	return nil
}

func installDumpTool(client *http.Client, containerName, dumpTool, baseDockerURL string) error {
	switch dumpTool {
	case "procdump":
		// Check if procdump is already installed
		_, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "which", "procdump")
		if err != nil {
			fmt.Println("Procdump not found. Installing...")
			_, err = helpers.ExecInContainer(client, containerName, baseDockerURL, "sh", "-c", "apk add --no-cache procdump || apt-get update && apt-get install -y procdump")
			if err != nil {
				return fmt.Errorf("error installing procdump: %v", err)
			}
			fmt.Println("Procdump installed successfully.")
		} else {
			fmt.Println("Procdump is already installed.")
		}
	case "dotnet-dump":
		// Check if dotnet-dump is already installed
		_, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "which", "dotnet-dump")
		if err != nil {
			fmt.Println("dotnet-dump not found. Installing...")
			_, err = helpers.ExecInContainer(client, containerName, baseDockerURL, "sh", "-c", "apt-get update && apt-get install -y curl && curl -sSL https://dot.net/v1/dotnet-install.sh -o dotnet-install.sh && chmod +x dotnet-install.sh && ./dotnet-install.sh --channel 7.0 --install-dir /root/.dotnet && dotnet tool install --global dotnet-dump")
			if err != nil {
				return fmt.Errorf("error installing dotnet-dump: %v", err)
			}
			fmt.Println("dotnet-dump installed successfully.")
		} else {
			fmt.Println("dotnet-dump is already installed.")
		}
	default:
		return fmt.Errorf("unsupported dump tool: %s", dumpTool)
	}
	return nil
}

func createMemoryDump(client *http.Client, containerName, dumpTool string, pid int, dumpFile string, totalMemoryThreshold float64, baseDockerURL string, checkInterval time.Duration) (string, error) {
	var cmd []string
	switch dumpTool {
	case "procdump":
		cmd = []string{"procdump", "-d", "-n", "1", "-s", "1", "-M", fmt.Sprintf("%.0f", totalMemoryThreshold), "-p", fmt.Sprintf("%d", pid), "-o", dumpFile}
		return helpers.ExecInContainer(client, containerName, baseDockerURL, cmd...)
	case "dotnet-dump":
		// Create a wrapper function to check memory usage before running dotnet-dump
		return createDotnetDump(client, containerName, pid, dumpFile, totalMemoryThreshold, baseDockerURL, checkInterval)
	default:
		return "", errors.New("unsupported dump tool")
	}
}

func createDotnetDump(client *http.Client, containerName string, pid int, dumpFile string, totalMemoryThreshold float64, baseDockerURL string, checkInterval time.Duration) (string, error) {
	for {
		memUsagePercent, memoryUsageMB, err := helpers.GetContainerMemoryUsage(client, containerName, baseDockerURL, false)
		if err != nil {
			return "", fmt.Errorf("failed to get memory usage: %v", err)
		}

		if float64(memoryUsageMB) >= totalMemoryThreshold {
			cmd := []string{"/root/.dotnet/tools/dotnet-dump", "collect", "-p", fmt.Sprintf("%d", pid), "-o", dumpFile}
			return helpers.ExecInContainer(client, containerName, baseDockerURL, cmd...)
		} else {
			fmt.Printf("Memory usage is %.2f%% (%.0f MB). Waiting for memory usage to exceed %.0f%% (%.0f MB)...\n",
				memUsagePercent,
				float64(memoryUsageMB),
				totalMemoryThreshold,
				totalMemoryThreshold)
		}

		time.Sleep(checkInterval)
	}
}
