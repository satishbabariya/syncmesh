const std = @import("std");
const print = std.debug.print;
const Allocator = std.mem.Allocator;

const IO = @import("io/io.zig").IO;
const SyncMesh = @import("core/syncmesh.zig").SyncMesh;
const Config = @import("config/config.zig").Config;
const Logger = @import("utils/logger.zig").Logger;

pub const std_options: std.Options = .{
    .log_level = std.log.Level.info,
    .logFn = Logger.log,
};

const SYNCMESH_VERSION = "0.1.0";

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    // Parse command line arguments
    const args = try std.process.argsAlloc(allocator);
    defer std.process.argsFree(allocator, args);

    if (args.len < 2) {
        try printUsage();
        return;
    }

    const command = args[1];

    if (std.mem.eql(u8, command, "version")) {
        print("SyncMesh v{s}\n", .{SYNCMESH_VERSION});
        return;
    } else if (std.mem.eql(u8, command, "start")) {
        try startSyncMesh(allocator, args[2..]);
    } else if (std.mem.eql(u8, command, "help")) {
        try printUsage();
    } else {
        print("Unknown command: {s}\n", .{command});
        try printUsage();
        std.process.exit(1);
    }
}

fn printUsage() !void {
    print(
        \\SyncMesh v{s} - High-Performance Distributed File Synchronization
        \\
        \\USAGE:
        \\    syncmesh <COMMAND> [OPTIONS]
        \\
        \\COMMANDS:
        \\    start       Start the SyncMesh daemon
        \\    version     Print version information
        \\    help        Show this help message
        \\
        \\START OPTIONS:
        \\    --config <file>         Configuration file path (default: syncmesh.toml)
        \\    --node-id <id>          Unique node identifier
        \\    --bind <address>        Bind address (default: 0.0.0.0:8080)
        \\    --peers <list>          Comma-separated list of peer addresses
        \\    --watch-dir <path>      Directory to watch and synchronize
        \\    --log-level <level>     Log level: debug, info, warn, error (default: info)
        \\
        \\EXAMPLES:
        \\    syncmesh start --node-id node1 --bind 0.0.0.0:8080 --watch-dir /data/sync
        \\    syncmesh start --config ./mesh-config.toml
        \\
    , .{SYNCMESH_VERSION});
}

fn startSyncMesh(allocator: Allocator, args: []const []const u8) !void {
    print("🚀 Starting SyncMesh v{s}...\n", .{SYNCMESH_VERSION});

    // Parse configuration
    var config = try Config.parseArgs(allocator, args);
    defer config.deinit();

    // Initialize logger
    try Logger.init(allocator, config.log_level);
    defer Logger.deinit();

    // Initialize high-performance I/O subsystem (TigerBeetle-style)
    var io = try IO.init(allocator, .{
        .entries = 256, // Size of submission queue
        .flags = 0,
    });
    defer io.deinit();

    // Initialize SyncMesh core
    var syncmesh = try SyncMesh.init(allocator, &io, &config);
    defer syncmesh.deinit();

    print("✅ SyncMesh initialized successfully\n", .{});
    print("📡 Node ID: {s}\n", .{config.node_id});
    print("🔗 Bind Address: {s}\n", .{config.bind_address});
    print("📁 Watch Directory: {s}\n", .{config.watch_directory});

    // Start the main event loop
    try syncmesh.run();
}
