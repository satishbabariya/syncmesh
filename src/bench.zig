const std = @import("std");
const print = std.debug.print;
const Allocator = std.mem.Allocator;

const IO = @import("io/io.zig").IO;
const Config = @import("config/config.zig").Config;
const SyncMesh = @import("core/syncmesh.zig").SyncMesh;

/// SyncMesh Performance Benchmarks
/// Tests inspired by TigerBeetle's performance testing methodology
pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    print("🚀 SyncMesh Performance Benchmarks\n", .{});
    print("=====================================\n\n", .{});

    // Run individual benchmarks
    try benchmarkIOThroughput(allocator);
    try benchmarkFileOperations(allocator);
    try benchmarkNetworkOperations(allocator);
    try benchmarkMemoryAllocations(allocator);
    try benchmarkConcurrentOperations(allocator);

    print("\n✅ All benchmarks completed!\n", .{});
}

/// Benchmark I/O subsystem throughput
fn benchmarkIOThroughput(allocator: Allocator) !void {
    print("📊 I/O Throughput Benchmark\n", .{});
    print("---------------------------\n", .{});

    var io = IO.init(allocator, .{
        .entries = 512,
        .flags = 0,
    }) catch |err| {
        print("❌ I/O initialization failed: {}\n", .{err});
        print("   (This might be expected in some test environments)\n\n", .{});
        return;
    };
    defer io.deinit();

    // Create test files for I/O operations
    const test_file_sizes = [_]usize{ 1024, 4096, 16384, 65536, 262144, 1048576 }; // 1KB to 1MB

    for (test_file_sizes) |size| {
        const throughput = try measureIOThroughput(&io, allocator, size);
        print("  {d:>8} bytes: {d:>8.2} MB/s\n", .{ size, throughput });
    }

    print("\n");
}

fn measureIOThroughput(io: *IO, allocator: Allocator, buffer_size: usize) !f64 {
    const iterations = 1000;
    const buffer = try allocator.alloc(u8, buffer_size);
    defer allocator.free(buffer);

    // Fill buffer with test data
    for (buffer, 0..) |*byte, i| {
        byte.* = @intCast(i % 256);
    }

    // Create temporary file
    const temp_path = "/tmp/syncmesh_bench_file";
    const file = std.fs.cwd().createFile(temp_path, .{}) catch |err| {
        // If we can't create the file, return a placeholder value
        _ = err;
        return 0.0;
    };
    defer {
        file.close();
        std.fs.cwd().deleteFile(temp_path) catch {};
    }

    const fd = file.handle;

    // Measure write throughput
    var completed_ops: u32 = 0;
    const start_time = std.time.nanoTimestamp();

    // Submit operations
    for (0..iterations) |i| {
        var completion: IO.Completion = undefined;
        const offset = i * buffer_size;
        
        io.write(
            u32,
            &completed_ops,
            writeCallback,
            &completion,
            fd,
            buffer,
            offset,
        );
    }

    // Wait for completions
    const timeout_ns = 5 * std.time.ns_per_s; // 5 seconds
    var total_time: u64 = 0;
    
    while (completed_ops < iterations and total_time < timeout_ns) {
        _ = io.tick() catch break;
        const current_time = std.time.nanoTimestamp();
        total_time = @intCast(current_time - start_time);
        std.time.sleep(1000); // 1 microsecond
    }

    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;

    if (completed_ops == 0) return 0.0;

    const total_bytes = @as(f64, @floatFromInt(completed_ops * buffer_size));
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const mb_per_sec = (total_bytes / duration_s) / (1024 * 1024);

    return mb_per_sec;
}

fn writeCallback(completed_ops: *u32, completion: *IO.Completion, result: anyerror!usize) void {
    _ = completion;
    _ = result catch return;
    _ = @atomicRmw(u32, completed_ops, .Add, 1, .Monotonic);
}

/// Benchmark file operations (checksum, copy, etc.)
fn benchmarkFileOperations(allocator: Allocator) !void {
    print("📁 File Operations Benchmark\n");
    print("----------------------------\n");

    const file_sizes = [_]usize{ 1024, 16384, 262144, 1048576, 16777216 }; // 1KB to 16MB

    for (file_sizes) |size| {
        const checksum_rate = try benchmarkChecksumRate(allocator, size);
        print("  Checksum {d:>8} bytes: {d:>8.2} MB/s\n", .{ size, checksum_rate });
    }

    print("\n");
}

fn benchmarkChecksumRate(allocator: Allocator, file_size: usize) !f64 {
    const data = try allocator.alloc(u8, file_size);
    defer allocator.free(data);

    // Fill with pseudo-random data
    for (data, 0..) |*byte, i| {
        byte.* = @intCast((i * 13 + 37) % 256);
    }

    const iterations = 100;
    const start_time = std.time.nanoTimestamp();

    var checksum: u32 = 0;
    for (0..iterations) |_| {
        checksum ^= std.hash.crc.Crc32.hash(data);
    }

    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;

    // Prevent optimization
    if (checksum == 0) unreachable;

    const total_bytes = @as(f64, @floatFromInt(iterations * file_size));
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const mb_per_sec = (total_bytes / duration_s) / (1024 * 1024);

    return mb_per_sec;
}

/// Benchmark network operations
fn benchmarkNetworkOperations(allocator: Allocator) !void {
    print("🌐 Network Operations Benchmark\n");
    print("-------------------------------\n");

    // Test message serialization/deserialization performance
    const message_sizes = [_]usize{ 64, 256, 1024, 4096, 16384 };

    for (message_sizes) |size| {
        const rate = try benchmarkMessageSerialization(allocator, size);
        print("  Message {d:>5} bytes: {d:>8.0} msgs/s\n", .{ size, rate });
    }

    print("\n");
}

fn benchmarkMessageSerialization(allocator: Allocator, message_size: usize) !f64 {
    const message_data = try allocator.alloc(u8, message_size);
    defer allocator.free(message_data);

    // Fill with test data
    for (message_data, 0..) |*byte, i| {
        byte.* = @intCast(i % 256);
    }

    const iterations = 10000;
    const start_time = std.time.nanoTimestamp();

    var total_size: usize = 0;
    for (0..iterations) |_| {
        // Simulate message serialization/deserialization
        const checksum = std.hash.crc.Crc32.hash(message_data);
        total_size += message_size + @sizeOf(u32); // message + checksum
        
        // Prevent optimization
        if (checksum == 0) unreachable;
    }

    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const messages_per_sec = @as(f64, @floatFromInt(iterations)) / duration_s;

    _ = total_size; // Prevent unused variable warning
    return messages_per_sec;
}

/// Benchmark memory allocation patterns
fn benchmarkMemoryAllocations(allocator: Allocator) !void {
    print("💾 Memory Allocation Benchmark\n");
    print("------------------------------\n");

    // Test different allocation patterns
    const allocation_sizes = [_]usize{ 64, 256, 1024, 4096, 16384, 65536 };

    for (allocation_sizes) |size| {
        const rate = try benchmarkAllocationRate(allocator, size);
        print("  Alloc {d:>5} bytes: {d:>8.0} allocs/s\n", .{ size, rate });
    }

    print("\n");
}

fn benchmarkAllocationRate(allocator: Allocator, allocation_size: usize) !f64 {
    const iterations = 10000;
    var allocations = try std.ArrayList([]u8).initCapacity(allocator, iterations);
    defer {
        for (allocations.items) |allocation| {
            allocator.free(allocation);
        }
        allocations.deinit();
    }

    const start_time = std.time.nanoTimestamp();

    for (0..iterations) |_| {
        const allocation = try allocator.alloc(u8, allocation_size);
        allocations.appendAssumeCapacity(allocation);
    }

    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const allocs_per_sec = @as(f64, @floatFromInt(iterations)) / duration_s;

    return allocs_per_sec;
}

/// Benchmark concurrent operations
fn benchmarkConcurrentOperations(allocator: Allocator) !void {
    print("🔄 Concurrent Operations Benchmark\n");
    print("----------------------------------\n");

    // Test atomic operations used in lock-free queues
    const atomic_ops_rate = try benchmarkAtomicOperations();
    print("  Atomic operations: {d:>8.0} ops/s\n", .{atomic_ops_rate});

    // Test hash map operations
    const hashmap_rate = try benchmarkHashMapOperations(allocator);
    print("  HashMap operations: {d:>8.0} ops/s\n", .{hashmap_rate});

    print("\n");
}

fn benchmarkAtomicOperations() !f64 {
    var counter = std.atomic.Atomic(u64).init(0);
    const iterations = 1000000;

    const start_time = std.time.nanoTimestamp();

    for (0..iterations) |_| {
        _ = counter.fetchAdd(1, .Monotonic);
    }

    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const ops_per_sec = @as(f64, @floatFromInt(iterations)) / duration_s;

    return ops_per_sec;
}

fn benchmarkHashMapOperations(allocator: Allocator) !f64 {
    var map = std.HashMap(u64, u64, std.hash_map.AutoContext(u64), std.hash_map.default_max_load_percentage).init(allocator);
    defer map.deinit();

    const iterations = 100000;
    const start_time = std.time.nanoTimestamp();

    // Mix of put and get operations
    for (0..iterations) |i| {
        try map.put(i, i * 2);
        _ = map.get(i);
    }

    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const ops_per_sec = @as(f64, @floatFromInt(iterations * 2)) / duration_s; // put + get

    return ops_per_sec;
}