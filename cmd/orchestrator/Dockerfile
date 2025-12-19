# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for version info
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH

# Build orchestrator binary
# Use CGO_ENABLED=0 for static binary
# GOARCH will be set by docker buildx or defaults to the build platform
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-w -s -X github.com/hearth-insights/holt/pkg/version.Version=${VERSION} -X github.com/hearth-insights/holt/pkg/version.Commit=${COMMIT} -X github.com/hearth-insights/holt/pkg/version.Date=${DATE}" -o holt-orchestrator ./cmd/orchestrator

# Runtime stage
FROM alpine:latest

# Build arguments for labels (passed through from build stage)
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

# Add version labels for introspection
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${COMMIT}"
LABEL org.opencontainers.image.created="${DATE}"
LABEL holt.component="orchestrator"

# Install ca-certificates for HTTPS and git for M4.7 System Spine
RUN apk --no-cache add ca-certificates git

# M3.4: Create holt user with known UID for Docker socket access
# The container will be launched with GroupAdd to match host's docker GID
RUN addgroup -g 1000 holt && \
    adduser -D -u 1000 -G holt holt

# Copy binary from builder
COPY --from=builder /build/holt-orchestrator /usr/local/bin/holt-orchestrator

# M3.4: Run as holt user (non-root)
# Docker socket access granted via GroupAdd in docker run command
USER holt

# Expose health check port
EXPOSE 8080

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/holt-orchestrator"]

# Document required environment variables
ENV HOLT_INSTANCE_NAME=""
ENV REDIS_URL=""
