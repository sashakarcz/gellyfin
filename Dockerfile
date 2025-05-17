# Stage 1: Build Nomad in a compatible environment
FROM hashicorp/nomad:1.1.6 AS nomad-builder

# Stage 2: Build the Go binary
FROM golang:1.17-alpine AS builder

WORKDIR /app

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o gellyfin .

# Stage 3: Create the minimal Debian image
FROM debian:bullseye-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=nomad-builder /bin/nomad /usr/local/bin/nomad
COPY --from=builder /app/gellyfin /app/gellyfin
COPY --from=builder /app/static /app/static

RUN chmod +x /app/gellyfin

EXPOSE 8888

CMD ["/app/gellyfin"]