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
COPY pkg/version ./pkg/version

# Build pup binary
RUN CGO_ENABLED=0 go build -o pup ./cmd/pup

# Stage 2: Runtime image
FROM alpine:latest

# Install required tools: jq (for JSON parsing), git (for commits), ca-certificates
RUN apk --no-cache add jq git ca-certificates

WORKDIR /app

# Copy pup binary from builder
COPY --from=builder /build/pup /app/pup

# Copy claude agent scripts
COPY agents/claude-agent/designer.sh /app/designer.sh
COPY agents/claude-agent/code.sh /app/code.sh

# Copy mock responses
COPY agents/claude-agent/mocks /app/mocks

# Make scripts executable
RUN chmod +x /app/designer.sh /app/code.sh

# Create non-root user
RUN adduser -D -u 1000 agent
USER agent

# Pup is the entrypoint - it will call our scripts
ENTRYPOINT ["/app/pup"]
