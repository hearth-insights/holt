# Build stage - compile the pup binary
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/pup ./cmd/pup
COPY internal/pup ./internal/pup
COPY pkg/blackboard ./pkg/blackboard
COPY pkg/version ./pkg/version
COPY internal/config ./internal/config
COPY internal/debug ./internal/debug
RUN CGO_ENABLED=0 GOOS=linux go build -o pup ./cmd/pup

# Runtime stage
FROM alpine:3.19
RUN apk add --no-cache bash jq
COPY --from=builder /build/pup /app/pup
COPY agents/caching-agent/run.sh /app/run.sh
RUN chmod +x /app/run.sh
WORKDIR /app
ENTRYPOINT ["/app/pup"]
