const std = @import("std");
const testing = std.testing;

// Import modules to test
const IO = @import("io/io.zig").IO;
const Config = @import("config/config.zig").Config;
const Logger = @import("utils/logger.zig").Logger;

test "Config parsing" {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    // Test basic config parsing
    const args = [_][]const u8{
        "--node-id", "test-node",
        "--bind", "127.0.0.1:9090",
        "--watch-dir", "/tmp/test-sync",
    };

    var config = Config.parseArgs(allocator, &args) catch |err| {
        std.log.err("Config parsing failed: {}", .{err});
        return err;
    };
    defer config.deinit();

    try testing.expectEqualStrings("test-node", config.node_id);
    try testing.expectEqualStrings("127.0.0.1", config.bind_address);
    try testing.expectEqual(@as(u16, 9090), config.bind_port);
    try testing.expectEqualStrings("/tmp/test-sync", config.watch_directory);
}

test "IO initialization" {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    var io = IO.init(allocator, .{
        .entries = 32,
        .flags = 0,
    }) catch |err| {
        // Some platforms might not support io_uring/kqueue in test environment
        if (err == error.SystemOutdated or err == error.PermissionDenied) {
            return;
        }
        return err;
    };
    defer io.deinit();

    // If we get here, IO initialization was successful
    try testing.expect(true);
}

test "Logger functionality" {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    try Logger.init(allocator, .debug);
    defer Logger.deinit();

    // Test logging functions
    Logger.logFast(.info, "Test log message");
    Logger.logMetric("test_metric", 123.45, "ms");
    Logger.logWithContext(.debug, "test-context", "Test message with args: {d}", .{42});

    try testing.expect(true);
}

test "Memory allocation patterns" {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    // Test allocation patterns similar to what SyncMesh would use
    const buffer_size = 64 * 1024; // 64KB
    const buffer = try allocator.alloc(u8, buffer_size);
    defer allocator.free(buffer);

    // Fill buffer with test data
    for (buffer, 0..) |*byte, i| {
        byte.* = @intCast(i % 256);
    }

    // Test checksum calculation (similar to file checksums)
    const checksum = std.hash.crc.Crc32.hash(buffer);
    try testing.expect(checksum != 0);
}

test "Platform detection" {
    const builtin = @import("builtin");
    
    // Ensure we can detect the platform correctly
    const platform_supported = switch (builtin.os.tag) {
        .linux, .macos, .windows => true,
        else => false,
    };
    
    try testing.expect(platform_supported);
    
    // Test platform-specific code paths exist
    switch (builtin.os.tag) {
        .linux => {
            // Linux should have io_uring support
            std.log.info("Testing on Linux with io_uring");
        },
        .macos => {
            // macOS should have kqueue support  
            std.log.info("Testing on macOS with kqueue");
        },
        .windows => {
            // Windows should have IOCP support
            std.log.info("Testing on Windows with IOCP");
        },
        else => {
            std.log.warn("Testing on unsupported platform");
        },
    }
}

test "Concurrency primitives" {
    // Test atomic operations used in lock-free queues
    var atomic_counter = std.atomic.Atomic(u32).init(0);
    
    const initial = atomic_counter.load(.Acquire);
    _ = atomic_counter.fetchAdd(1, .AcqRel);
    const final = atomic_counter.load(.Acquire);
    
    try testing.expectEqual(initial + 1, final);
}

test "Error handling" {
    // Test error handling patterns
    const TestError = error{ TestFailure, ResourceExhausted };
    
    const test_func = struct {
        fn failing_operation() TestError!u32 {
            return TestError.TestFailure;
        }
    }.failing_operation;
    
    const result = test_func();
    try testing.expectError(TestError.TestFailure, result);
}

// Benchmark tests
test "Performance: hash calculation" {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    const data_size = 1024 * 1024; // 1MB
    const data = try allocator.alloc(u8, data_size);
    defer allocator.free(data);

    // Fill with random-ish data
    for (data, 0..) |*byte, i| {
        byte.* = @intCast((i * 7) % 256);
    }

    const start_time = std.time.nanoTimestamp();
    const checksum = std.hash.crc.Crc32.hash(data);
    const end_time = std.time.nanoTimestamp();

    const duration_ns = end_time - start_time;
    const mb_per_sec = (@as(f64, @floatFromInt(data_size)) / (@as(f64, @floatFromInt(duration_ns)) / 1e9)) / (1024 * 1024);

    std.log.info("Hash calculation: {d} MB/s (checksum: 0x{x})", .{ mb_per_sec, checksum });
    
    try testing.expect(checksum != 0);
    try testing.expect(mb_per_sec > 0);
}