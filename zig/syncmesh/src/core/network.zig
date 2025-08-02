const std = @import("std");
const Allocator = std.mem.Allocator;
const IO = @import("../io/io.zig").IO;
const Config = @import("../config/config.zig").Config;

/// High-performance networking layer for SyncMesh
/// Handles peer-to-peer communication and file transfer protocols
pub const NetworkLayer = struct {
    allocator: Allocator,
    io: *IO,
    config: *const Config,

    // Network state
    server_socket: std.posix.socket_t,
    connections: ConnectionPool,
    message_queue: MessageQueue,
    running: bool,

    // Statistics
    total_connections: u32,
    active_connections: u32,
    bytes_sent: u64,
    bytes_received: u64,

    /// Network message types
    pub const Message = struct {
        source_node: []const u8,
        message_type: MessageType,
        payload: []const u8,
        timestamp: i64,

        pub const MessageType = enum {
            sync_request,
            sync_response,
            file_transfer,
            heartbeat,
            discovery,
            authentication,
        };

        pub fn deinit(self: *Message, allocator: Allocator) void {
            allocator.free(self.source_node);
            allocator.free(self.payload);
        }
    };

    /// Sync operation for inter-node communication
    pub const SyncOperation = struct {
        target_node: []const u8,
        operation_type: OperationType,
        file_path: []const u8,
        data: []const u8,
        checksum: u32,
        timestamp: i64,

        pub const OperationType = enum {
            create_file,
            update_file,
            delete_file,
            sync_directory,
        };

        pub fn deinit(self: *SyncOperation, allocator: Allocator) void {
            allocator.free(self.target_node);
            allocator.free(self.file_path);
            allocator.free(self.data);
        }
    };

    /// Connection management
    const Connection = struct {
        socket: std.posix.socket_t,
        address: std.net.Address,
        node_id: []const u8,
        connected_at: i64,
        last_activity: i64,
        send_buffer: []u8,
        recv_buffer: []u8,
        send_offset: usize,
        recv_offset: usize,
        state: State,

        const State = enum {
            connecting,
            authenticating,
            active,
            disconnecting,
        };

        fn init(allocator: Allocator, socket: std.posix.socket_t, address: std.net.Address) !Connection {
            const send_buffer = try allocator.alloc(u8, 64 * 1024); // 64KB
            const recv_buffer = try allocator.alloc(u8, 64 * 1024); // 64KB

            return Connection{
                .socket = socket,
                .address = address,
                .node_id = try allocator.dupe(u8, "unknown"),
                .connected_at = std.time.timestamp(),
                .last_activity = std.time.timestamp(),
                .send_buffer = send_buffer,
                .recv_buffer = recv_buffer,
                .send_offset = 0,
                .recv_offset = 0,
                .state = .connecting,
            };
        }

        fn deinit(self: *Connection, allocator: Allocator) void {
            allocator.free(self.node_id);
            allocator.free(self.send_buffer);
            allocator.free(self.recv_buffer);
            std.posix.close(self.socket);
        }
    };

    const ConnectionPool = struct {
        connections: std.ArrayList(Connection),
        max_connections: u32,

        fn init(allocator: Allocator, max_connections: u32) ConnectionPool {
            return ConnectionPool{
                .connections = std.ArrayList(Connection).init(allocator),
                .max_connections = max_connections,
            };
        }

        fn deinit(self: *ConnectionPool, allocator: Allocator) void {
            for (self.connections.items) |*conn| {
                conn.deinit(allocator);
            }
            self.connections.deinit();
        }

        fn addConnection(self: *ConnectionPool, connection: Connection) !void {
            if (self.connections.items.len >= self.max_connections) {
                return error.TooManyConnections;
            }
            try self.connections.append(connection);
        }

        fn removeConnection(self: *ConnectionPool, allocator: Allocator, socket: std.posix.socket_t) void {
            for (self.connections.items, 0..) |*conn, i| {
                if (conn.socket == socket) {
                    conn.deinit(allocator);
                    _ = self.connections.swapRemove(i);
                    break;
                }
            }
        }

        fn getConnection(self: *ConnectionPool, socket: std.posix.socket_t) ?*Connection {
            for (self.connections.items) |*conn| {
                if (conn.socket == socket) return conn;
            }
            return null;
        }

        fn getActiveCount(self: *const ConnectionPool) u32 {
            var count: u32 = 0;
            for (self.connections.items) |*conn| {
                if (conn.state == .active) count += 1;
            }
            return count;
        }
    };

    /// Lock-free message queue
    const MessageQueue = struct {
        messages: []Message,
        head: std.atomic.Value(u32),
        tail: std.atomic.Value(u32),
        capacity: u32,

        fn init(allocator: Allocator, capacity: u32) !MessageQueue {
            const messages = try allocator.alloc(Message, capacity);
            return MessageQueue{
                .messages = messages,
                .head = std.atomic.Value(u32).init(0),
                .tail = std.atomic.Value(u32).init(0),
                .capacity = capacity,
            };
        }

        fn deinit(self: *MessageQueue, allocator: Allocator) void {
            allocator.free(self.messages);
        }

        fn push(self: *MessageQueue, message: Message) bool {
            const tail = self.tail.load(.acquire);
            const next_tail = (tail + 1) % self.capacity;

            if (next_tail == self.head.load(.acquire)) {
                return false; // Queue full
            }

            self.messages[tail] = message;
            self.tail.store(next_tail, .release);
            return true;
        }

        fn pop(self: *MessageQueue) ?Message {
            const head = self.head.load(.acquire);
            if (head == self.tail.load(.acquire)) {
                return null; // Queue empty
            }

            const message = self.messages[head];
            const next_head = (head + 1) % self.capacity;
            self.head.store(next_head, .release);
            return message;
        }
    };

    pub fn init(allocator: Allocator, io: *IO, config: *const Config) !NetworkLayer {
        std.log.info("Initializing NetworkLayer on {s}:{d}", .{ config.bind_address, config.bind_port });

        // Create server socket
        const server_socket = try std.posix.socket(std.posix.AF.INET, std.posix.SOCK.STREAM | std.posix.SOCK.CLOEXEC, std.posix.IPPROTO.TCP);

        // Set socket options
        try std.posix.setsockopt(server_socket, std.posix.SOL.SOCKET, std.posix.SO.REUSEADDR, &std.mem.toBytes(@as(c_int, 1)));

        // Bind socket
        const address = try std.net.Address.parseIp(config.bind_address, config.bind_port);
        try std.posix.bind(server_socket, &address.any, address.getOsSockLen());

        // Listen
        try std.posix.listen(server_socket, 128);

        // Initialize connection pool
        const connections = ConnectionPool.init(allocator, 100); // Max 100 connections

        // Initialize message queue
        const message_queue = try MessageQueue.init(allocator, 1024);

        return NetworkLayer{
            .allocator = allocator,
            .io = io,
            .config = config,
            .server_socket = server_socket,
            .connections = connections,
            .message_queue = message_queue,
            .running = false,
            .total_connections = 0,
            .active_connections = 0,
            .bytes_sent = 0,
            .bytes_received = 0,
        };
    }

    pub fn deinit(self: *NetworkLayer) void {
        std.log.info("Shutting down NetworkLayer", .{});

        self.running = false;
        std.posix.close(self.server_socket);
        self.connections.deinit(self.allocator);
        self.message_queue.deinit(self.allocator);
    }

    pub fn start(self: *NetworkLayer) !void {
        std.log.info("Starting NetworkLayer server", .{});

        self.running = true;

        // Start accepting connections
        try self.startAccepting();

        // Connect to configured peers
        try self.connectToPeers();
    }

    fn startAccepting(self: *NetworkLayer) !void {
        var completion: IO.Completion = undefined;

        self.io.accept(*NetworkLayer, self, acceptCallback, &completion, self.server_socket);
    }

    fn acceptCallback(
        self: *NetworkLayer,
        completion: *IO.Completion,
        result: anyerror!usize,
    ) void {
        _ = completion;

        const client_socket = result catch |err| {
            std.log.err("Accept failed: {}", .{err});
            // Continue accepting
            if (self.running) {
                self.startAccepting() catch {};
            }
            return;
        };

        // Create new connection
        const address = std.net.Address.initIp4(.{ 127, 0, 0, 1 }, 0); // Placeholder
        const connection = Connection.init(self.allocator, @intCast(client_socket), address) catch |err| {
            std.log.err("Failed to create connection: {}", .{err});
            std.posix.close(@intCast(client_socket));
            return;
        };

        // Add to connection pool
        self.connections.addConnection(connection) catch |err| {
            std.log.warn("Connection pool full: {}", .{err});
            return;
        };

        self.total_connections += 1;
        self.active_connections = self.connections.getActiveCount();

        std.log.info("New connection accepted (total: {d})", .{self.total_connections});

        // Start receiving from this connection
        self.startReceiving(@intCast(client_socket)) catch {};

        // Continue accepting
        if (self.running) {
            self.startAccepting() catch {};
        }
    }

    fn startReceiving(self: *NetworkLayer, socket: std.posix.socket_t) !void {
        if (self.connections.getConnection(socket)) |conn| {
            var completion: IO.Completion = undefined;

            self.io.read(*NetworkLayer, self, receiveCallback, &completion, socket, conn.recv_buffer[conn.recv_offset..], 0);
        }
    }

    fn receiveCallback(
        self: *NetworkLayer,
        completion: *IO.Completion,
        result: anyerror!usize,
    ) void {
        _ = completion;

        const bytes_read = result catch |err| {
            std.log.err("Receive failed: {}", .{err});
            return;
        };

        if (bytes_read == 0) {
            // Connection closed
            return;
        }

        self.bytes_received += bytes_read;

        // TODO: Parse incoming messages and queue them
        std.log.debug("Received {d} bytes", .{bytes_read});
    }

    fn connectToPeers(self: *NetworkLayer) !void {
        for (self.config.peers) |peer_address| {
            self.connectToPeer(peer_address) catch |err| {
                std.log.warn("Failed to connect to peer {s}: {}", .{ peer_address, err });
            };
        }
    }

    fn connectToPeer(self: *NetworkLayer, peer_address: []const u8) !void {
        std.log.info("Connecting to peer: {s}", .{peer_address});

        // Parse address
        const colon_pos = std.mem.indexOf(u8, peer_address, ":") orelse return error.InvalidPeerAddress;
        const host = peer_address[0..colon_pos];
        const port = try std.fmt.parseInt(u16, peer_address[colon_pos + 1 ..], 10);

        // Create socket
        const socket = try std.posix.socket(std.posix.AF.INET, std.posix.SOCK.STREAM | std.posix.SOCK.CLOEXEC, std.posix.IPPROTO.TCP);

        // Connect
        const address = try std.net.Address.parseIp(host, port);

        var completion: IO.Completion = undefined;
        self.io.connect(*NetworkLayer, self, connectCallback, &completion, socket, &address.any, address.getOsSockLen());
    }

    fn connectCallback(
        _: *NetworkLayer,
        _: *IO.Completion,
        result: anyerror!usize,
    ) void {
        _ = result catch |err| {
            std.log.err("Connect failed: {}", .{err});
            return;
        };

        std.log.info("Successfully connected to peer", .{});
    }

    pub fn tick(self: *NetworkLayer) !void {
        if (!self.running) return;

        // Update connection states
        self.active_connections = self.connections.getActiveCount();

        // Handle timeouts and cleanup
        self.cleanupConnections();
    }

    fn cleanupConnections(self: *NetworkLayer) void {
        const now = std.time.timestamp();
        const timeout_seconds = 300; // 5 minutes

        var i: usize = 0;
        while (i < self.connections.connections.items.len) {
            const conn = &self.connections.connections.items[i];

            if (now - conn.last_activity > timeout_seconds) {
                std.log.info("Closing idle connection", .{});
                self.connections.removeConnection(self.allocator, conn.socket);
                // Don't increment i since we removed an item
            } else {
                i += 1;
            }
        }
    }

    /// Send sync operation to target node
    pub fn sendSyncOperation(_: *NetworkLayer, operation: SyncOperation) !void {
        // TODO: Serialize operation and send to target node
        std.log.debug("Sending sync operation: {s} -> {s}", .{ operation.file_path, operation.target_node });
    }

    /// Poll for incoming messages
    pub fn pollMessage(self: *NetworkLayer) ?Message {
        return self.message_queue.pop();
    }

    /// Get active connection count
    pub fn getConnectionCount(self: *const NetworkLayer) u32 {
        return self.active_connections;
    }

    /// Health check
    pub fn isHealthy(self: *const NetworkLayer) bool {
        return self.running;
    }
};
