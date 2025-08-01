# SyncMesh Architecture

## Overview

SyncMesh is a high-performance distributed file synchronization system built in Zig, heavily inspired by TigerBeetle's architectural patterns. This document outlines the design decisions, performance characteristics, and implementation details.

## Design Philosophy

### TigerBeetle-Inspired Principles

1. **Single-Threaded Design**: Like TigerBeetle, SyncMesh uses a single-threaded event loop to eliminate coordination overhead and maximize performance.

2. **Zero-Copy I/O**: Minimizes memory allocations and copies in hot paths, using static buffers and direct kernel operations.

3. **Cross-Platform Async I/O**: Unified abstraction over platform-specific high-performance I/O APIs:
   - Linux: io_uring for kernel-bypass I/O
   - macOS: kqueue for efficient event monitoring
   - Windows: IOCP for overlapped I/O

4. **Lock-Free Data Structures**: Uses atomic operations for inter-component communication to avoid blocking.

5. **Batching**: Groups I/O operations to amortize system call overhead.

## System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        SyncMesh Node                            │
├─────────────────┬─────────────────┬─────────────────────────────┤
│   Main Thread   │  Event Loop     │     Components              │
├─────────────────┼─────────────────┼─────────────────────────────┤
│                 │                 │  ┌─────────────────────────┐ │
│  Configuration  │   I/O Events    │  │     SyncEngine          │ │
│  Initialization │   File Events   │  │  (Coordination Logic)   │ │
│  Signal Handling│   Network Msgs  │  │  - File Registry        │ │
│                 │   Timers        │  │  - Conflict Resolution  │ │
│                 │                 │  │  - Version Control      │ │
│                 │                 │  └─────────────────────────┘ │
│                 │                 │  ┌─────────────────────────┐ │
│                 │                 │  │    NetworkLayer         │ │
│                 │                 │  │  (Peer Communication)   │ │
│                 │                 │  │  - Connection Pool      │ │
│                 │                 │  │  - Message Queues       │ │
│                 │                 │  │  - Protocol Handling    │ │
│                 │                 │  └─────────────────────────┘ │
│                 │                 │  ┌─────────────────────────┐ │
│                 │                 │  │    FileWatcher          │ │
│                 │                 │  │  (FS Monitoring)        │ │
│                 │                 │  │  - Platform-native APIs │ │
│                 │                 │  │  - Event Queuing        │ │
│                 │                 │  │  - Change Detection     │ │
│                 │                 │  └─────────────────────────┘ │
└─────────────────┴─────────────────┴─────────────────────────────┘
                            │
                            ▼
                 ┌─────────────────────┐
                 │    I/O Subsystem    │
                 │  (Cross-Platform)   │
                 ├─────────────────────┤
                 │ Linux: io_uring     │
                 │ macOS: kqueue       │
                 │ Windows: IOCP       │
                 └─────────────────────┘
```

## Core Components

### 1. I/O Abstraction Layer (`src/io/`)

The I/O layer provides a unified interface over platform-specific async I/O APIs, following TigerBeetle's pattern:

**Interface Design:**
- Common `IO` struct with platform-specific implementations
- Completion-based callback model
- Overflow queues for when submission rings are full
- Zero-copy operation submissions

**Platform Implementations:**
- **Linux** (`io_linux.zig`): Uses io_uring for maximum performance
- **macOS** (`io_darwin.zig`): Uses kqueue with userland I/O completion
- **Windows** (`io_windows.zig`): Uses IOCP for async operations

**Key Features:**
- Batched submissions to amortize syscall overhead
- User data tracking for callback correlation
- Intrusive linked lists for memory efficiency
- Configurable queue sizes and timeouts

### 2. File Watcher (`src/core/filewatcher.zig`)

Real-time file system monitoring with platform-native APIs:

**Linux (inotify):**
- Monitors directory for CREATE, MODIFY, DELETE, MOVE events
- Async event reading via I/O layer
- Efficient bulk event processing

**macOS (kqueue):**
- EVFILT_VNODE for directory monitoring
- Real-time change detection
- Polling integration with main event loop

**Windows (ReadDirectoryChangesW):**
- Overlapped I/O for async notifications
- IOCP integration for completion handling

**Features:**
- Lock-free event queuing
- Configurable exclusion patterns
- CRC32 checksums for change detection
- Minimal memory allocation in hot paths

### 3. Network Layer (`src/core/network.zig`)

High-performance peer-to-peer networking:

**Connection Management:**
- Connection pooling with configurable limits
- Automatic peer discovery and reconnection
- Heartbeat and keepalive mechanisms
- Non-blocking connect/accept operations

**Message Protocol:**
- Binary message format for efficiency
- CRC32 integrity checking
- Batched send/receive operations
- Message type routing

**Performance Features:**
- Zero-copy message handling where possible
- Lock-free message queues
- Connection state machines
- Bandwidth monitoring and throttling

### 4. Sync Engine (`src/core/sync.zig`)

Distributed synchronization logic and coordination:

**File Registry:**
- In-memory metadata tracking
- Version control and conflict detection
- Thread-safe operations with mutexes
- Efficient lookups with hash maps

**Conflict Resolution:**
- Multiple strategies: last-writer-wins, manual, version-merge
- Configurable resolution policies
- Conflict detection via checksums and timestamps
- Rollback capabilities

**Synchronization Logic:**
- Delta sync for large files
- Batch operation processing
- Retry mechanisms with exponential backoff
- Bandwidth optimization

## Performance Characteristics

### TigerBeetle-Style Optimizations

1. **Static Memory Allocation:**
   - Pre-allocated buffers to avoid GC pressure
   - Memory pools for common object types
   - Stack-based allocation where possible

2. **Cache-Friendly Data Layout:**
   - Contiguous memory layouts for hot data
   - Aligned structures for optimal access
   - Minimal pointer chasing

3. **Batching Strategy:**
   - I/O operation batching
   - Network message batching
   - File event processing in chunks

4. **Lock-Free Programming:**
   - Atomic operations for shared state
   - Compare-and-swap operations
   - Memory ordering guarantees

### Expected Performance

Based on TigerBeetle's patterns and modern hardware:

- **File I/O**: >1 GB/s sustained throughput
- **Network**: >100k messages/second
- **File Sync**: >10k files/second
- **Latency**: <5ms for typical operations
- **Memory**: <100MB baseline usage

## Scalability Design

### Horizontal Scaling

1. **Mesh Topology:**
   - Each node connects to all other nodes
   - No single point of failure
   - Automatic peer discovery

2. **Load Distribution:**
   - Files are distributed across nodes
   - Conflict resolution is distributed
   - Network load is balanced

3. **Consistency Model:**
   - Eventually consistent with conflict resolution
   - Vector clocks for ordering
   - Merkle trees for verification

### Vertical Scaling

1. **Resource Utilization:**
   - Single-threaded design maximizes CPU cache usage
   - Memory-mapped files for large datasets
   - Efficient I/O queue management

2. **Platform Optimization:**
   - Native platform APIs for maximum performance
   - Hardware acceleration where available
   - NUMA-aware memory allocation

## Security Architecture

### Authentication and Authorization

1. **Token-Based Authentication:**
   - HMAC-based tokens for node authentication
   - Token rotation and revocation
   - Per-node access control

2. **Transport Security:**
   - Optional TLS encryption for network traffic
   - Certificate-based mutual authentication
   - Perfect forward secrecy

### Data Integrity

1. **Checksums:**
   - CRC32 for fast integrity checking
   - SHA-256 for cryptographic verification
   - Merkle trees for bulk verification

2. **Access Control:**
   - File permission preservation
   - User/group mapping across nodes
   - Audit logging for all operations

## Monitoring and Observability

### Metrics

1. **Performance Metrics:**
   - I/O throughput and latency
   - Network bandwidth utilization
   - File synchronization rates
   - Error rates and types

2. **System Metrics:**
   - Memory usage and allocation patterns
   - CPU utilization
   - Disk usage and IOPS
   - Network connection counts

### Health Checks

1. **Component Health:**
   - I/O subsystem responsiveness
   - Network connectivity
   - File system accessibility
   - Peer availability

2. **System Health:**
   - Resource utilization thresholds
   - Error rate monitoring
   - Performance degradation detection
   - Automatic recovery mechanisms

## Configuration Management

### Static Configuration

1. **Node Configuration:**
   - Unique node identifiers
   - Network bind addresses
   - Peer node addresses
   - Security credentials

2. **Performance Tuning:**
   - I/O queue sizes
   - Buffer sizes
   - Timeout values
   - Batch sizes

### Dynamic Configuration

1. **Runtime Adjustments:**
   - Log levels
   - Monitoring intervals
   - Connection limits
   - Sync intervals

2. **Hot Reloading:**
   - Configuration file watching
   - Signal-based reloading
   - Graceful parameter updates
   - Validation and rollback

## Deployment Patterns

### Docker Integration

1. **Container Support:**
   - Docker API integration
   - Container label-based configuration
   - Volume mounting strategies
   - Health check integration

2. **Orchestration:**
   - Kubernetes operator patterns
   - Service discovery integration
   - Rolling update support
   - Backup and recovery

### High Availability

1. **Redundancy:**
   - Multiple node deployment
   - Geographic distribution
   - Automatic failover
   - Data replication

2. **Disaster Recovery:**
   - Backup strategies
   - Point-in-time recovery
   - Cross-region replication
   - Emergency procedures

## Future Enhancements

### Performance Improvements

1. **Advanced I/O:**
   - io_uring_spawn for parallel operations
   - Direct I/O for large files
   - Memory-mapped file handling
   - Vectored I/O operations

2. **Network Optimizations:**
   - RDMA support for high-speed networks
   - Compression algorithms
   - Delta compression
   - Multicast for bulk updates

### Feature Additions

1. **Advanced Sync:**
   - Real-time collaboration features
   - Selective synchronization
   - Bandwidth throttling
   - Priority-based sync

2. **Enterprise Features:**
   - LDAP integration
   - Role-based access control
   - Compliance logging
   - Data loss prevention

This architecture document serves as the foundation for understanding SyncMesh's design decisions and implementation strategy, heavily influenced by TigerBeetle's proven patterns for high-performance systems.