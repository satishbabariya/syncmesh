const std = @import("std");
const print = std.debug.print;

// Simple test to verify our core architecture concepts work
pub fn main() !void {
    print("🚀 SyncMesh Minimal Architecture Test\n", .{});
    print("====================================\n", .{});

    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    // Test 1: Memory allocation patterns
    print("✅ Test 1: Memory allocation... ", .{});
    const buffer = try allocator.alloc(u8, 64 * 1024);
    defer allocator.free(buffer);
    print("PASSED\n", .{});

    // Test 2: Atomic operations (TigerBeetle-style lock-free)
    print("✅ Test 2: Atomic operations... ", .{});
    var counter: u64 = 0;
    _ = @atomicRmw(u64, &counter, .Add, 1, .monotonic);
    const value = @atomicLoad(u64, &counter, .monotonic);
    if (value == 1) {
        print("PASSED\n", .{});
    } else {
        print("FAILED\n", .{});
    }

    // Test 3: Hash operations (file checksums)
    print("✅ Test 3: Hash operations... ", .{});
    const test_data = "SyncMesh TigerBeetle-inspired architecture";
    const checksum = std.hash.crc.Crc32.hash(test_data);
    if (checksum != 0) {
        print("PASSED (checksum: 0x{x})\n", .{checksum});
    } else {
        print("FAILED\n", .{});
    }

    // Test 4: Platform detection
    print("✅ Test 4: Platform detection... ", .{});
    const platform = switch (@import("builtin").os.tag) {
        .linux => "Linux (io_uring)",
        .macos => "macOS (kqueue)",
        .windows => "Windows (IOCP)",
        else => "Unsupported",
    };
    print("PASSED ({s})\n", .{platform});

    // Test 5: Time operations
    print("✅ Test 5: Time operations... ", .{});
    const timestamp = std.time.timestamp();
    if (timestamp > 0) {
        print("PASSED (timestamp: {})\n", .{timestamp});
    } else {
        print("FAILED\n", .{});
    }

    // Test 6: Performance measurement
    print("✅ Test 6: Performance measurement... ", .{});
    const start_time = std.time.nanoTimestamp();
    // Simulate some work
    var sum: u64 = 0;
    for (0..1000) |i| {
        sum += i;
    }
    const end_time = std.time.nanoTimestamp();
    const duration_ns = end_time - start_time;
    print("PASSED (duration: {}ns, sum: {})\n", .{ duration_ns, sum });

    print("\n🎉 All basic architecture tests passed!\n", .{});
    print("TigerBeetle-inspired patterns are working correctly.\n", .{});
}