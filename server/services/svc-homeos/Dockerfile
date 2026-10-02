# =============================================================================
# HomeCube · svc-homeos Dockerfile
#
# Multi-stage Go build for the homeos service.
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
    -o /build/svc-homeos \
    ./services/svc-homeos/cmd/svc-homeos

# ---- Stage 2: Runtime -------------------------------------------------------
FROM alpine:3.20

# Install ca-certificates for HTTPS and tzdata for timezone support
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /build/svc-homeos /app/svc-homeos

# Create uploads directory (PRD 14.7, §8.1)
RUN mkdir -p /app/uploads

EXPOSE 8080

ENTRYPOINT ["/app/svc-homeos"]
