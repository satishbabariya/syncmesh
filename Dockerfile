# Multi-stage build for SyncMesh - High-Performance Distributed File Synchronization
# Build stage
FROM alpine:3.20 AS builder

# Install build dependencies
RUN apk add --no-cache \
    zig \
    musl-dev \
    build-base \
    git

# Create app directory
WORKDIR /app

# Copy build configuration files first for better caching
COPY build.zig build.zig.zon ./

# Copy source code
COPY src/ ./src/

# Build the application with release optimizations
RUN zig build -Drelease-fast=true

# Runtime stage - minimal Alpine image
FROM alpine:3.20 AS runtime

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    && rm -rf /var/cache/apk/*

# Create non-root user for security
RUN addgroup -g 1000 syncmesh && \
    adduser -D -s /bin/sh -u 1000 -G syncmesh syncmesh

# Create necessary directories
RUN mkdir -p /data/sync /var/log/syncmesh /etc/syncmesh && \
    chown -R syncmesh:syncmesh /data /var/log/syncmesh /etc/syncmesh

# Copy built binary from builder stage
COPY --from=builder /app/zig-out/bin/syncmesh /usr/local/bin/syncmesh

# Set proper permissions
RUN chmod +x /usr/local/bin/syncmesh

# Switch to non-root user
USER syncmesh

# Set working directory
WORKDIR /data

# Expose default port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD syncmesh version || exit 1

# Default command
ENTRYPOINT ["syncmesh"]

# Default arguments
CMD ["start", "--bind", "0.0.0.0:8080", "--watch-dir", "/data/sync"]

# Metadata
LABEL maintainer="Satish Babariya <satishbabariya@gmail.com>"
LABEL org.opencontainers.image.title="SyncMesh"
LABEL org.opencontainers.image.description="High-Performance Distributed File Synchronization System"
LABEL org.opencontainers.image.version="0.1.0"
LABEL org.opencontainers.image.source="https://github.com/satishbabariya/syncmesh"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.vendor="SyncMesh Project"