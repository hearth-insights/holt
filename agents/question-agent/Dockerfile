# Build stage - compile the pup binary
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy go.mod and go.sum
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy pup source code
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY pkg/blackboard ./pkg/blackboard
COPY internal/config ./internal/config

# Build the pup binary
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.18

# Install git, bash, and jq
RUN apk add --no-cache git bash jq

# Copy pup binary from builder
COPY --from=builder /build/pup /app/pup

# Copy agent tool script
COPY agents/question-agent/run.sh /app/run.sh
RUN chmod +x /app/run.sh

# Set working directory
WORKDIR /app

# Entrypoint is the pup binary (it will invoke run.sh when granted claims)
ENTRYPOINT ["/app/pup"]
