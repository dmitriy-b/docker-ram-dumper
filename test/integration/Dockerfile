FROM ubuntu:24.04

# Install necessary packages
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        stress-ng \
        wget \
        gdb \
        lldb \
        ca-certificates \
        strace && \
    rm -rf /var/lib/apt/lists/*

# Add Microsoft package repository and install procdump and dotnet
RUN wget -q https://packages.microsoft.com/config/ubuntu/24.04/packages-microsoft-prod.deb -O packages-microsoft-prod.deb && \
    dpkg -i packages-microsoft-prod.deb && \
    rm packages-microsoft-prod.deb && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        dotnet-sdk-8.0 && \
        apt-get install -y --no-install-recommends procdump=1.2-4 > /tmp/test.txt \
        && cat /tmp/test.txt \
        && rm -rf /var/lib/apt/lists/*


# Create directories and set permissions
RUN mkdir -p /var/dumps /tmp/diagnostics && \
    chmod 1777 /var/dumps && \
    chmod 1777 /tmp && \
    chmod 1777 /tmp/diagnostics

# Install dotnet-dump and configure paths
RUN dotnet tool install --global dotnet-dump --version 6.0.351802 && \
    mkdir -p /root/.dotnet/tools && \
    chmod -R 777 /root/.dotnet && \
    ln -s /root/.dotnet/tools/dotnet-dump /usr/local/bin/dotnet-dump

# Create a simple .NET memory stress test program
COPY <<-"EOF" /root/MemoryStress.cs
using System;
using System.Collections.Generic;
using System.Threading;
using System.IO;

class Program
{
    static List<byte[]> memoryHog = new List<byte[]>();
    
    static void Main(string[] args)
    {
        if (args.Length < 2)
        {
            Console.WriteLine("Usage: MemoryStress <percentage> <duration_seconds>");
            return;
        }

        int percentage = int.Parse(args[0].TrimEnd('%'));
        int durationSeconds = int.Parse(args[1]);
        
        // Get container memory limit from cgroups
        long totalMemory = GetContainerMemoryLimit();
        long targetMemory = totalMemory * percentage / 100;
        
        Console.WriteLine($"Container memory limit: {totalMemory / (1024*1024)} MB");
        Console.WriteLine($"Target memory: {targetMemory / (1024*1024)} MB ({percentage}%)");
        Console.WriteLine($"Current memory: {GC.GetTotalMemory(false) / (1024*1024)} MB");
        
        try
        {
            // Use smaller chunks for faster allocation
            const int chunkSize = 50 * 1024 * 1024; // 50MB chunks instead of 500MB
            while (GC.GetTotalMemory(false) < targetMemory)
            {
                memoryHog.Add(new byte[chunkSize]);
                Array.Fill(memoryHog[memoryHog.Count - 1], (byte)1);
                Console.WriteLine($"Allocated chunk {memoryHog.Count}, total memory: {GC.GetTotalMemory(false) / (1024*1024)} MB");
                Thread.Sleep(10); // Small delay to prevent overwhelming the system
            }
            
            Console.WriteLine($"Allocated {GC.GetTotalMemory(false) / (1024*1024)} MB");
            // Keep memory allocated
            var endTime = DateTime.Now.AddSeconds(durationSeconds);
            while (DateTime.Now < endTime)
            {
                GC.KeepAlive(memoryHog);
                Thread.Sleep(100);
            }
        }
        catch (Exception ex)
        {
            Console.WriteLine($"Error: {ex.Message}");
        }
    }

    static long GetContainerMemoryLimit()
    {
        try
        {
            string memLimit = File.ReadAllText("/sys/fs/cgroup/memory/memory.limit_in_bytes");
            if (long.TryParse(memLimit.Trim(), out long limit))
            {
                return limit;
            }
        }
        catch
        {
            // Fallback if cgroups v1 is not available
            try
            {
                string memLimit = File.ReadAllText("/sys/fs/cgroup/memory.max");
                if (long.TryParse(memLimit.Trim(), out long limit))
                {
                    return limit;
                }
            }
            catch
            {
                // If both fail, use a default value
                return 8L * 1024 * 1024 * 1024; // 8GB
            }
        }
        return 8L * 1024 * 1024 * 1024; // 8GB default
    }
}
EOF

# Compile the .NET stress test program
RUN dotnet new console -n MemoryStress -o /root/MemoryStress && \
    cp /root/MemoryStress.cs /root/MemoryStress/Program.cs && \
    cd /root/MemoryStress && \
    dotnet build -c Release

# Create a script to run the memory stress test
COPY <<-"EOF" /usr/local/bin/run-memory-stress
#!/bin/bash
cd /root/MemoryStress/bin/Release/net8.0
./MemoryStress "$@"
EOF

RUN chmod +x /usr/local/bin/run-memory-stress

# Create a script to handle dumps with proper permissions
COPY <<-"EOF" /usr/local/bin/create-dump
#!/bin/bash
set -x  # Enable debug output

# Ensure all output goes to stderr for docker logs
exec 1>&2

pid=$1
output_dir=$2
output_file="${output_dir}/core_${pid}_$(date +%s).dmp"

# Use procdump with default settings
procdump \
    -ma \
    -accepteula \
    --privileged \
    --cap-add=SYS_PTRACE \
    --cap-add=SYS_ADMIN \
    --security-opt seccomp=unconfined \
    --security-opt apparmor=unconfined \
    $pid \
    "$output_file"

# Check if the file was created
if [ -f "$output_file" ]; then
    echo "Dump file exists with size: $(ls -lh $output_file)"
    chmod 666 $output_file
    exit 0
fi

exit 1
EOF

RUN chmod +x /usr/local/bin/create-dump

# Add dotnet tools to PATH (only essential environment variables during build)
ENV PATH="${PATH}:/root/.dotnet/tools" \
    TMPDIR="/tmp"

# Verify installation
RUN dotnet --info && \
    dotnet-dump --help && \
    ls -la /usr/local/bin/create-dump && \
    ls -la /root/.dotnet/tools/dotnet-dump

# Set diagnostic environment variables at runtime via entrypoint script
COPY <<-"EOF" /usr/local/bin/entrypoint.sh
#!/bin/bash
set -x  # Enable debug output

# Set up diagnostic environment
export TMPDIR="/tmp"

# Print diagnostic information
echo "=== ENTRYPOINT ENVIRONMENT ==="
env | sort

# Execute the command
echo "Executing command: $@"
exec "$@"
EOF

RUN chmod +x /usr/local/bin/entrypoint.sh

# Add analyze script
COPY <<-"EOF" /usr/local/bin/analyze-dump
#!/bin/bash
dotnet-dump analyze "$@"
EOF

RUN chmod +x /usr/local/bin/analyze-dump

RUN wget -O dotMemoryclt.zip https://www.nuget.org/api/v2/package/JetBrains.dotMemory.Console.linux-x64 && \
    apt-get update && apt-get install -y unzip && \
    unzip dotMemoryclt.zip -d ./dotMemoryclt && \
    chmod +x -R dotMemoryclt/*

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
