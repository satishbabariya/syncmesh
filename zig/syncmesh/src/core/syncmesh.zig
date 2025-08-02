const std = @import("std");
const Allocator = std.mem.Allocator;
const IO = @import("../io/io.zig").IO;
const Config = @import("../config/config.zig").Config;
const Logger = @import("../utils/logger.zig").Logger;
const FileWatcher = @import("filewatcher.zig").FileWatcher;
const NetworkLayer = @import("network.zig").NetworkLayer;
const SyncEngine = @import("sync.zig").SyncEngine;

/// Main SyncMesh coordinator
/// Single-threaded design inspired by TigerBeetle for maximum performance
pub const SyncMesh = struct {
    allocator: Allocator,
    io: *IO,
    config: *const Config,

    // Core components
    file_watcher: FileWatcher,
    network: NetworkLayer,
    sync_engine: SyncEngine,

    // State
    running: bool,
    start_time: i64,
    stats: Stats,

    /// Runtime statistics
    const Stats = struct {
        files_synced: u64 = 0,
        bytes_transferred: u64 = 0,
        sync_operations: u64 = 0,
        network_connections: u32 = 0,
        errors: u64 = 0,
        uptime_seconds: u64 = 0,

        pub fn print(self: *const Stats) void {
            std.log.info("=== SyncMesh Statistics ===", .{});
            std.log.info("Files Synced: {d}", .{self.files_synced});
            std.log.info("Bytes Transferred: {d}", .{self.bytes_transferred});
            std.log.info("Sync Operations: {d}", .{self.sync_operations});
            std.log.info("Active Connections: {d}", .{self.network_connections});
            std.log.info("Errors: {d}", .{self.errors});
            std.log.info("Uptime: {d}s", .{self.uptime_seconds});
        }
    };

    /// Initialize SyncMesh with all subsystems
    pub fn init(allocator: Allocator, io: *IO, config: *const Config) !SyncMesh {
        try config.validate();

        std.log.info("Initializing SyncMesh subsystems...", .{});

        // Initialize file watcher
        const file_watcher = try FileWatcher.init(allocator, io, config);

        // Initialize network layer
        const network = try NetworkLayer.init(allocator, io, config);

        // Initialize sync engine
        const sync_engine = try SyncEngine.init(allocator, io, config);

        const now = std.time.timestamp();

        return SyncMesh{
            .allocator = allocator,
            .io = io,
            .config = config,
            .file_watcher = file_watcher,
            .network = network,
            .sync_engine = sync_engine,
            .running = false,
            .start_time = now,
            .stats = Stats{},
        };
    }

    /// Cleanup and shutdown
    pub fn deinit(self: *SyncMesh) void {
        std.log.info("Shutting down SyncMesh...", .{});

        self.running = false;

        self.sync_engine.deinit();
        self.network.deinit();
        self.file_watcher.deinit();

        self.stats.print();
        std.log.info("SyncMesh shutdown complete", .{});
    }

    /// Main event loop - TigerBeetle style single-threaded processing
    pub fn run(self: *SyncMesh) !void {
        std.log.info("🚀 Starting SyncMesh main event loop", .{});

        // Print configuration
        self.config.print();

        self.running = true;

        // Start subsystems
        try self.file_watcher.start();
        try self.network.start();
        try self.sync_engine.start();

        var last_stats_time = std.time.timestamp();
        var iteration_count: u64 = 0;

        // Main event loop
        while (self.running) {
            iteration_count += 1;

            // Update uptime
            const now = std.time.timestamp();
            self.stats.uptime_seconds = @intCast(now - self.start_time);

            // Run I/O for a short duration (1ms)
            try self.io.runForNs(1_000_000); // 1 millisecond

            // Process each subsystem
            try self.file_watcher.tick();
            try self.network.tick();
            try self.sync_engine.tick();

            // Handle inter-component communication
            try self.processEvents();

            // Print statistics every 10 seconds
            if (now - last_stats_time >= 10) {
                self.printRuntimeStats(iteration_count);
                last_stats_time = now;
                iteration_count = 0;
            }

            // Brief sleep to prevent 100% CPU usage
            std.time.sleep(100_000); // 100 microseconds
        }

        std.log.info("Main event loop terminated", .{});
    }

    /// Process inter-component events and messages
    fn processEvents(self: *SyncMesh) !void {
        // File changes -> Sync Engine
        while (self.file_watcher.pollFileEvent()) |event| {
            try self.sync_engine.handleFileEvent(event);
            self.stats.files_synced += 1;
        }

        // Network messages -> Sync Engine
        while (self.network.pollMessage()) |message| {
            try self.sync_engine.handleNetworkMessage(message);
            self.stats.sync_operations += 1;
        }

        // Sync operations -> Network
        while (self.sync_engine.pollSyncOperation()) |operation| {
            try self.network.sendSyncOperation(operation);
            self.stats.bytes_transferred += operation.data.len;
        }

        // Update connection count
        self.stats.network_connections = self.network.getConnectionCount();
    }

    /// Print runtime performance statistics
    fn printRuntimeStats(self: *SyncMesh, iterations: u64) void {
        const rate = @as(f64, @floatFromInt(iterations)) / 10.0; // iterations per second

        Logger.logMetric("event_loop_rate", rate, "iter/s");
        Logger.logMetric("files_synced", @floatFromInt(self.stats.files_synced), "");
        Logger.logMetric("bytes_transferred", @floatFromInt(self.stats.bytes_transferred), "bytes");
        Logger.logMetric("active_connections", @floatFromInt(self.stats.network_connections), "");

        // Memory usage
        // TODO: Implement proper memory leak detection
    }

    /// Graceful shutdown signal handler
    pub fn shutdown(self: *SyncMesh) void {
        std.log.info("Received shutdown signal", .{});
        self.running = false;
    }

    /// Health check for monitoring
    pub fn healthCheck(self: *const SyncMesh) bool {
        return self.running and
            self.file_watcher.isHealthy() and
            self.network.isHealthy() and
            self.sync_engine.isHealthy();
    }

    /// Get current metrics for monitoring endpoints
    pub fn getMetrics(self: *const SyncMesh) Stats {
        return self.stats;
    }
};
