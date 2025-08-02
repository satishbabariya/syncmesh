const std = @import("std");
const windows = std.os.windows;
const Allocator = std.mem.Allocator;
const IO = @import("../io.zig").IO;
const Completion = IO.Completion;

/// Windows-specific I/O implementation using IOCP (I/O Completion Ports)
/// TigerBeetle pattern adapted for Windows async I/O
pub const IoWindows = struct {
    allocator: Allocator,
    iocp_handle: windows.HANDLE,
    overflow_queue: OverflowQueue,
    running: bool,
    max_events: u16,

    const OverflowQueue = struct {
        head: ?*Completion = null,
        tail: ?*Completion = null,
        count: u32 = 0,

        fn push(self: *OverflowQueue, completion: *Completion) void {
            completion.next = null;
            if (self.tail) |tail| {
                tail.next = completion;
            } else {
                self.head = completion;
            }
            self.tail = completion;
            self.count += 1;
        }

        fn pop(self: *OverflowQueue) ?*Completion {
            const head = self.head orelse return null;
            self.head = head.next;
            if (self.head == null) {
                self.tail = null;
            }
            head.next = null;
            self.count -= 1;
            return head;
        }

        fn isEmpty(self: *const OverflowQueue) bool {
            return self.head == null;
        }
    };

    pub fn init(allocator: Allocator, options: IO.InitOptions) !IoWindows {
        const iocp = windows.kernel32.CreateIoCompletionPort(windows.INVALID_HANDLE_VALUE, null, 0, 0 // Use system default (number of processors)
        ) orelse return error.IOCPCreationFailed;

        return IoWindows{
            .allocator = allocator,
            .iocp_handle = iocp,
            .overflow_queue = OverflowQueue{},
            .running = false,
            .max_events = options.entries,
        };
    }

    pub fn deinit(self: *IoWindows) void {
        _ = windows.kernel32.CloseHandle(self.iocp_handle);
    }

    pub fn submit(self: *IoWindows, completion: *Completion) void {
        // Windows IOCP has different semantics - operations are submitted directly
        // to the file handles which are associated with the completion port
        switch (completion.operation) {
            .read => self.submitRead(completion),
            .write => self.submitWrite(completion),
            .accept => self.submitAccept(completion),
            .connect => self.submitConnect(completion),
            .close => self.submitClose(completion),
            .timeout => self.submitTimeout(completion),
            .fsync => self.submitFsync(completion),
            .openat => self.submitOpenat(completion),
        }
    }

    fn submitRead(_: *IoWindows, completion: *Completion) void {
        const op = completion.operation.read;

        // Create OVERLAPPED structure for async operation
        var overlapped = std.mem.zeroes(windows.OVERLAPPED);
        overlapped.Internal = @intFromPtr(completion);
        overlapped.Offset = @truncate(op.offset);
        overlapped.OffsetHigh = @truncate(op.offset >> 32);

        var bytes_read: windows.DWORD = 0;
        const result = windows.kernel32.ReadFile(@ptrFromInt(op.fd), op.buffer.ptr, @intCast(op.buffer.len), &bytes_read, &overlapped);

        if (result == 0) {
            const err = windows.kernel32.GetLastError();
            if (err != windows.ERROR_IO_PENDING) {
                completion.callback(completion.context, completion, error.ReadFailed);
                return;
            }
        }
    }

    fn submitWrite(_: *IoWindows, completion: *Completion) void {
        const op = completion.operation.write;

        var overlapped = std.mem.zeroes(windows.OVERLAPPED);
        overlapped.Internal = @intFromPtr(completion);
        overlapped.Offset = @truncate(op.offset);
        overlapped.OffsetHigh = @truncate(op.offset >> 32);

        var bytes_written: windows.DWORD = 0;
        const result = windows.kernel32.WriteFile(@ptrFromInt(op.fd), op.buffer.ptr, @intCast(op.buffer.len), &bytes_written, &overlapped);

        if (result == 0) {
            const err = windows.kernel32.GetLastError();
            if (err != windows.ERROR_IO_PENDING) {
                completion.callback(completion.context, completion, error.WriteFailed);
                return;
            }
        }
    }

    fn submitAccept(_: *IoWindows, completion: *Completion) void {
        // Windows socket accept is more complex with AcceptEx
        // For simplicity, we'll use blocking accept and complete immediately
        const op = completion.operation.accept;

        const client_socket = windows.ws2_32.accept(@intFromPtr(op.socket), op.address, op.address_size);

        if (client_socket == windows.ws2_32.INVALID_SOCKET) {
            completion.callback(completion.context, completion, error.AcceptFailed);
        } else {
            completion.callback(completion.context, completion, @intCast(client_socket));
        }
    }

    fn submitConnect(_: *IoWindows, completion: *Completion) void {
        const op = completion.operation.connect;

        const result = windows.ws2_32.connect(@intFromPtr(op.socket), op.address, op.address_size);

        if (result == windows.ws2_32.SOCKET_ERROR) {
            completion.callback(completion.context, completion, error.ConnectFailed);
        } else {
            completion.callback(completion.context, completion, 0);
        }
    }

    fn submitClose(_: *IoWindows, completion: *Completion) void {
        const op = completion.operation.close;
        _ = windows.kernel32.CloseHandle(@ptrFromInt(op.fd));
        completion.callback(completion.context, completion, 0);
    }

    fn submitTimeout(self: *IoWindows, completion: *Completion) void {
        const op = completion.operation.timeout;

        // Create a timer
        const timer = windows.kernel32.CreateWaitableTimerW(null, windows.TRUE, null);
        if (timer == null) {
            completion.callback(completion.context, completion, error.TimerCreationFailed);
            return;
        }

        // Set timer
        var due_time: windows.LARGE_INTEGER = @intCast(-(op.nanoseconds / 100)); // Convert to 100ns intervals
        if (windows.kernel32.SetWaitableTimer(timer, &due_time, 0, null, null, windows.FALSE) == 0) {
            _ = windows.kernel32.CloseHandle(timer);
            completion.callback(completion.context, completion, error.TimerSetFailed);
            return;
        }

        // For simplicity, we'll queue this timer completion for later processing
        self.overflow_queue.push(completion);
    }

    fn submitFsync(_: *IoWindows, completion: *Completion) void {
        const op = completion.operation.fsync;

        const result = windows.kernel32.FlushFileBuffers(@ptrFromInt(op.fd));
        if (result == 0) {
            completion.callback(completion.context, completion, error.FsyncFailed);
        } else {
            completion.callback(completion.context, completion, 0);
        }
    }

    fn submitOpenat(self: *IoWindows, completion: *Completion) void {
        const op = completion.operation.openat;

        // Convert path to wide string for Windows
        var path_buf: [windows.PATH_MAX_WIDE]u16 = undefined;
        const path_len = std.unicode.utf8ToUtf16Le(path_buf[0..], op.path) catch {
            completion.callback(completion.context, completion, error.InvalidPath);
            return;
        };
        path_buf[path_len] = 0;

        const handle = windows.kernel32.CreateFileW(&path_buf, windows.GENERIC_READ | windows.GENERIC_WRITE, windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE, null, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, null);

        if (handle == windows.INVALID_HANDLE_VALUE) {
            completion.callback(completion.context, completion, error.OpenFailed);
        } else {
            // Associate with completion port
            _ = windows.kernel32.CreateIoCompletionPort(handle, self.iocp_handle, @intFromPtr(completion), 0);
            completion.callback(completion.context, completion, @intFromPtr(handle));
        }
    }

    pub fn runForNs(self: *IoWindows, nanoseconds: u64) !void {
        const start_time = std.time.nanoTimestamp();
        const end_time = start_time + @as(i128, nanoseconds);

        self.running = true;
        defer self.running = false;

        while (self.running and std.time.nanoTimestamp() < end_time) {
            _ = try self.tick();
            std.time.sleep(1000); // 1 microsecond
        }
    }

    pub fn tick(self: *IoWindows) !u32 {
        var bytes_transferred: windows.DWORD = 0;
        var completion_key: windows.ULONG_PTR = 0;
        var overlapped: ?*windows.OVERLAPPED = null;

        const timeout_ms: windows.DWORD = 1; // 1ms timeout
        const result = windows.kernel32.GetQueuedCompletionStatus(self.iocp_handle, &bytes_transferred, &completion_key, &overlapped, timeout_ms);

        var completed: u32 = 0;

        if (result != 0 and overlapped != null) {
            // Extract completion from overlapped structure
            const completion = @as(*Completion, @ptrFromInt(overlapped.?.Internal));
            completed += 1;

            // Complete the operation
            completion.callback(completion.context, completion, @as(usize, bytes_transferred));
        }

        // Process any overflow queue items (like timers)
        if (!self.overflow_queue.isEmpty()) {
            if (self.overflow_queue.pop()) |completion| {
                completion.callback(completion.context, completion, 0);
                completed += 1;
            }
        }

        return completed;
    }

    pub fn stop(self: *IoWindows) void {
        self.running = false;
    }
};
