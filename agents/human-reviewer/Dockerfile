# Stage 1: Build pup and human-reviewer binaries
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
COPY internal/debug ./internal/debug
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version

# Build pup binary
RUN CGO_ENABLED=0 go build -o pup ./cmd/pup

# Copy source for human-reviewer
COPY agents/human-reviewer/main.go ./agents/human-reviewer/

# Build human-reviewer binary
RUN CGO_ENABLED=0 go build -o human-reviewer ./agents/human-reviewer/main.go

# Stage 2: Runtime image
FROM alpine:latest

# Install ca-certificates for any TLS connections
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /build/pup /app/pup
COPY --from=builder /build/human-reviewer /app/human-reviewer

# Make binaries executable
RUN chmod +x /app/pup /app/human-reviewer

# Create non-root user
RUN adduser -D -u 1000 agent
USER agent

# Pup is the entrypoint - it will call our human-reviewer binary
ENTRYPOINT ["/app/pup"]
