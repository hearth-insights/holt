# Build stage - compile agent pup
FROM golang:1.24-alpine AS builder

WORKDIR /build
# Copy go.mod/sum from root (context is root of repo)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code needed for pup
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY internal/config ./internal/config
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version

RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:latest

RUN apk add --no-cache bash jq curl

WORKDIR /app

# Copy pup binary
COPY --from=builder /build/pup /app/pup

# Copy agent scripts (context is root of repo)
COPY demos/production-gatekeeper/test_runner.sh /app/
COPY demos/production-gatekeeper/gatekeeper.sh /app/
COPY demos/production-gatekeeper/deployer.sh /app/
COPY demos/production-gatekeeper/run.sh /app/
RUN chmod +x /app/*.sh

ENTRYPOINT ["/app/pup"]
