# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY . . 
RUN rm -rf ./dumps
RUN CGO_ENABLED=0 GOOS=linux go build -o docker-ram-dumper ./cmd/docker-ram-dumper

# Final stage
FROM alpine:latest

# Install Docker CLI
RUN apk add --no-cache \
    docker-cli \
    bash

RUN mkdir -p /tmp/dumps
WORKDIR /root/
COPY --from=builder /app/docker-ram-dumper .
ENTRYPOINT ["./docker-ram-dumper"]