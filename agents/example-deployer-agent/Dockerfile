# Example Deployer Agent - Named Pattern Synchronizer
# Demonstrates fan-in synchronization waiting for TestResult + LintResult + SecurityScan

# Build stage - compile agent pup
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy Go module files
COPY go.mod go.sum ./
RUN go mod download

# Copy pup source code
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version

# Build pup binary
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:latest

# Install dependencies
RUN apk --no-cache add \
    ca-certificates \
    jq

WORKDIR /app

# Copy pup binary from builder
COPY --from=builder /build/pup /app/pup

# Copy deployer script
COPY agents/example-deployer-agent/deploy.sh /app/deploy.sh
RUN chmod +x /app/deploy.sh

# Run as non-root user
RUN adduser -D -u 1000 agent
USER agent

# Pup is the entrypoint (it will execute deploy.sh when granted a claim)
ENTRYPOINT ["/app/pup"]
