# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY . . 
RUN rm -rf ./dumps
RUN CGO_ENABLED=0 GOOS=linux go build -o docker-ram-dumper ./cmd/docker-ram-dumper

# Final stage
FROM alpine:latest

# Install Docker CLI and required packages
RUN apk add --no-cache \
    docker-cli \
    bash \
    gdb \
    strace \
    procps \
    libc6-compat

RUN mkdir -p /tmp/dumps && chmod 1777 /tmp/dumps
WORKDIR /root/
COPY --from=builder /app/docker-ram-dumper .

# Set proper permissions for dump operations
ENV TMPDIR="/tmp"

ENTRYPOINT ["./docker-ram-dumper"]