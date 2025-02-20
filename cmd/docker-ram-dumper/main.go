package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	helpers "github.com/NethermindEth/docker-ram-dumper/internal/_helpers"
)

var (
	dotMemoryTimeout string
	dotMemoryVersion string
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
		installOnly      bool
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
	flag.StringVar(&dumpTool, "dump-tool", "procdump", "Tool to use for memory dump (procdump, dotnet-dump, dotMemory)")
	flag.DurationVar(&globalTimeout, "timeout", 0, "Global timeout for the application (e.g., 1h, 30m, 1h30m)")
	flag.StringVar(&dotMemoryTimeout, "dotmemory-timeout", "30s", "Timeout for dotMemory tool")
	flag.StringVar(&dotMemoryVersion, "dotmemory-version", "2024.3.5", "Version of dotMemory tool")
	flag.BoolVar(&installOnly, "install", false, "Install dump tool and exit")
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

	// If install-only mode is enabled, install the tool and exit
	if installOnly {
		fmt.Printf("Installing %s dump tool...\n", dumpTool)
		output, err := installDumpTool(client, containerName, dumpTool, baseDockerURL)
		if err != nil {
			fmt.Printf("Failed to install %s: %v\n", dumpTool, err)
			os.Exit(1)
		}
		fmt.Printf("Successfully installed %s\nOutput: \n\n%s\n", dumpTool, output)
		os.Exit(0)
	}

	if cleanup {
		defer cleanupDumps(client, containerName, dumpDirContainer, baseDockerURL)
		defer killProcess(client, containerName, dumpTool, baseDockerURL)
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
			fmt.Printf("Global timeout: %v has been reached. Use -timeout flag to increase the timeout. Exiting the loop... \n", globalTimeout)
			fmt.Println("Goodbye!")
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
				_, err := installDumpTool(client, containerName, dumpTool, baseDockerURL)
				if err != nil {
					fmt.Println("Error installing dump tool:", err)
					time.Sleep(checkInterval)
					return
				}

				// Get the PID of the processName process inside the target container
				pid, err := helpers.GetPIDInContainer(client, containerName, processName, baseDockerURL)
				if err != nil {
					fmt.Println("Error getting PID:", err)
					fmt.Println("Please check if the processName is correct and if the container is running.")
					return
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

				if dumpTool == "procdump" {
					dumpFile = dumpFile + "_0." + strconv.Itoa(pid)
				}
				if dumpTool == "dotMemory" {
					// replace ".dmp" with ".dmw"
					dumpFile = dumpFile + ".dmw"
				}
				// Copy the dump file from the target container to the host
				hostDumpFile := filepath.Join(dumpDirHost, filepath.Base(dumpFile))
				fmt.Printf("Trying to save memory dump to %s inside the target container ...\n", hostDumpFile)
				// _ = helpers.CopyFromContainer(client, containerName, dumpFile, dumpFile, baseDockerURL)

				cmd := exec.Command("docker", "cp", fmt.Sprintf("%s:%s", containerName, dumpFile), dumpFile)
				output, err := cmd.CombinedOutput()
				if err != nil {
					fmt.Println("Error copying dump file (dumpFile) to host:", err)
					fmt.Printf("Command output: %s\n", output)
				} else {
					fmt.Printf("Dump file copied to container: %s. Use docker volumes to get it\n", dumpFile)
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
				fmt.Println("___")
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

func killProcess(client *http.Client, containerName, processName, baseDockerURL string) error {
	processes, _ := helpers.ExecInContainer(client, containerName, baseDockerURL, "ps", "aux")
	fmt.Println("Active processes:\n", processes)
	_, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "pkill", "-f", processName)
	if err != nil {
		return fmt.Errorf("error killing process: %v", err)
	} else {
		fmt.Println("Successfully killed " + processName + " process.")
	}
	return nil
}

func installDumpTool(client *http.Client, containerName, dumpTool, baseDockerURL string) (string, error) {
	switch dumpTool {
	case "procdump":
		// Check if procdump is already installed
		which, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "which", "procdump")
		if err != nil {
			fmt.Println("Procdump not found. Installing...")
			result, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "sh", "-c", "apk add --no-cache procdump || apt-get update && apt-get install -y procdump")
			if err != nil {
				return "", fmt.Errorf("error installing procdump: %v", err)
			}
			fmt.Println("Procdump installed successfully.")
			return result, nil
		} else {
			fmt.Printf("Procdump is already installed: %s\n", which)
			return which, nil
		}
	case "dotnet-dump":
		// Check if dotnet-dump is already installed
		which, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "ls", "/root/.dotnet/tools/dotnet-dump")
		if err != nil || strings.Contains(which, "No such file or directory") {
			fmt.Println("dotnet-dump not found. Installing...")
			result, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "sh", "-c", "apt-get update && apt-get install -y dotnet-sdk-8.0 curl && curl -sSL https://dot.net/v1/dotnet-install.sh -o dotnet-install.sh && chmod +x dotnet-install.sh && ./dotnet-install.sh --channel 8.0 --install-dir /root/.dotnet && dotnet tool install --global dotnet-dump")
			if err != nil {
				return "", fmt.Errorf("error installing dotnet-dump: %v", err)
			}
			fmt.Println("dotnet-dump installed successfully.")
			return result, nil
		} else {
			fmt.Printf("dotnet-dump is already installed: %s\n", which)
			return which, nil
		}
	case "dotMemory":
		// Check if dotnet-dump is already installed
		which, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "ls", "/dotMemoryclt/dotmemory")
		if err != nil || strings.Contains(which, "No such file or directory") {
			fmt.Println("dotMemory not found. Installing...")
			dockerArch := "linux-arm64"
			if runtime.GOARCH == "amd64" {
				dockerArch = "linux-x64"
			} else if runtime.GOARCH == "arm64" {
				dockerArch = "linux-arm64"
			}
			result, err := helpers.ExecInContainer(client, containerName, baseDockerURL, "sh", "-c", "apt-get update && apt-get install -y curl && curl -L -o dotMemory.tar.gz https://download.jetbrains.com/resharper/dotUltimate."+dotMemoryVersion+"/JetBrains.dotMemory.Console."+dockerArch+"."+dotMemoryVersion+".tar.gz && mkdir -p /dotMemoryclt && tar -xzf dotMemory.tar.gz -C /dotMemoryclt && chmod +x -R /dotMemoryclt/*")
			if err != nil {
				return "", fmt.Errorf("error installing dotnet-dump: %v", err)
			}
			fmt.Println("dotMemory installed successfully.")
			return result, nil
		} else {
			fmt.Printf("dotMemory is already installed: %s\n", which)
			return which, nil
		}
	default:
		return "", fmt.Errorf("unsupported dump tool: %s", dumpTool)
	}
}

func createMemoryDump(client *http.Client, containerName, dumpTool string, pid int, dumpFile string, totalMemoryThreshold float64, baseDockerURL string, checkInterval time.Duration) (string, error) {
	var cmd []string
	switch dumpTool {
	case "procdump":
		cmd = []string{"procdump", "-d", "-n", "1", "-s", "1", "-M", fmt.Sprintf("%.0f", totalMemoryThreshold), "-p", fmt.Sprintf("%d", pid), "-o", dumpFile}
		return helpers.ExecInContainer(client, containerName, baseDockerURL, cmd...)
	case "dotnet-dump":
		// Create a wrapper function to check memory usage before running dotnet-dump
		return createDotnetDump(client, containerName, pid, dumpFile, totalMemoryThreshold, baseDockerURL, checkInterval, "dotnet-dump")
	case "dotMemory":
		// Create a wrapper function to check memory usage before running dotnet-dump
		return createDotnetDump(client, containerName, pid, dumpFile, totalMemoryThreshold, baseDockerURL, checkInterval, "dotMemory")
	default:
		return "", errors.New("unsupported dump tool")
	}
}

func createDotnetDump(client *http.Client, containerName string, pid int, dumpFile string, totalMemoryThreshold float64, baseDockerURL string, checkInterval time.Duration, tool string) (string, error) {
	for {
		memUsagePercent, memoryUsageMB, err := helpers.GetContainerMemoryUsage(client, containerName, baseDockerURL, false)
		if err != nil {
			return "", fmt.Errorf("failed to get memory usage: %v", err)
		}

		if float64(memoryUsageMB) >= totalMemoryThreshold {
			if tool == "dotnet-dump" {
				cmd := []string{"/root/.dotnet/tools/dotnet-dump", "collect", "-p", fmt.Sprintf("%d", pid), "-o", dumpFile}
				return helpers.ExecInContainer(client, containerName, baseDockerURL, cmd...)
			} else if tool == "dotMemory" {
				cmd := []string{"/dotMemoryclt/dotmemory", "attach", fmt.Sprintf("%d", pid), "--save-to-file=" + dumpFile, "--overwrite", "--trigger-on-activation", "--timeout=" + dotMemoryTimeout}
				fmt.Println("Executing command:", cmd)
				output, err := helpers.ExecInContainer(client, containerName, baseDockerURL, cmd...)
				// if unrecognized address, try to run dotmemory again
				const maxRetries = 5
				retryCount := 0
				for (strings.Contains(output, "unrecognized address") || strings.Contains(output, "Object reference not set to an instance of an object") || strings.Contains(output, "Non-writeable path")) && retryCount < maxRetries {
					fmt.Printf("Retrying command (attempt %d of %d)...\n", retryCount+1, maxRetries)
					if strings.Contains(output, "-writeable path") {
						// remove dump directory
						fmt.Println("Removing dump directory...")
						helpers.ExecInContainer(client, containerName, baseDockerURL, "rm", "-rf", "/tmp/dumps")
						time.Sleep(2 * time.Second)
					}
					// cmd = []string{"/dotMemoryclt/dotmemory", "get-snapshot", fmt.Sprintf("%d", pid), "--save-to-file=" + dumpFile, "--overwrite"}
					cmd = []string{"/dotMemoryclt/dotmemory", "attach", fmt.Sprintf("%d", pid), "--save-to-file=" + dumpFile, "--overwrite", "--trigger-on-activation", "--timeout=" + dotMemoryTimeout}
					output, err = helpers.ExecInContainer(client, containerName, baseDockerURL, cmd...)
					retryCount++
					if err != nil {
						fmt.Printf("Cannot save memory dump. Attempt %d failed: %v\n", retryCount, err)
					}
					time.Sleep(2 * time.Second) // Add small delay between retries
				}
				fmt.Println("dotMemory output:", output)
				files, _ := helpers.ExecInContainer(client, containerName, baseDockerURL, "ls", "-l", "/tmp/dumps")
				fmt.Println("Files in /tmp/dumps:", files)
				return output, err
			} else {
				return "", errors.New("unsupported dump tool: " + tool)
			}
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
