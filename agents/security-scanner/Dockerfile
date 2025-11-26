# Stage 1: Build pup binary and install gosec
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

# Install gosec
RUN go install github.com/securego/gosec/v2/cmd/gosec@latest

# Stage 2: Runtime image
FROM alpine:latest

# Install required tools: git (for checkout), jq (for JSON parsing)
RUN apk --no-cache add git jq ca-certificates

WORKDIR /app

# Copy pup binary from builder
COPY --from=builder /build/pup /app/pup

# Copy gosec binary from builder
COPY --from=builder /go/bin/gosec /usr/local/bin/gosec

# Copy security scanner script
COPY agents/security-scanner/scan.sh /app/scan.sh
RUN chmod +x /app/scan.sh

# Create non-root user
RUN adduser -D -u 1000 agent
USER agent

# Pup is the entrypoint - it will call our script
ENTRYPOINT ["/app/pup"]
