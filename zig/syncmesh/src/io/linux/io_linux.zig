const std = @import("std");
const linux = std.os.linux;
const Allocator = std.mem.Allocator;
const IO = @import("../io.zig").IO;
const Completion = IO.Completion;

/// Linux-specific I/O implementation using io_uring
/// Inspired by TigerBeetle's high-performance approach
pub const IoLinux = struct {
    allocator: Allocator,
    ring: linux.IO_Uring,
    overflow_queue: OverflowQueue,
    running: bool,

    /// Intrusive linked list for overflow operations when ring is full
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

    pub fn init(allocator: Allocator, options: IO.InitOptions) !IoLinux {
        const ring = try linux.IO_Uring.init(options.entries, options.flags);

        return IoLinux{
            .allocator = allocator,
            .ring = ring,
            .overflow_queue = OverflowQueue{},
            .running = false,
        };
    }

    pub fn deinit(self: *IoLinux) void {
        self.ring.deinit();
    }

    /// Submit a completion for execution
    /// TigerBeetle pattern: try immediate submission, queue on overflow
    pub fn submit(self: *IoLinux, completion: *Completion) void {
        // Try to submit immediately
        if (self.trySubmitImmediate(completion)) {
            return;
        }

        // Queue for later submission if ring is full
        self.overflow_queue.push(completion);
    }

    /// Attempt immediate submission to io_uring
    fn trySubmitImmediate(self: *IoLinux, completion: *Completion) bool {
        const sqe = self.ring.get_sqe() catch return false;

        // Set user data to completion pointer for callback tracking
        sqe.user_data = @intFromPtr(completion);

        // Configure SQE based on operation type
        switch (completion.operation) {
            .read => |op| {
                sqe.prep_read(op.fd, op.buffer, op.offset);
            },
            .write => |op| {
                sqe.prep_write(op.fd, op.buffer, op.offset);
            },
            .accept => |op| {
                sqe.prep_accept(op.socket, op.address, op.address_size, 0);
            },
            .connect => |op| {
                sqe.prep_connect(op.socket, op.address, op.address_size);
            },
            .close => |op| {
                sqe.prep_close(op.fd);
            },
            .timeout => |op| {
                const timespec = linux.kernel_timespec{
                    .tv_sec = @intCast(op.nanoseconds / std.time.ns_per_s),
                    .tv_nsec = @intCast(op.nanoseconds % std.time.ns_per_s),
                };
                sqe.prep_timeout(&timespec, 0, 0);
            },
            .fsync => |op| {
                sqe.prep_fsync(op.fd, 0);
            },
            .openat => |op| {
                sqe.prep_openat(op.dir_fd, op.path.ptr, op.flags, op.mode);
            },
        }

        return true;
    }

    /// Process overflow queue and submit pending operations
    fn processOverflowQueue(self: *IoLinux) void {
        while (!self.overflow_queue.isEmpty()) {
            const completion = self.overflow_queue.pop() orelse break;

            if (!self.trySubmitImmediate(completion)) {
                // Ring still full, put it back at the front
                // This is a simple strategy - TigerBeetle might have more sophisticated queuing
                const old_head = self.overflow_queue.head;
                completion.next = old_head;
                self.overflow_queue.head = completion;
                if (old_head == null) {
                    self.overflow_queue.tail = completion;
                }
                self.overflow_queue.count += 1;
                break;
            }
        }
    }

    /// Run the event loop for specified nanoseconds
    pub fn runForNs(self: *IoLinux, nanoseconds: u64) !void {
        const start_time = std.time.nanoTimestamp();
        const end_time = start_time + @as(i128, nanoseconds);

        self.running = true;
        defer self.running = false;

        while (self.running and std.time.nanoTimestamp() < end_time) {
            _ = try self.tick();

            // Brief yield to avoid busy spinning
            std.time.sleep(1000); // 1 microsecond
        }
    }

    /// Single iteration of the event loop
    pub fn tick(self: *IoLinux) !u32 {
        // Process overflow queue first
        self.processOverflowQueue();

        // Submit all pending operations
        _ = try self.ring.submit();

        // Process completions
        var completed: u32 = 0;
        while (self.ring.cq_ready() > 0) {
            const cqe = try self.ring.copy_cqe();
            completed += 1;

            // Extract completion from user data
            const completion = @as(*Completion, @ptrFromInt(cqe.user_data));

            // Convert result
            const result: anyerror!usize = if (cqe.res < 0)
                std.posix.unexpectedErrno(@enumFromInt(-cqe.res))
            else
                @intCast(cqe.res);

            // Invoke callback
            completion.callback(completion.context, completion, result);
        }

        return completed;
    }

    /// Stop the event loop
    pub fn stop(self: *IoLinux) void {
        self.running = false;
    }
};
