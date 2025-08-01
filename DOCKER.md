# SyncMesh Docker Guide

This guide explains how to build, push, and use the SyncMesh Docker image.

## 🐳 Quick Start

### Pull and Run from Docker Hub

```bash
# Pull the latest image
docker pull syncmesh/syncmesh:latest

# Run a basic SyncMesh node
docker run -d \
  --name syncmesh-node \
  -p 8080:8080 \
  -v /path/to/sync:/data/sync \
  syncmesh/syncmesh:latest
```

### Build from Source

```bash
# Clone the repository
git clone https://github.com/your-org/syncmesh.git
cd syncmesh

# Build the Docker image
docker build -t syncmesh:latest .

# Run the container
docker run -d \
  --name syncmesh-node \
  -p 8080:8080 \
  -v /path/to/sync:/data/sync \
  syncmesh:latest
```

## 🏗️ Building for Production

### Build with Specific Tags

```bash
# Build with version tag
docker build -t syncmesh/syncmesh:v0.1.0 .

# Build for multiple architectures
docker buildx build --platform linux/amd64,linux/arm64 \
  -t syncmesh/syncmesh:v0.1.0 \
  --push .
```

### Build Arguments

The Dockerfile supports the following build arguments:

```bash
# Build with custom Zig target
docker build \
  --build-arg ZIG_TARGET=x86_64-linux-musl \
  -t syncmesh:custom .
```

## 📦 Pushing to Registry

### Docker Hub

```bash
# Login to Docker Hub
docker login

# Tag the image
docker tag syncmesh:latest syncmesh/syncmesh:v0.1.0

# Push to Docker Hub
docker push syncmesh/syncmesh:v0.1.0
docker push syncmesh/syncmesh:latest
```

### Other Registries

```bash
# For GitHub Container Registry
docker tag syncmesh:latest ghcr.io/your-org/syncmesh:v0.1.0
docker push ghcr.io/your-org/syncmesh:v0.1.0

# For AWS ECR
aws ecr get-login-password --region us-west-2 | docker login --username AWS --password-stdin 123456789012.dkr.ecr.us-west-2.amazonaws.com
docker tag syncmesh:latest 123456789012.dkr.ecr.us-west-2.amazonaws.com/syncmesh:v0.1.0
docker push 123456789012.dkr.ecr.us-west-2.amazonaws.com/syncmesh:v0.1.0
```

## 🚀 Running SyncMesh

### Basic Usage

```bash
# Start a single node
docker run -d \
  --name syncmesh-node1 \
  -p 8080:8080 \
  -v /data/sync:/data/sync \
  syncmesh/syncmesh:latest \
  start --node-id node1 --bind 0.0.0.0:8080 --watch-dir /data/sync
```

### Multi-Node Cluster

```bash
# Node 1
docker run -d \
  --name syncmesh-node1 \
  -p 8080:8080 \
  -v /data/sync1:/data/sync \
  syncmesh/syncmesh:latest \
  start \
    --node-id node1 \
    --bind 0.0.0.0:8080 \
    --watch-dir /data/sync \
    --peers node2:8081,node3:8082

# Node 2
docker run -d \
  --name syncmesh-node2 \
  -p 8081:8080 \
  -v /data/sync2:/data/sync \
  syncmesh/syncmesh:latest \
  start \
    --node-id node2 \
    --bind 0.0.0.0:8080 \
    --watch-dir /data/sync \
    --peers node1:8080,node3:8082

# Node 3
docker run -d \
  --name syncmesh-node3 \
  -p 8082:8080 \
  -v /data/sync3:/data/sync \
  syncmesh/syncmesh:latest \
  start \
    --node-id node3 \
    --bind 0.0.0.0:8080 \
    --watch-dir /data/sync \
    --peers node1:8080,node2:8081
```

### Using Docker Compose

Create a `docker-compose.yml` file:

```yaml
version: '3.8'

services:
  syncmesh-node1:
    image: syncmesh/syncmesh:latest
    container_name: syncmesh-node1
    ports:
      - "8080:8080"
    volumes:
      - ./data/node1:/data/sync
    command: >
      start
      --node-id node1
      --bind 0.0.0.0:8080
      --watch-dir /data/sync
      --peers node2:8080,node3:8080
    networks:
      - syncmesh-network

  syncmesh-node2:
    image: syncmesh/syncmesh:latest
    container_name: syncmesh-node2
    ports:
      - "8081:8080"
    volumes:
      - ./data/node2:/data/sync
    command: >
      start
      --node-id node2
      --bind 0.0.0.0:8080
      --watch-dir /data/sync
      --peers node1:8080,node3:8080
    networks:
      - syncmesh-network

  syncmesh-node3:
    image: syncmesh/syncmesh:latest
    container_name: syncmesh-node3
    ports:
      - "8082:8080"
    volumes:
      - ./data/node3:/data/sync
    command: >
      start
      --node-id node3
      --bind 0.0.0.0:8080
      --watch-dir /data/sync
      --peers node1:8080,node2:8080
    networks:
      - syncmesh-network

networks:
  syncmesh-network:
    driver: bridge
```

Run with:

```bash
docker-compose up -d
```

## 🔧 Configuration

### Environment Variables

```bash
# Set log level
docker run -d \
  -e SYNCMESH_LOG_LEVEL=debug \
  syncmesh/syncmesh:latest

# Set configuration file
docker run -d \
  -v /path/to/config.toml:/etc/syncmesh/config.toml \
  syncmesh/syncmesh:latest \
  start --config /etc/syncmesh/config.toml
```

### Volume Mounts

```bash
# Mount configuration directory
docker run -d \
  -v /path/to/config:/etc/syncmesh \
  -v /path/to/data:/data/sync \
  -v /path/to/logs:/var/log/syncmesh \
  syncmesh/syncmesh:latest
```

## 🔍 Monitoring and Debugging

### Check Container Status

```bash
# View logs
docker logs syncmesh-node1

# Follow logs
docker logs -f syncmesh-node1

# Check health status
docker inspect syncmesh-node1 | grep Health -A 10
```

### Execute Commands

```bash
# Get version
docker exec syncmesh-node1 syncmesh version

# Check status
docker exec syncmesh-node1 syncmesh status

# Interactive shell
docker exec -it syncmesh-node1 sh
```

## 🔒 Security Considerations

### Non-Root User

The container runs as a non-root user (`syncmesh`) for security:

```bash
# Check user
docker exec syncmesh-node1 whoami
# Output: syncmesh
```

### Read-Only Root Filesystem

For enhanced security, you can run with read-only root filesystem:

```bash
docker run -d \
  --read-only \
  --tmpfs /tmp \
  --tmpfs /var/log/syncmesh \
  -v /data/sync:/data/sync:rw \
  syncmesh/syncmesh:latest
```

### Resource Limits

```bash
docker run -d \
  --memory=512m \
  --cpus=1.0 \
  --pids-limit=100 \
  syncmesh/syncmesh:latest
```

## 🐛 Troubleshooting

### Common Issues

1. **Permission Denied**: Ensure volume mounts have correct permissions
   ```bash
   chmod 755 /path/to/sync
   chown 1000:1000 /path/to/sync
   ```

2. **Port Already in Use**: Use different ports
   ```bash
   docker run -d -p 8081:8080 syncmesh/syncmesh:latest
   ```

3. **Container Won't Start**: Check logs for errors
   ```bash
   docker logs syncmesh-node1
   ```

### Debug Mode

Run with debug logging:

```bash
docker run -d \
  syncmesh/syncmesh:latest \
  start --log-level debug --bind 0.0.0.0:8080 --watch-dir /data/sync
```

## 📊 Performance Tuning

### Optimize for High Throughput

```bash
docker run -d \
  --ulimit nofile=65536:65536 \
  --shm-size=256m \
  syncmesh/syncmesh:latest
```

### Network Optimization

```bash
docker run -d \
  --network host \
  syncmesh/syncmesh:latest
```

## 🔄 Updates and Maintenance

### Update Image

```bash
# Pull latest image
docker pull syncmesh/syncmesh:latest

# Stop and remove old container
docker stop syncmesh-node1
docker rm syncmesh-node1

# Start with new image
docker run -d \
  --name syncmesh-node1 \
  -p 8080:8080 \
  -v /data/sync:/data/sync \
  syncmesh/syncmesh:latest
```

### Backup and Restore

```bash
# Backup data
docker exec syncmesh-node1 tar czf - /data/sync > backup.tar.gz

# Restore data
docker exec -i syncmesh-node1 tar xzf - < backup.tar.gz
```

## 📚 Additional Resources

- [SyncMesh Documentation](https://docs.syncmesh.org)
- [Docker Best Practices](https://docs.docker.com/develop/dev-best-practices/)
- [Container Security](https://docs.docker.com/engine/security/)