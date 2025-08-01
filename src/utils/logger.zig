const std = @import("std");
const Allocator = std.mem.Allocator;

/// High-performance logger for SyncMesh
/// Thread-safe logging with minimal allocation overhead
pub const Logger = struct {
    var initialized: bool = false;
    var log_level: std.log.Level = .info;
    var allocator: ?Allocator = null;
    
    /// Initialize the logger
    pub fn init(alloc: Allocator, level: std.log.Level) !void {
        allocator = alloc;
        log_level = level;
        initialized = true;
        
        std.log.info("Logger initialized with level: {s}", .{@tagName(level)});
    }
    
    /// Cleanup logger resources
    pub fn deinit() void {
        initialized = false;
        allocator = null;
    }
    
    /// Custom log function for std.log
    pub fn log(
        comptime level: std.log.Level,
        comptime scope: @TypeOf(.EnumLiteral),
        comptime format: []const u8,
        args: anytype,
    ) void {
        if (!initialized) return;
        if (@intFromEnum(level) < @intFromEnum(log_level)) return;
        
        // Get current timestamp
        const timestamp = std.time.timestamp();
        const time_info = std.time.epoch.EpochSeconds{ .secs = @intCast(timestamp) };
        const day_seconds = time_info.getDaySeconds();
        const hours = day_seconds.getHoursIntoDay();
        const minutes = day_seconds.getMinutesIntoHour();
        const seconds = day_seconds.getSecondsIntoMinute();
        
        // Format: [TIMESTAMP] [LEVEL] [SCOPE] MESSAGE
        const level_str = switch (level) {
            .err => "ERROR",
            .warn => "WARN ",
            .info => "INFO ",
            .debug => "DEBUG",
        };
        
        const scope_str = @tagName(scope);
        
        // Thread-safe output to stderr
        const stderr = std.io.getStdErr().writer();
        std.debug.lockStdErr();
        defer std.debug.unlockStdErr();
        
        stderr.print("[{:02d}:{:02d}:{:02d}] [{s}] [{s}] ", .{
            hours, minutes, seconds, level_str, scope_str
        }) catch return;
        
        stderr.print(format, args) catch return;
        stderr.print("\n") catch return;
    }
    
    /// Structured logging for performance metrics
    pub fn logMetric(metric_name: []const u8, value: f64, unit: []const u8) void {
        if (!initialized) return;
        if (@intFromEnum(std.log.Level.info) < @intFromEnum(log_level)) return;
        
        std.log.info("METRIC {s}={d:.2}{s}", .{ metric_name, value, unit });
    }
    
    /// Log with additional context
    pub fn logWithContext(
        comptime level: std.log.Level,
        context: []const u8,
        comptime format: []const u8,
        args: anytype,
    ) void {
        if (!initialized) return;
        if (@intFromEnum(level) < @intFromEnum(log_level)) return;
        
        log(level, .syncmesh, "[{s}] " ++ format, .{context} ++ args);
    }
    
    /// Performance-critical logging that avoids allocations
    pub fn logFast(comptime level: std.log.Level, comptime message: []const u8) void {
        if (!initialized) return;
        if (@intFromEnum(level) < @intFromEnum(log_level)) return;
        
        log(level, .syncmesh, message, .{});
    }
};