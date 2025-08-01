const std = @import("std");
const builtin = @import("builtin");
const Allocator = std.mem.Allocator;

// Platform-specific implementations
const IoLinux = if (builtin.os.tag == .linux) @import("linux/io_linux.zig").IoLinux else void;
const IoDarwin = if (builtin.os.tag == .macos) @import("darwin/io_darwin.zig").IoDarwin else void;
const IoWindows = if (builtin.os.tag == .windows) @import("windows/io_windows.zig").IoWindows else void;

/// High-performance cross-platform I/O abstraction inspired by TigerBeetle
/// Provides unified interface over io_uring (Linux), kqueue (macOS), and IOCP (Windows)
pub const IO = struct {
    allocator: Allocator,
    impl: Impl,

    const Impl = switch (builtin.os.tag) {
        .linux => IoLinux,
        .macos => IoDarwin,
        .windows => IoWindows,
        else => @compileError("Unsupported platform for SyncMesh"),
    };

    pub const InitOptions = struct {
        entries: u16 = 256,
        flags: u32 = 0,
    };

    pub const CompletionCallback = *const fn (
        context: ?*anyopaque,
        completion: *Completion,
        result: anyerror!usize,
    ) void;

    /// Represents a single I/O operation with completion tracking
    pub const Completion = struct {
        io: *IO,
        context: ?*anyopaque,
        callback: CompletionCallback,
        operation: Operation,
        next: ?*Completion = null, // For intrusive linked list in overflow queue

        pub const Operation = union(enum) {
            read: ReadOp,
            write: WriteOp,
            accept: AcceptOp,
            connect: ConnectOp,
            close: CloseOp,
            timeout: TimeoutOp,
            fsync: FsyncOp,
            openat: OpenatOp,

            pub const ReadOp = struct {
                fd: std.posix.fd_t,
                buffer: []u8,
                offset: u64 = 0,
            };

            pub const WriteOp = struct {
                fd: std.posix.fd_t,
                buffer: []const u8,
                offset: u64 = 0,
            };

            pub const AcceptOp = struct {
                socket: std.posix.socket_t,
                address: ?*std.posix.sockaddr = null,
                address_size: ?*std.posix.socklen_t = null,
            };

            pub const ConnectOp = struct {
                socket: std.posix.socket_t,
                address: *const std.posix.sockaddr,
                address_size: std.posix.socklen_t,
            };

            pub const CloseOp = struct {
                fd: std.posix.fd_t,
            };

            pub const TimeoutOp = struct {
                nanoseconds: u64,
            };

            pub const FsyncOp = struct {
                fd: std.posix.fd_t,
            };

            pub const OpenatOp = struct {
                dir_fd: std.posix.fd_t,
                path: []const u8,
                flags: u32,
                mode: std.posix.mode_t,
            };
        };
    };

    /// Initialize the I/O subsystem with platform-specific backend
    pub fn init(allocator: Allocator, options: InitOptions) !IO {
        return IO{
            .allocator = allocator,
            .impl = try Impl.init(allocator, options),
        };
    }

    /// Cleanup and shutdown the I/O subsystem
    pub fn deinit(self: *IO) void {
        self.impl.deinit();
    }

    /// Submit an I/O operation for asynchronous execution
    /// TigerBeetle-style: either submits immediately or adds to overflow queue
    pub fn submit(self: *IO, completion: *Completion) void {
        self.impl.submit(completion);
    }

    /// Run the event loop for the specified duration (nanoseconds)
    /// Processes completions and submits queued operations
    pub fn runForNs(self: *IO, nanoseconds: u64) !void {
        try self.impl.runForNs(nanoseconds);
    }

    /// Run one iteration of the event loop
    /// Returns number of completed operations
    pub fn tick(self: *IO) !u32 {
        return try self.impl.tick();
    }

    /// Convenience methods for common operations
    pub fn read(
        self: *IO,
        comptime Context: type,
        context: Context,
        comptime callback: fn (ctx: Context, completion: *Completion, result: anyerror!usize) void,
        completion: *Completion,
        fd: std.posix.fd_t,
        buffer: []u8,
        offset: u64,
    ) void {
        completion.* = .{
            .io = self,
            .context = context,
            .callback = struct {
                fn wrapper(ctx: ?*anyopaque, comp: *Completion, result: anyerror!usize) void {
                    callback(@as(Context, @ptrCast(@alignCast(ctx))), comp, result);
                }
            }.wrapper,
            .operation = .{ .read = .{ .fd = fd, .buffer = buffer, .offset = offset } },
        };
        self.submit(completion);
    }

    pub fn write(
        self: *IO,
        comptime Context: type,
        context: Context,
        comptime callback: fn (ctx: Context, completion: *Completion, result: anyerror!usize) void,
        completion: *Completion,
        fd: std.posix.fd_t,
        buffer: []const u8,
        offset: u64,
    ) void {
        completion.* = .{
            .io = self,
            .context = context,
            .callback = struct {
                fn wrapper(ctx: ?*anyopaque, comp: *Completion, result: anyerror!usize) void {
                    callback(@as(Context, @ptrCast(@alignCast(ctx))), comp, result);
                }
            }.wrapper,
            .operation = .{ .write = .{ .fd = fd, .buffer = buffer, .offset = offset } },
        };
        self.submit(completion);
    }

    pub fn accept(
        self: *IO,
        comptime Context: type,
        context: Context,
        comptime callback: fn (ctx: Context, completion: *Completion, result: anyerror!usize) void,
        completion: *Completion,
        socket: std.posix.socket_t,
    ) void {
        completion.* = .{
            .io = self,
            .context = context,
            .callback = struct {
                fn wrapper(ctx: ?*anyopaque, comp: *Completion, result: anyerror!usize) void {
                    callback(@as(Context, @ptrCast(@alignCast(ctx))), comp, result);
                }
            }.wrapper,
            .operation = .{ .accept = .{ .socket = socket } },
        };
        self.submit(completion);
    }

    pub fn connect(
        self: *IO,
        comptime Context: type,
        context: Context,
        comptime callback: fn (ctx: Context, completion: *Completion, result: anyerror!usize) void,
        completion: *Completion,
        socket: std.posix.socket_t,
        address: *const std.posix.sockaddr,
        address_size: std.posix.socklen_t,
    ) void {
        completion.* = .{
            .io = self,
            .context = context,
            .callback = struct {
                fn wrapper(ctx: ?*anyopaque, comp: *Completion, result: anyerror!usize) void {
                    callback(@as(Context, @ptrCast(@alignCast(ctx))), comp, result);
                }
            }.wrapper,
            .operation = .{ .connect = .{ .socket = socket, .address = address, .address_size = address_size } },
        };
        self.submit(completion);
    }

    pub fn timeout(
        self: *IO,
        comptime Context: type,
        context: Context,
        comptime callback: fn (ctx: Context, completion: *Completion, result: anyerror!usize) void,
        completion: *Completion,
        nanoseconds: u64,
    ) void {
        completion.* = .{
            .io = self,
            .context = context,
            .callback = struct {
                fn wrapper(ctx: ?*anyopaque, comp: *Completion, result: anyerror!usize) void {
                    callback(@as(Context, @ptrCast(@alignCast(ctx))), comp, result);
                }
            }.wrapper,
            .operation = .{ .timeout = .{ .nanoseconds = nanoseconds } },
        };
        self.submit(completion);
    }
};