const std = @import("std");
const Allocator = std.mem.Allocator;
const IO = @import("../io/io.zig").IO;
const Config = @import("../config/config.zig").Config;
const FileEvent = @import("filewatcher.zig").FileWatcher.FileEvent;
const Message = @import("network.zig").NetworkLayer.Message;
const SyncOperation = @import("network.zig").NetworkLayer.SyncOperation;

/// Core synchronization engine for SyncMesh
/// Implements distributed file synchronization with conflict resolution
pub const SyncEngine = struct {
    allocator: Allocator,
    io: *IO,
    config: *const Config,

    // Sync state
    file_registry: FileRegistry,
    operation_queue: OperationQueue,
    conflict_resolver: ConflictResolver,
    running: bool,

    // Performance tracking
    sync_operations: u64,
    conflicts_resolved: u64,
    files_synchronized: u64,

    /// File metadata for tracking synchronization state
    const FileMetadata = struct {
        path: []const u8,
        size: u64,
        checksum: u32,
        modified_time: i64,
        version: u64,
        sync_state: SyncState,
        last_synced: i64,

        const SyncState = enum {
            untracked,
            synchronized,
            modified,
            conflicted,
            deleted,
        };

        pub fn deinit(self: *FileMetadata, allocator: Allocator) void {
            allocator.free(self.path);
        }
    };

    /// Registry of tracked files with their synchronization state
    const FileRegistry = struct {
        files: std.HashMap([]const u8, FileMetadata, StringContext, std.hash_map.default_max_load_percentage),
        mutex: std.Thread.Mutex,

        const StringContext = struct {
            pub fn hash(self: @This(), s: []const u8) u64 {
                _ = self;
                return std.hash_map.hashString(s);
            }

            pub fn eql(self: @This(), a: []const u8, b: []const u8) bool {
                _ = self;
                return std.mem.eql(u8, a, b);
            }
        };

        fn init(allocator: Allocator) FileRegistry {
            return FileRegistry{
                .files = std.HashMap([]const u8, FileMetadata, StringContext, std.hash_map.default_max_load_percentage).init(allocator),
                .mutex = std.Thread.Mutex{},
            };
        }

        fn deinit(self: *FileRegistry) void {
            self.mutex.lock();
            defer self.mutex.unlock();

            var iterator = self.files.iterator();
            while (iterator.next()) |entry| {
                var metadata = entry.value_ptr;
                metadata.deinit(self.files.allocator);
            }
            self.files.deinit();
        }

        fn updateFile(self: *FileRegistry, metadata: FileMetadata) !void {
            self.mutex.lock();
            defer self.mutex.unlock();

            try self.files.put(metadata.path, metadata);
        }

        fn getFile(self: *FileRegistry, path: []const u8) ?FileMetadata {
            self.mutex.lock();
            defer self.mutex.unlock();

            return self.files.get(path);
        }

        fn removeFile(self: *FileRegistry, allocator: Allocator, path: []const u8) void {
            self.mutex.lock();
            defer self.mutex.unlock();

            if (self.files.fetchRemove(path)) |entry| {
                var metadata = entry.value;
                metadata.deinit(allocator);
            }
        }

        fn getModifiedFiles(self: *FileRegistry, allocator: Allocator) ![]FileMetadata {
            self.mutex.lock();
            defer self.mutex.unlock();

            var modified_files = std.ArrayList(FileMetadata).init(allocator);

            var iterator = self.files.iterator();
            while (iterator.next()) |entry| {
                const metadata = entry.value_ptr;
                if (metadata.sync_state == .modified or metadata.sync_state == .conflicted) {
                    try modified_files.append(metadata.*);
                }
            }

            return modified_files.toOwnedSlice();
        }
    };

    /// Queue for pending sync operations
    const OperationQueue = struct {
        operations: []SyncOperation,
        head: std.atomic.Value(u32),
        tail: std.atomic.Value(u32),
        capacity: u32,

        fn init(allocator: Allocator, capacity: u32) !OperationQueue {
            const operations = try allocator.alloc(SyncOperation, capacity);
            return OperationQueue{
                .operations = operations,
                .head = std.atomic.Value(u32).init(0),
                .tail = std.atomic.Value(u32).init(0),
                .capacity = capacity,
            };
        }

        fn deinit(self: *OperationQueue, allocator: Allocator) void {
            allocator.free(self.operations);
        }

        fn push(self: *OperationQueue, operation: SyncOperation) bool {
            const tail = self.tail.load(.acquire);
            const next_tail = (tail + 1) % self.capacity;

            if (next_tail == self.head.load(.acquire)) {
                return false; // Queue full
            }

            self.operations[tail] = operation;
            self.tail.store(next_tail, .release);
            return true;
        }

        fn pop(self: *OperationQueue) ?SyncOperation {
            const head = self.head.load(.acquire);
            if (head == self.tail.load(.acquire)) {
                return null; // Queue empty
            }

            const operation = self.operations[head];
            const next_head = (head + 1) % self.capacity;
            self.head.store(next_head, .release);
            return operation;
        }
    };

    /// Conflict resolution strategies
    const ConflictResolver = struct {
        strategy: Strategy,

        const Strategy = enum {
            last_writer_wins,
            manual_resolution,
            version_merge,
            size_based,
        };

        fn init(strategy: Strategy) ConflictResolver {
            return ConflictResolver{ .strategy = strategy };
        }

        fn resolveConflict(
            self: *ConflictResolver,
            local_metadata: FileMetadata,
            remote_metadata: FileMetadata,
        ) ConflictResolution {
            return switch (self.strategy) {
                .last_writer_wins => if (local_metadata.modified_time > remote_metadata.modified_time)
                    .keep_local
                else
                    .keep_remote,

                .size_based => if (local_metadata.size > remote_metadata.size)
                    .keep_local
                else
                    .keep_remote,

                .manual_resolution => .require_manual,
                .version_merge => .attempt_merge,
            };
        }

        const ConflictResolution = enum {
            keep_local,
            keep_remote,
            require_manual,
            attempt_merge,
        };
    };

    pub fn init(allocator: Allocator, io: *IO, config: *const Config) !SyncEngine {
        std.log.info("Initializing SyncEngine", .{});

        return SyncEngine{
            .allocator = allocator,
            .io = io,
            .config = config,
            .file_registry = FileRegistry.init(allocator),
            .operation_queue = try OperationQueue.init(allocator, 1024),
            .conflict_resolver = ConflictResolver.init(.last_writer_wins),
            .running = false,
            .sync_operations = 0,
            .conflicts_resolved = 0,
            .files_synchronized = 0,
        };
    }

    pub fn deinit(self: *SyncEngine) void {
        std.log.info("Shutting down SyncEngine", .{});

        self.running = false;
        self.file_registry.deinit();
        self.operation_queue.deinit(self.allocator);
    }

    pub fn start(self: *SyncEngine) !void {
        std.log.info("Starting SyncEngine", .{});

        self.running = true;

        // Perform initial directory scan
        try self.performInitialScan();
    }

    fn performInitialScan(self: *SyncEngine) !void {
        std.log.info("Performing initial directory scan: {s}", .{self.config.watch_directory});

        var dir = try std.fs.cwd().openDir(self.config.watch_directory, .{ .iterate = true });
        defer dir.close();

        var iterator = dir.iterate();
        while (try iterator.next()) |entry| {
            if (entry.kind != .file) continue;

            // Create file metadata
            const file_path = try self.allocator.dupe(u8, entry.name);
            const stat = try dir.statFile(entry.name);
            const checksum = try self.calculateFileChecksum(dir, entry.name);

            const metadata = FileMetadata{
                .path = file_path,
                .size = stat.size,
                .checksum = checksum,
                .modified_time = @intCast(stat.mtime),
                .version = 1,
                .sync_state = .synchronized,
                .last_synced = std.time.timestamp(),
            };

            try self.file_registry.updateFile(metadata);
            self.files_synchronized += 1;
        }

        std.log.info("Initial scan complete: {d} files tracked", .{self.files_synchronized});
    }

    fn calculateFileChecksum(self: *SyncEngine, dir: std.fs.Dir, filename: []const u8) !u32 {
        _ = self;

        const file = try dir.openFile(filename, .{});
        defer file.close();

        // Use a streaming approach for large files
        var hasher = std.hash.crc.Crc32.init();
        var buffer: [8192]u8 = undefined;

        while (true) {
            const bytes_read = try file.readAll(&buffer);
            if (bytes_read == 0) break;

            hasher.update(buffer[0..bytes_read]);

            if (bytes_read < buffer.len) break;
        }

        return hasher.final();
    }

    /// Handle file system events from FileWatcher
    pub fn handleFileEvent(self: *SyncEngine, event: FileEvent) !void {
        std.log.debug("Processing file event: {s} ({s})", .{ event.path, @tagName(event.event_type) });

        switch (event.event_type) {
            .created, .modified => try self.handleFileChange(event),
            .deleted => try self.handleFileDelete(event),
            .moved => try self.handleFileMove(event),
        }

        self.sync_operations += 1;
    }

    fn handleFileChange(self: *SyncEngine, event: FileEvent) !void {
        // Check if file is already tracked
        if (self.file_registry.getFile(event.path)) |existing| {
            // File exists, check for changes
            if (existing.checksum != event.checksum) {
                // File has been modified
                var updated_metadata = existing;
                updated_metadata.checksum = event.checksum;
                updated_metadata.size = event.file_size;
                updated_metadata.modified_time = event.timestamp;
                updated_metadata.version += 1;
                updated_metadata.sync_state = .modified;

                try self.file_registry.updateFile(updated_metadata);

                // Queue sync operation
                try self.queueSyncOperation(updated_metadata, .update_file);
            }
        } else {
            // New file
            const file_path = try self.allocator.dupe(u8, event.path);
            const metadata = FileMetadata{
                .path = file_path,
                .size = event.file_size,
                .checksum = event.checksum,
                .modified_time = event.timestamp,
                .version = 1,
                .sync_state = .modified,
                .last_synced = 0,
            };

            try self.file_registry.updateFile(metadata);
            try self.queueSyncOperation(metadata, .create_file);
        }
    }

    fn handleFileDelete(self: *SyncEngine, event: FileEvent) !void {
        if (self.file_registry.getFile(event.path)) |metadata| {
            // Mark as deleted and queue sync operation
            var deleted_metadata = metadata;
            deleted_metadata.sync_state = .deleted;
            deleted_metadata.modified_time = event.timestamp;

            try self.file_registry.updateFile(deleted_metadata);
            try self.queueSyncOperation(deleted_metadata, .delete_file);
        }
    }

    fn handleFileMove(self: *SyncEngine, event: FileEvent) !void {
        // For simplicity, treat as delete + create
        try self.handleFileDelete(event);
        try self.handleFileChange(event);
    }

    fn queueSyncOperation(self: *SyncEngine, metadata: FileMetadata, operation_type: SyncOperation.OperationType) !void {
        // Read file data if needed
        var file_data: []const u8 = "";
        if (operation_type == .create_file or operation_type == .update_file) {
            file_data = try self.readFileData(metadata.path);
        }

        // Create sync operation for each peer
        for (self.config.peers) |peer| {
            const operation = SyncOperation{
                .target_node = try self.allocator.dupe(u8, peer),
                .operation_type = operation_type,
                .file_path = try self.allocator.dupe(u8, metadata.path),
                .data = try self.allocator.dupe(u8, file_data),
                .checksum = metadata.checksum,
                .timestamp = metadata.modified_time,
            };

            if (!self.operation_queue.push(operation)) {
                std.log.warn("Sync operation queue full, dropping operation for: {s}", .{metadata.path});
                @as(*SyncOperation, @ptrFromInt(@intFromPtr(&operation))).deinit(self.allocator);
            }
        }

        if (file_data.len > 0) {
            self.allocator.free(file_data);
        }
    }

    fn readFileData(self: *SyncEngine, file_path: []const u8) ![]u8 {
        const full_path = try std.fs.path.join(self.allocator, &[_][]const u8{ self.config.watch_directory, file_path });
        defer self.allocator.free(full_path);

        const file = try std.fs.cwd().openFile(full_path, .{});
        defer file.close();

        const file_size = try file.getEndPos();
        if (file_size > self.config.max_file_size) {
            return error.FileTooLarge;
        }

        const data = try self.allocator.alloc(u8, file_size);
        _ = try file.readAll(data);

        return data;
    }

    /// Handle incoming network messages
    pub fn handleNetworkMessage(self: *SyncEngine, message: Message) !void {
        std.log.debug("Processing network message from: {s}", .{message.source_node});

        switch (message.message_type) {
            .sync_request => try self.handleSyncRequest(message),
            .sync_response => try self.handleSyncResponse(message),
            .file_transfer => try self.handleFileTransfer(message),
            .heartbeat => try self.handleHeartbeat(message),
            .discovery => try self.handleDiscovery(message),
            .authentication => try self.handleAuthentication(message),
        }
    }

    fn handleSyncRequest(self: *SyncEngine, message: Message) !void {
        _ = self;
        _ = message;
        // TODO: Implement sync request handling
        std.log.debug("Handling sync request", .{});
    }

    fn handleSyncResponse(self: *SyncEngine, message: Message) !void {
        _ = self;
        _ = message;
        // TODO: Implement sync response handling
        std.log.debug("Handling sync response", .{});
    }

    fn handleFileTransfer(self: *SyncEngine, message: Message) !void {
        _ = self;
        _ = message;
        // TODO: Implement file transfer handling
        std.log.debug("Handling file transfer", .{});
    }

    fn handleHeartbeat(self: *SyncEngine, message: Message) !void {
        _ = self;
        _ = message;
        std.log.debug("Received heartbeat", .{});
    }

    fn handleDiscovery(self: *SyncEngine, message: Message) !void {
        _ = self;
        _ = message;
        std.log.debug("Handling discovery message", .{});
    }

    fn handleAuthentication(self: *SyncEngine, message: Message) !void {
        _ = self;
        _ = message;
        std.log.debug("Handling authentication", .{});
    }

    pub fn tick(self: *SyncEngine) !void {
        if (!self.running) return;

        // Process periodic synchronization
        try self.performPeriodicSync();

        // Clean up completed operations
        self.cleanupOperations();
    }

    fn performPeriodicSync(self: *SyncEngine) !void {
        // Check if it's time for periodic sync
        const now = std.time.timestamp();
        const sync_interval = self.config.sync_interval_ms * std.time.ns_per_ms;

        // TODO: Track last sync time and only sync if interval has passed
        _ = now;
        _ = sync_interval;
    }

    fn cleanupOperations(_: *SyncEngine) void {
        // TODO: Clean up completed sync operations
    }

    /// Poll for pending sync operations
    pub fn pollSyncOperation(self: *SyncEngine) ?SyncOperation {
        return self.operation_queue.pop();
    }

    /// Health check
    pub fn isHealthy(self: *const SyncEngine) bool {
        return self.running;
    }

    /// Get synchronization statistics
    pub fn getStats(self: *const SyncEngine) struct { ops: u64, conflicts: u64, files: u64 } {
        return .{
            .ops = self.sync_operations,
            .conflicts = self.conflicts_resolved,
            .files = self.files_synchronized,
        };
    }
};
