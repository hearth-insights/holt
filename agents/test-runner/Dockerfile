# Stage 1: Build pup binary
FROM golang:1.24-alpine AS builder
WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go module files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code for pup
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY pkg/blackboard ./pkg/blackboard

# Build pup binary
RUN CGO_ENABLED=0 go build -o pup ./cmd/pup

# Stage 2: Runtime image
FROM alpine:latest

# Install required tools: git (for checkout), make (for tests), jq (for JSON parsing)
RUN apk --no-cache add git make jq ca-certificates

# Configure git to trust /workspace directory (fixes "dubious ownership" errors in CI)
# This is required when the workspace is owned by a different user than the container user
RUN git config --global --add safe.directory /workspace

WORKDIR /app

# Copy pup binary from builder
COPY --from=builder /build/pup /app/pup

# Copy test runner script
COPY agents/test-runner/run-tests.sh /app/run-tests.sh
RUN chmod +x /app/run-tests.sh

# Create non-root user
RUN adduser -D -u 1000 agent
USER agent

# Pup is the entrypoint - it will call our script
ENTRYPOINT ["/app/pup"]
