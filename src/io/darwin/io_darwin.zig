const std = @import("std");
const darwin = std.c;
const Allocator = std.mem.Allocator;
const IO = @import("../io.zig").IO;
const Completion = IO.Completion;

/// macOS-specific I/O implementation using kqueue
/// TigerBeetle pattern: kqueue for readiness, actual I/O in userland
pub const IoDarwin = struct {
    allocator: Allocator,
    kqueue_fd: i32,
    overflow_queue: OverflowQueue,
    running: bool,
    events: []darwin.Kevent,
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

    pub fn init(allocator: Allocator, options: IO.InitOptions) !IoDarwin {
        const kq = darwin.kqueue();
        if (kq == -1) {
            return error.KqueueInitFailed;
        }

        const events = try allocator.alloc(darwin.Kevent, options.entries);

        return IoDarwin{
            .allocator = allocator,
            .kqueue_fd = kq,
            .overflow_queue = OverflowQueue{},
            .running = false,
            .events = events,
            .max_events = options.entries,
        };
    }

    pub fn deinit(self: *IoDarwin) void {
        _ = darwin.close(self.kqueue_fd);
        self.allocator.free(self.events);
    }

    pub fn submit(self: *IoDarwin, completion: *Completion) void {
        // For kqueue, we handle operations differently than io_uring
        // Most operations need immediate processing or readiness monitoring
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

    fn submitRead(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.read;
        
        // First, try non-blocking read
        const result = std.posix.read(op.fd, op.buffer);
        if (result) |bytes_read| {
            // Success - complete immediately
            completion.callback(completion.context, completion, bytes_read);
            return;
        } else |err| switch (err) {
            error.WouldBlock => {
                // Need to wait for readiness
                self.addReadEvent(op.fd, completion);
            },
            else => {
                // Other error - complete with error
                completion.callback(completion.context, completion, err);
            },
        }
    }

    fn submitWrite(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.write;
        
        const result = std.posix.write(op.fd, op.buffer);
        if (result) |bytes_written| {
            completion.callback(completion.context, completion, bytes_written);
            return;
        } else |err| switch (err) {
            error.WouldBlock => {
                self.addWriteEvent(op.fd, completion);
            },
            else => {
                completion.callback(completion.context, completion, err);
            },
        }
    }

    fn submitAccept(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.accept;
        
        const result = std.posix.accept(op.socket, op.address, op.address_size, std.posix.SOCK.NONBLOCK);
        if (result) |client_fd| {
            completion.callback(completion.context, completion, @intCast(client_fd));
            return;
        } else |err| switch (err) {
            error.WouldBlock => {
                self.addReadEvent(op.socket, completion);
            },
            else => {
                completion.callback(completion.context, completion, err);
            },
        }
    }

    fn submitConnect(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.connect;
        
        const result = std.posix.connect(op.socket, op.address, op.address_size);
        if (result) |_| {
            completion.callback(completion.context, completion, 0);
            return;
        } else |err| switch (err) {
            error.WouldBlock => {
                self.addWriteEvent(op.socket, completion);
            },
            else => {
                completion.callback(completion.context, completion, err);
            },
        }
    }

    fn submitClose(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.close;
        std.posix.close(op.fd);
        completion.callback(completion.context, completion, 0);
    }

    fn submitTimeout(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.timeout;
        
        var change = std.mem.zeroes(darwin.Kevent);
        change.ident = 1; // Timer ID
        change.filter = darwin.EVFILT_TIMER;
        change.flags = darwin.EV_ADD | darwin.EV_ONESHOT;
        change.data = @intCast(op.nanoseconds / 1_000_000); // Convert to milliseconds
        change.udata = @intFromPtr(completion);
        
        const result = darwin.kevent(self.kqueue_fd, &change, 1, null, 0, null);
        if (result == -1) {
            completion.callback(completion.context, completion, error.TimerAddFailed);
        }
    }

    fn submitFsync(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.fsync;
        
        const result = std.posix.fsync(op.fd);
        if (result) |_| {
            completion.callback(completion.context, completion, 0);
        } else |err| {
            completion.callback(completion.context, completion, err);
        }
    }

    fn submitOpenat(self: *IoDarwin, completion: *Completion) void {
        const op = completion.operation.openat;
        
        const result = std.posix.openat(op.dir_fd, op.path, op.flags, op.mode);
        if (result) |fd| {
            completion.callback(completion.context, completion, @intCast(fd));
        } else |err| {
            completion.callback(completion.context, completion, err);
        }
    }

    fn addReadEvent(self: *IoDarwin, fd: i32, completion: *Completion) void {
        var change = std.mem.zeroes(darwin.Kevent);
        change.ident = @intCast(fd);
        change.filter = darwin.EVFILT_READ;
        change.flags = darwin.EV_ADD | darwin.EV_ONESHOT;
        change.udata = @intFromPtr(completion);
        
        const result = darwin.kevent(self.kqueue_fd, &change, 1, null, 0, null);
        if (result == -1) {
            completion.callback(completion.context, completion, error.EventAddFailed);
        }
    }

    fn addWriteEvent(self: *IoDarwin, fd: i32, completion: *Completion) void {
        var change = std.mem.zeroes(darwin.Kevent);
        change.ident = @intCast(fd);
        change.filter = darwin.EVFILT_WRITE;
        change.flags = darwin.EV_ADD | darwin.EV_ONESHOT;
        change.udata = @intFromPtr(completion);
        
        const result = darwin.kevent(self.kqueue_fd, &change, 1, null, 0, null);
        if (result == -1) {
            completion.callback(completion.context, completion, error.EventAddFailed);
        }
    }

    pub fn runForNs(self: *IoDarwin, nanoseconds: u64) !void {
        const start_time = std.time.nanoTimestamp();
        const end_time = start_time + @as(i128, nanoseconds);
        
        self.running = true;
        defer self.running = false;
        
        while (self.running and std.time.nanoTimestamp() < end_time) {
            _ = try self.tick();
            std.time.sleep(1000); // 1 microsecond
        }
    }

    pub fn tick(self: *IoDarwin) !u32 {
        // Poll kqueue for events
        const timeout = darwin.timespec{ .tv_sec = 0, .tv_nsec = 1_000_000 }; // 1ms timeout
        const num_events = darwin.kevent(
            self.kqueue_fd,
            null, 0,
            self.events.ptr, @intCast(self.events.len),
            &timeout
        );
        
        if (num_events == -1) {
            return error.KqueuePollFailed;
        }
        
        var completed: u32 = 0;
        for (self.events[0..@intCast(num_events)]) |event| {
            const completion = @as(*Completion, @ptrFromInt(event.udata));
            completed += 1;
            
            // Handle the ready event by performing the actual I/O
            switch (completion.operation) {
                .read => {
                    const op = completion.operation.read;
                    const result = std.posix.read(op.fd, op.buffer);
                    completion.callback(completion.context, completion, result);
                },
                .write => {
                    const op = completion.operation.write;
                    const result = std.posix.write(op.fd, op.buffer);
                    completion.callback(completion.context, completion, result);
                },
                .accept => {
                    const op = completion.operation.accept;
                    const result = std.posix.accept(op.socket, op.address, op.address_size, 0);
                    completion.callback(completion.context, completion, result);
                },
                .connect => {
                    const op = completion.operation.connect;
                    const result = std.posix.connect(op.socket, op.address, op.address_size);
                    completion.callback(completion.context, completion, result);
                },
                .timeout => {
                    completion.callback(completion.context, completion, 0);
                },
                else => {
                    // Should not reach here for other operations
                    completion.callback(completion.context, completion, error.UnexpectedEvent);
                },
            }
        }
        
        return completed;
    }

    pub fn stop(self: *IoDarwin) void {
        self.running = false;
    }
};