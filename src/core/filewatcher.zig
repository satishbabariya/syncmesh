const std = @import("std");
const Allocator = std.mem.Allocator;
const IO = @import("../io/io.zig").IO;
const Config = @import("../config/config.zig").Config;

/// High-performance file system watcher using async I/O
/// Monitors directories for changes and generates sync events
pub const FileWatcher = struct {
    allocator: Allocator,
    io: *IO,
    config: *const Config,
    
    // Watched directory state
    watch_fd: std.posix.fd_t,
    event_queue: EventQueue,
    running: bool,
    
    // Inotify/kqueue specific data
    platform_data: PlatformData,
    
    /// File system event
    pub const FileEvent = struct {
        path: []const u8,
        event_type: EventType,
        timestamp: i64,
        file_size: u64,
        checksum: u32, // CRC32 for quick change detection
        
        pub const EventType = enum {
            created,
            modified,
            deleted,
            moved,
        };
        
        pub fn deinit(self: *FileEvent, allocator: Allocator) void {
            allocator.free(self.path);
        }
    };
    
    /// Lock-free queue for file events
    const EventQueue = struct {
        events: []FileEvent,
        head: std.atomic.Atomic(u32),
        tail: std.atomic.Atomic(u32),
        capacity: u32,
        
        fn init(allocator: Allocator, capacity: u32) !EventQueue {
            const events = try allocator.alloc(FileEvent, capacity);
            return EventQueue{
                .events = events,
                .head = std.atomic.Atomic(u32).init(0),
                .tail = std.atomic.Atomic(u32).init(0),
                .capacity = capacity,
            };
        }
        
        fn deinit(self: *EventQueue, allocator: Allocator) void {
            allocator.free(self.events);
        }
        
        fn push(self: *EventQueue, event: FileEvent) bool {
            const tail = self.tail.load(.Acquire);
            const next_tail = (tail + 1) % self.capacity;
            
            if (next_tail == self.head.load(.Acquire)) {
                return false; // Queue full
            }
            
            self.events[tail] = event;
            self.tail.store(next_tail, .Release);
            return true;
        }
        
        fn pop(self: *EventQueue) ?FileEvent {
            const head = self.head.load(.Acquire);
            if (head == self.tail.load(.Acquire)) {
                return null; // Queue empty
            }
            
            const event = self.events[head];
            const next_head = (head + 1) % self.capacity;
            self.head.store(next_head, .Release);
            return event;
        }
    };
    
    const PlatformData = switch (@import("builtin").os.tag) {
        .linux => struct {
            inotify_fd: std.posix.fd_t,
            watch_descriptors: std.ArrayList(i32),
        },
        .macos => struct {
            kqueue_fd: std.posix.fd_t,
            watch_descriptors: std.ArrayList(i32),
        },
        .windows => struct {
            directory_handles: std.ArrayList(std.os.windows.HANDLE),
        },
        else => struct {},
    };
    
    pub fn init(allocator: Allocator, io: *IO, config: *const Config) !FileWatcher {
        std.log.info("Initializing FileWatcher for: {s}", .{config.watch_directory});
        
        // Open watch directory
        const watch_fd = try std.posix.open(
            config.watch_directory,
            std.posix.O.RDONLY,
            0
        );
        
        // Initialize event queue
        var event_queue = try EventQueue.init(allocator, 1024);
        
        // Platform-specific initialization
        var platform_data = try initPlatformData(allocator, config);
        
        return FileWatcher{
            .allocator = allocator,
            .io = io,
            .config = config,
            .watch_fd = watch_fd,
            .event_queue = event_queue,
            .running = false,
            .platform_data = platform_data,
        };
    }
    
    fn initPlatformData(allocator: Allocator, config: *const Config) !PlatformData {
        switch (@import("builtin").os.tag) {
            .linux => {
                const inotify_fd = try std.posix.inotify_init1(std.os.linux.IN.CLOEXEC);
                var watch_descriptors = std.ArrayList(i32).init(allocator);
                
                // Add watch for the directory
                const wd = try std.posix.inotify_add_watch(
                    inotify_fd,
                    config.watch_directory,
                    std.os.linux.IN.CREATE | std.os.linux.IN.MODIFY | 
                    std.os.linux.IN.DELETE | std.os.linux.IN.MOVE
                );
                try watch_descriptors.append(wd);
                
                return PlatformData{
                    .inotify_fd = inotify_fd,
                    .watch_descriptors = watch_descriptors,
                };
            },
            .macos => {
                const kqueue_fd = std.c.kqueue();
                if (kqueue_fd == -1) return error.KqueueInitFailed;
                
                var watch_descriptors = std.ArrayList(i32).init(allocator);
                try watch_descriptors.append(kqueue_fd);
                
                return PlatformData{
                    .kqueue_fd = kqueue_fd,
                    .watch_descriptors = watch_descriptors,
                };
            },
            .windows => {
                var directory_handles = std.ArrayList(std.os.windows.HANDLE).init(allocator);
                
                // TODO: Implement ReadDirectoryChangesW setup
                
                return PlatformData{
                    .directory_handles = directory_handles,
                };
            },
            else => return PlatformData{},
        }
    }
    
    pub fn deinit(self: *FileWatcher) void {
        std.log.info("Shutting down FileWatcher");
        
        self.running = false;
        std.posix.close(self.watch_fd);
        
        // Platform-specific cleanup
        switch (@import("builtin").os.tag) {
            .linux => {
                std.posix.close(self.platform_data.inotify_fd);
                self.platform_data.watch_descriptors.deinit();
            },
            .macos => {
                std.posix.close(self.platform_data.kqueue_fd);
                self.platform_data.watch_descriptors.deinit();
            },
            .windows => {
                for (self.platform_data.directory_handles.items) |handle| {
                    _ = std.os.windows.kernel32.CloseHandle(handle);
                }
                self.platform_data.directory_handles.deinit();
            },
            else => {},
        }
        
        self.event_queue.deinit(self.allocator);
    }
    
    pub fn start(self: *FileWatcher) !void {
        std.log.info("Starting FileWatcher");
        self.running = true;
        
        // Start async monitoring based on platform
        try self.startPlatformWatching();
    }
    
    fn startPlatformWatching(self: *FileWatcher) !void {
        switch (@import("builtin").os.tag) {
            .linux => try self.startInotifyWatching(),
            .macos => try self.startKqueueWatching(),
            .windows => try self.startWindowsWatching(),
            else => return error.UnsupportedPlatform,
        }
    }
    
    fn startInotifyWatching(self: *FileWatcher) !void {
        // Submit async read for inotify events
        var completion: IO.Completion = undefined;
        var buffer: [4096]u8 = undefined;
        
        self.io.read(
            *FileWatcher,
            self,
            inotifyCallback,
            &completion,
            self.platform_data.inotify_fd,
            &buffer,
            0
        );
    }
    
    fn inotifyCallback(
        self: *FileWatcher,
        completion: *IO.Completion,
        result: anyerror!usize,
    ) void {
        _ = completion;
        
        const bytes_read = result catch |err| {
            std.log.err("Inotify read error: {}", .{err});
            return;
        };
        
        if (bytes_read == 0) return;
        
        // Parse inotify events and queue them
        // TODO: Implement inotify event parsing
        std.log.debug("Received {d} bytes from inotify", .{bytes_read});
        
        // Continue monitoring
        if (self.running) {
            self.startInotifyWatching() catch |err| {
                std.log.err("Failed to restart inotify watching: {}", .{err});
            };
        }
    }
    
    fn startKqueueWatching(self: *FileWatcher) !void {
        // Setup kqueue to monitor directory
        var event = std.mem.zeroes(std.c.Kevent);
        event.ident = @intCast(self.watch_fd);
        event.filter = std.c.EVFILT_VNODE;
        event.flags = std.c.EV_ADD | std.c.EV_CLEAR;
        event.fflags = std.c.NOTE_WRITE | std.c.NOTE_DELETE | std.c.NOTE_RENAME;
        
        const result = std.c.kevent(
            self.platform_data.kqueue_fd,
            &event, 1,
            null, 0,
            null
        );
        
        if (result == -1) {
            return error.KqueueEventAddFailed;
        }
    }
    
    fn startWindowsWatching(self: *FileWatcher) !void {
        // TODO: Implement ReadDirectoryChangesW
        std.log.warn("Windows file watching not fully implemented");
    }
    
    pub fn tick(self: *FileWatcher) !void {
        if (!self.running) return;
        
        // Platform-specific event polling
        switch (@import("builtin").os.tag) {
            .macos => try self.pollKqueueEvents(),
            .windows => try self.pollWindowsEvents(),
            else => {}, // Linux uses async callbacks
        }
    }
    
    fn pollKqueueEvents(self: *FileWatcher) !void {
        var events: [16]std.c.Kevent = undefined;
        const timeout = std.c.timespec{ .tv_sec = 0, .tv_nsec = 1_000_000 }; // 1ms
        
        const num_events = std.c.kevent(
            self.platform_data.kqueue_fd,
            null, 0,
            &events, events.len,
            &timeout
        );
        
        if (num_events == -1) return;
        
        for (events[0..@intCast(num_events)]) |event| {
            if (event.filter == std.c.EVFILT_VNODE) {
                try self.handleDirectoryChange();
            }
        }
    }
    
    fn pollWindowsEvents(self: *FileWatcher) !void {
        // TODO: Poll ReadDirectoryChangesW results
    }
    
    fn handleDirectoryChange(self: *FileWatcher) !void {
        // Scan directory for changes
        var dir = std.fs.cwd().openDir(self.config.watch_directory, .{ .iterate = true }) catch |err| {
            std.log.err("Failed to open watch directory: {}", .{err});
            return;
        };
        defer dir.close();
        
        var iterator = dir.iterate();
        while (try iterator.next()) |entry| {
            if (entry.kind != .file) continue;
            
            // Check if file matches exclusion patterns
            if (self.isExcluded(entry.name)) continue;
            
            // Create file event
            const file_path = try self.allocator.dupe(u8, entry.name);
            const stat = try dir.statFile(entry.name);
            
            const event = FileEvent{
                .path = file_path,
                .event_type = .modified, // Simplified for now
                .timestamp = std.time.timestamp(),
                .file_size = stat.size,
                .checksum = try self.calculateChecksum(dir, entry.name),
            };
            
            if (!self.event_queue.push(event)) {
                std.log.warn("File event queue full, dropping event for: {s}", .{file_path});
                self.allocator.free(file_path);
            }
        }
    }
    
    fn isExcluded(self: *FileWatcher, filename: []const u8) bool {
        for (self.config.excluded_patterns) |pattern| {
            if (std.mem.indexOf(u8, filename, pattern) != null) {
                return true;
            }
        }
        return false;
    }
    
    fn calculateChecksum(self: *FileWatcher, dir: std.fs.Dir, filename: []const u8) !u32 {
        _ = self;
        
        const file = try dir.openFile(filename, .{});
        defer file.close();
        
        // Read first 1KB for quick checksum
        var buffer: [1024]u8 = undefined;
        const bytes_read = try file.readAll(&buffer);
        
        return std.hash.crc.Crc32.hash(buffer[0..bytes_read]);
    }
    
    /// Poll for file events (lock-free)
    pub fn pollFileEvent(self: *FileWatcher) ?FileEvent {
        return self.event_queue.pop();
    }
    
    /// Health check
    pub fn isHealthy(self: *const FileWatcher) bool {
        return self.running;
    }
};