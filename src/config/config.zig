const std = @import("std");
const Allocator = std.mem.Allocator;

/// Configuration for SyncMesh node
pub const Config = struct {
    allocator: Allocator,
    
    // Node identification
    node_id: []const u8,
    
    // Network configuration
    bind_address: []const u8,
    bind_port: u16,
    peers: [][]const u8,
    
    // File synchronization
    watch_directory: []const u8,
    sync_interval_ms: u32,
    max_file_size: u64,
    excluded_patterns: [][]const u8,
    
    // Security
    auth_token: ?[]const u8,
    tls_cert_path: ?[]const u8,
    tls_key_path: ?[]const u8,
    
    // Performance
    io_queue_size: u16,
    worker_threads: u8,
    buffer_size: usize,
    
    // Monitoring
    log_level: std.log.Level,
    metrics_port: ?u16,
    health_check_port: ?u16,
    
    // Docker integration
    docker_api_socket: ?[]const u8,
    container_labels: [][]const u8,
    
    const Self = @This();
    
    /// Default configuration values
    pub const defaults = Config{
        .allocator = undefined,
        .node_id = "syncmesh-node",
        .bind_address = "0.0.0.0",
        .bind_port = 8080,
        .peers = &[_][]const u8{},
        .watch_directory = "/data/sync",
        .sync_interval_ms = 1000,
        .max_file_size = 100 * 1024 * 1024, // 100MB
        .excluded_patterns = &[_][]const u8{ "*.tmp", "*.log", ".git/*" },
        .auth_token = null,
        .tls_cert_path = null,
        .tls_key_path = null,
        .io_queue_size = 256,
        .worker_threads = 1, // Single-threaded like TigerBeetle
        .buffer_size = 64 * 1024, // 64KB
        .log_level = .info,
        .metrics_port = null,
        .health_check_port = null,
        .docker_api_socket = null,
        .container_labels = &[_][]const u8{},
    };
    
    /// Parse configuration from command line arguments
    pub fn parseArgs(allocator: Allocator, args: []const []const u8) !Config {
        var config = defaults;
        config.allocator = allocator;
        
        // Clone default string values to avoid issues with string literals
        config.node_id = try allocator.dupe(u8, defaults.node_id);
        config.bind_address = try allocator.dupe(u8, defaults.bind_address);
        config.watch_directory = try allocator.dupe(u8, defaults.watch_directory);
        
        var i: usize = 0;
        while (i < args.len) : (i += 1) {
            const arg = args[i];
            
            if (std.mem.eql(u8, arg, "--config")) {
                i += 1;
                if (i >= args.len) return error.MissingConfigFile;
                try config.loadFromFile(args[i]);
            } else if (std.mem.eql(u8, arg, "--node-id")) {
                i += 1;
                if (i >= args.len) return error.MissingNodeId;
                allocator.free(config.node_id);
                config.node_id = try allocator.dupe(u8, args[i]);
            } else if (std.mem.eql(u8, arg, "--bind")) {
                i += 1;
                if (i >= args.len) return error.MissingBindAddress;
                const bind_arg = args[i];
                
                // Parse address:port
                if (std.mem.indexOf(u8, bind_arg, ":")) |colon_pos| {
                    allocator.free(config.bind_address);
                    config.bind_address = try allocator.dupe(u8, bind_arg[0..colon_pos]);
                    config.bind_port = try std.fmt.parseInt(u16, bind_arg[colon_pos + 1 ..], 10);
                } else {
                    allocator.free(config.bind_address);
                    config.bind_address = try allocator.dupe(u8, bind_arg);
                }
            } else if (std.mem.eql(u8, arg, "--peers")) {
                i += 1;
                if (i >= args.len) return error.MissingPeers;
                try config.parsePeers(args[i]);
            } else if (std.mem.eql(u8, arg, "--watch-dir")) {
                i += 1;
                if (i >= args.len) return error.MissingWatchDir;
                allocator.free(config.watch_directory);
                config.watch_directory = try allocator.dupe(u8, args[i]);
            } else if (std.mem.eql(u8, arg, "--log-level")) {
                i += 1;
                if (i >= args.len) return error.MissingLogLevel;
                config.log_level = parseLogLevel(args[i]) orelse return error.InvalidLogLevel;
            } else if (std.mem.eql(u8, arg, "--auth-token")) {
                i += 1;
                if (i >= args.len) return error.MissingAuthToken;
                config.auth_token = try allocator.dupe(u8, args[i]);
            } else if (std.mem.eql(u8, arg, "--metrics-port")) {
                i += 1;
                if (i >= args.len) return error.MissingMetricsPort;
                config.metrics_port = try std.fmt.parseInt(u16, args[i], 10);
            }
        }
        
        return config;
    }
    
    /// Load configuration from TOML file
    fn loadFromFile(self: *Config, file_path: []const u8) !void {
        _ = self;
        // TODO: Implement TOML parsing
        // For now, just verify file exists
        std.fs.cwd().access(file_path, .{}) catch |err| switch (err) {
            error.FileNotFound => {
                std.log.warn("Config file not found: {s}", .{file_path});
                return;
            },
            else => return err,
        };
        
        std.log.info("Loading configuration from: {s}", .{file_path});
        // In a real implementation, we'd parse the TOML file here
    }
    
    /// Parse comma-separated peer list
    fn parsePeers(self: *Config, peers_str: []const u8) !void {
        var peer_list = std.ArrayList([]const u8).init(self.allocator);
        defer peer_list.deinit();
        
        var iterator = std.mem.split(u8, peers_str, ",");
        while (iterator.next()) |peer| {
            const trimmed = std.mem.trim(u8, peer, " \t\n\r");
            if (trimmed.len > 0) {
                try peer_list.append(try self.allocator.dupe(u8, trimmed));
            }
        }
        
        self.peers = try peer_list.toOwnedSlice();
    }
    
    /// Parse log level from string
    fn parseLogLevel(level_str: []const u8) ?std.log.Level {
        if (std.mem.eql(u8, level_str, "debug")) return .debug;
        if (std.mem.eql(u8, level_str, "info")) return .info;
        if (std.mem.eql(u8, level_str, "warn")) return .warn;
        if (std.mem.eql(u8, level_str, "error")) return .err;
        return null;
    }
    
    /// Validate configuration
    pub fn validate(self: *const Config) !void {
        if (self.node_id.len == 0) {
            return error.EmptyNodeId;
        }
        
        if (self.bind_port == 0) {
            return error.InvalidBindPort;
        }
        
        if (self.watch_directory.len == 0) {
            return error.EmptyWatchDirectory;
        }
        
        // Verify watch directory exists
        std.fs.cwd().access(self.watch_directory, .{}) catch |err| switch (err) {
            error.FileNotFound => return error.WatchDirectoryNotFound,
            else => return err,
        };
        
        std.log.info("Configuration validated successfully");
    }
    
    /// Cleanup allocated memory
    pub fn deinit(self: *Config) void {
        self.allocator.free(self.node_id);
        self.allocator.free(self.bind_address);
        self.allocator.free(self.watch_directory);
        
        for (self.peers) |peer| {
            self.allocator.free(peer);
        }
        self.allocator.free(self.peers);
        
        if (self.auth_token) |token| {
            self.allocator.free(token);
        }
        
        if (self.tls_cert_path) |cert| {
            self.allocator.free(cert);
        }
        
        if (self.tls_key_path) |key| {
            self.allocator.free(key);
        }
        
        if (self.docker_api_socket) |socket| {
            self.allocator.free(socket);
        }
        
        for (self.container_labels) |label| {
            self.allocator.free(label);
        }
        self.allocator.free(self.container_labels);
    }
    
    /// Print configuration summary
    pub fn print(self: *const Config) void {
        std.log.info("SyncMesh Configuration:");
        std.log.info("  Node ID: {s}", .{self.node_id});
        std.log.info("  Bind: {s}:{d}", .{ self.bind_address, self.bind_port });
        std.log.info("  Watch Directory: {s}", .{self.watch_directory});
        std.log.info("  Peers: {d} configured", .{self.peers.len});
        std.log.info("  Log Level: {s}", .{@tagName(self.log_level)});
        std.log.info("  I/O Queue Size: {d}", .{self.io_queue_size});
        std.log.info("  Buffer Size: {d} bytes", .{self.buffer_size});
    }
};