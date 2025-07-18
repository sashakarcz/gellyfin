# Stage 1: Build Go binary
FROM golang:1.21-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-w -s' -o gellyfin .

# Stage 2: Download Nomad binary
FROM alpine:latest AS nomad
ARG NOMAD_VERSION=1.8.1

RUN apk add --no-cache curl unzip && \
    curl -fsSL -o /tmp/nomad.zip "https://releases.hashicorp.com/nomad/${NOMAD_VERSION}/nomad_${NOMAD_VERSION}_linux_amd64.zip" && \
    unzip /tmp/nomad.zip -d /usr/local/bin && \
    chmod +x /usr/local/bin/nomad

# Stage 3: Final image (Debian for glibc compatibility)
FROM debian:bullseye-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/gellyfin /app/gellyfin
COPY --from=builder /app/static /app/static
COPY --from=nomad /usr/local/bin/nomad /usr/local/bin/nomad

RUN chmod +x /app/gellyfin /usr/local/bin/nomad

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:${PORT:-8888}/health || exit 1

EXPOSE 8888

# Create non-root user
RUN useradd -u 1001 -r -s /bin/false gellyfin && \
    chown -R gellyfin:gellyfin /app

USER gellyfin

CMD ["/app/gellyfin"]

