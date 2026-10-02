# =============================================================================
# HomeCube · svc-finance Dockerfile
#
# Multi-stage Go build for the finance service.
# Stage 1: Build the binary with go build
# Stage 2: Minimal runtime image with just the binary
# =============================================================================

# ---- Stage 1: Build ---------------------------------------------------------
FROM golang:1.27-alpine AS builder

WORKDIR /build

# Copy go module files first for better caching
COPY server/go.mod server/go.sum ./

# Download dependencies (cached layer)
RUN go mod download

# Copy source code
COPY server/ .

# Build the binary with optimizations
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" \
    -o /build/svc-finance \
    ./services/svc-finance/cmd/svc-finance

# ---- Stage 2: Runtime -------------------------------------------------------
FROM alpine:3.20

# Install ca-certificates for HTTPS and tzdata for timezone support
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /build/svc-finance /app/svc-finance

EXPOSE 8081

ENTRYPOINT ["/app/svc-finance"]
