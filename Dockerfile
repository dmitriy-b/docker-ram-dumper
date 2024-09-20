# Build stage
FROM golang:1.17-alpine AS builder

WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o docker-ram-dumper ./cmd/docker-ram-dumper

# Final stage
FROM alpine:latest

WORKDIR /root/
COPY --from=builder /app/docker-ram-dumper .
ENTRYPOINT ["./docker-ram-dumper"]