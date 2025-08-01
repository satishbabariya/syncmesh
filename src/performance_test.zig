const std = @import("std");
const print = std.debug.print;

pub fn main() !void {
    print("🚀 SyncMesh Performance Tests (TigerBeetle-inspired)\n", .{});
    print("===================================================\n", .{});

    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    // Test 1: Memory allocation speed (TigerBeetle pattern)
    try testMemoryAllocation(allocator);
    
    // Test 2: Hash performance (file checksums)
    try testHashPerformance(allocator);
    
    // Test 3: Atomic operations speed (lock-free queues)
    try testAtomicPerformance();
    
    // Test 4: Buffer operations (zero-copy patterns)
    try testBufferOperations(allocator);

    print("\n🎉 All performance tests completed!\n", .{});
}

fn testMemoryAllocation(allocator: std.mem.Allocator) !void {
    print("\n📊 Memory Allocation Performance\n", .{});
    print("--------------------------------\n", .{});
    
    const iterations = 10000;
    const allocation_sizes = [_]usize{ 64, 256, 1024, 4096, 16384, 65536 };
    
    for (allocation_sizes) |size| {
        var allocations = std.ArrayList([]u8).init(allocator);
        defer {
            for (allocations.items) |allocation| {
                allocator.free(allocation);
            }
            allocations.deinit();
        }
        
        const start_time = std.time.nanoTimestamp();
        
        for (0..iterations) |_| {
            const allocation = try allocator.alloc(u8, size);
            try allocations.append(allocation);
        }
        
        const end_time = std.time.nanoTimestamp();
        const duration_ns = end_time - start_time;
        const allocs_per_sec = (@as(f64, @floatFromInt(iterations)) / (@as(f64, @floatFromInt(duration_ns)) / 1e9));
        
        print("  {d:>5} bytes: {d:>8.0} allocs/s\n", .{ size, allocs_per_sec });
    }
}

fn testHashPerformance(allocator: std.mem.Allocator) !void {
    print("\n📊 Hash Performance (File Checksums)\n", .{});
    print("------------------------------------\n", .{});
    
    const data_sizes = [_]usize{ 1024, 16384, 262144, 1048576, 16777216 }; // 1KB to 16MB
    
    for (data_sizes) |size| {
        const data = try allocator.alloc(u8, size);
        defer allocator.free(data);
        
        // Fill with pseudo-random data
        for (data, 0..) |*byte, i| {
            byte.* = @intCast((i * 13 + 37) % 256);
        }
        
        var iterations: u32 = 1000;
        if (size >= 1048576) iterations = 100; // Less iterations for large data
        const start_time = std.time.nanoTimestamp();
        
        var checksum: u32 = 0;
        for (0..iterations) |_| {
            checksum ^= std.hash.crc.Crc32.hash(data);
        }
        
        const end_time = std.time.nanoTimestamp();
        const duration_ns = end_time - start_time;
        
        const total_bytes = @as(f64, @floatFromInt(iterations * size));
        const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
        const mb_per_sec = (total_bytes / duration_s) / (1024 * 1024);
        
        print("  {d:>8} bytes: {d:>8.2} MB/s (checksum: 0x{x})\n", .{ size, mb_per_sec, checksum });
    }
}

fn testAtomicPerformance() !void {
    print("\n📊 Atomic Operations Performance (Lock-free)\n", .{});
    print("--------------------------------------------\n", .{});
    
    var counter: u64 = 0;
    const iterations = 1000000;
    
    const start_time = std.time.nanoTimestamp();
    
    for (0..iterations) |_| {
        _ = @atomicRmw(u64, &counter, .Add, 1, .monotonic);
    }
    
    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    const ops_per_sec = (@as(f64, @floatFromInt(iterations)) / (@as(f64, @floatFromInt(duration_ns)) / 1e9));
    
    print("  Atomic increments: {d:>8.0} ops/s\n", .{ops_per_sec});
    print("  Final counter value: {d}\n", .{@atomicLoad(u64, &counter, .monotonic)});
}

fn testBufferOperations(allocator: std.mem.Allocator) !void {
    print("\n📊 Buffer Operations (Zero-copy patterns)\n", .{});
    print("-----------------------------------------\n", .{});
    
    const buffer_size = 1024 * 1024; // 1MB
    const source = try allocator.alloc(u8, buffer_size);
    defer allocator.free(source);
    const dest = try allocator.alloc(u8, buffer_size);
    defer allocator.free(dest);
    
    // Fill source with test data
    for (source, 0..) |*byte, i| {
        byte.* = @intCast(i % 256);
    }
    
    const iterations = 1000;
    const start_time = std.time.nanoTimestamp();
    
    for (0..iterations) |_| {
        @memcpy(dest, source);
    }
    
    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    
    const total_bytes = @as(f64, @floatFromInt(iterations * buffer_size));
    const duration_s = @as(f64, @floatFromInt(duration_ns)) / 1e9;
    const gb_per_sec = (total_bytes / duration_s) / (1024 * 1024 * 1024);
    
    print("  Memory copy: {d:>8.2} GB/s\n", .{gb_per_sec});
    print("  Verification: {s}\n", .{if (std.mem.eql(u8, source[0..100], dest[0..100])) "PASSED" else "FAILED"});
}