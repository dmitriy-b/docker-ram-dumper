# Docker RAM Dumper

Docker RAM Dumper is a Go-based tool designed to monitor memory usage of a specified Docker container and create memory dumps when usage exceeds a defined threshold.

## Features

- Monitor memory usage of a specified Docker container
- Create memory dumps when usage exceeds a threshold
- Configurable process name, dump directories, and check intervals
- Continuous monitoring option (the tool will create a dump every X seconds)

## Prerequisites

- Go 1.20 or later
- Docker installed and running
- Access to Docker socket (/var/run/docker.sock)
- [procdump](https://github.com/Sysinternals/ProcDump-for-Linux) installed in the container (if not, it will be installed by the tool)
- ps, grep, awk installed in the container or passed from host (to get the pid of the process to dump)

## Installation

1. Clone the repository:
   ```
   git clone https://github.com/yourusername/docker-ram-dumper.git
   ```

2. Navigate to the project directory:
   ```
   cd docker-ram-dumper
   ```

3. Build the project:
   ```
   go build -o docker-ram-dumper cmd/docker-ram-dumper/main.go
   ```

## Usage

Run the tool with the following command:

```
./docker-ram-dumper -container <container_name> -process <process_name> -threshold <memory_threshold> -interval <check_interval> -dumpdir-container <dump_directory_in_container> -dumpdir-host <dump_directory_on_host> -monitor
```

### Flags

- `-threshold float`: Memory usage threshold percentage (default 90.0)
- `-process string`: Name of the process to monitor (default "dotnet")
- `-dumpdir-container string`: Directory to store memory dumps inside the container (default "/tmp/dumps")
- `-dumpdir-host string`: Directory to store memory dumps on the host (default "/tmp/dumps")
- `-container string`: Name of the container to monitor (default "sedge-node")
- `-interval duration`: Interval between memory checks (default 30s)
- `-monitor`: Continuously monitor memory usage (default false)
- `-dumps-count int`: Number of memory dumps to create before stopping (default 1)
- `-cleanup`: Clean up dumps in container after copying memory dump to host (default false)
- `-base-docker-url string`: Base Docker URL (default "http://localhost")

### Example

To monitor a container named "my-container" for a process named "myapp", with a memory threshold of 85%, checking every minute, and continuously monitoring:

```
./docker-ram-dumper -container my-container -process myapp -threshold 85 -interval 1m -monitor
```

## Running inside docker container

To run the tool inside a docker container, you can use the following command:

```
docker build . -t docker-ram-dumper:latest
docker run -v /var/run/docker.sock:/var/run/docker.sock -v $PWD/dumps:/tmp/dumps --net=host -it docker-ram-dumper:latest -threshold=<memory_threshold> -process=<process_name> -container=<container_name>
```

It is possible to pass procdump, ps, grep and awk from host to avoid installing them in the container:

```
docker run -v /var/run/docker.sock:/var/run/docker.sock -v $PWD/dumps:/tmp/dumps -v /usr/bin/procdump:/usr/bin/procdump -v /usr/bin/ps:/usr/bin/ps -v /usr/bin/grep:/usr/bin/grep -v /usr/bin/awk:/usr/bin/awk --net=host -it docker-ram-dumper:latest -threshold=<memory_threshold> -process=<process_name> -container=<container_name>
```

### Download docker image from github container registry

1. Create a [personal access token ](https://github.com/settings/tokens)(PAT) on github with repo access
2. Set the PAT as an environment variable

```
export CR_PAT=<your_pat>
```
3. Login to github container registry

```
echo $CR_PAT | docker login ghcr.io -u USERNAME --password-stdin
```

4. Download the image

```
docker pull ghcr.io/dmitriy-b/docker-ram-dumper:main
```

## How it works

1. The tool connects to the Docker daemon and retrieves memory usage statistics for the specified container.
2. If memory usage exceeds the threshold, it installs procdump in the container (if not already present).
3. It then uses procdump to create a memory dump of the specified process.
4. The dump file is copied from the container to the host machine.
5. If continuous monitoring is enabled, the tool repeats this process at the specified interval.

## Notes

- Ensure that the user running the tool has permission to access the Docker socket.
- The tool requires the ability to execute commands inside the target container and copy files from it.
- Memory dumps can be large, so ensure sufficient disk space is available in both the container and on the host.

## License

[MIT License](LICENSE)