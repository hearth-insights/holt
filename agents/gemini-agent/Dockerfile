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

# Install required tools: jq (for JSON parsing), ca-certificates
RUN apk --no-cache add jq ca-certificates

WORKDIR /app

# Copy pup binary from builder
COPY --from=builder /build/pup /app/pup

# Copy gemini agent scripts
COPY agents/gemini-agent/designer.sh /app/designer.sh
COPY agents/gemini-agent/answer.sh /app/answer.sh
COPY agents/gemini-agent/review-code.sh /app/review-code.sh

# Copy mock responses
COPY agents/gemini-agent/mocks /app/mocks

# Make scripts executable
RUN chmod +x /app/designer.sh /app/answer.sh /app/review-code.sh

# Create non-root user
RUN adduser -D -u 1000 agent
USER agent

# Pup is the entrypoint - it will call our scripts
ENTRYPOINT ["/app/pup"]
