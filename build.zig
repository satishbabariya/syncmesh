const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // Main SyncMesh executable
    const exe = b.addExecutable(.{
        .name = "syncmesh",
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
    });

    // Add system dependencies for async I/O
    if (target.result.os.tag == .linux) {
        exe.linkLibC();
    } else if (target.result.os.tag == .macos) {
        exe.linkLibC();
    } else if (target.result.os.tag == .windows) {
        exe.linkLibC();
    }

    b.installArtifact(exe);

    // Run command
    const run_cmd = b.addRunArtifact(exe);
    run_cmd.step.dependOn(b.getInstallStep());
    if (b.args) |args| {
        run_cmd.addArgs(args);
    }

    const run_step = b.step("run", "Run SyncMesh");
    run_step.dependOn(&run_cmd.step);

    // Tests
    const unit_tests = b.addTest(.{
        .root_source_file = b.path("src/test.zig"),
        .target = target,
        .optimize = optimize,
    });

    const run_unit_tests = b.addRunArtifact(unit_tests);
    const test_step = b.step("test", "Run unit tests");
    test_step.dependOn(&run_unit_tests.step);

    // Benchmarks
    const bench_exe = b.addExecutable(.{
        .name = "syncmesh-bench",
        .root_source_file = b.path("src/bench.zig"),
        .target = target,
        .optimize = optimize,
    });

    b.installArtifact(bench_exe);

    const bench_run = b.addRunArtifact(bench_exe);
    const bench_step = b.step("bench", "Run benchmarks");
    bench_step.dependOn(&bench_run.step);

    // Minimal test
    const minimal_test = b.addExecutable(.{
        .name = "minimal-test",
        .root_source_file = b.path("src/minimal_test.zig"),
        .target = target,
        .optimize = optimize,
    });

    b.installArtifact(minimal_test);

    const minimal_run = b.addRunArtifact(minimal_test);
    const minimal_step = b.step("minimal", "Run minimal architecture test");
    minimal_step.dependOn(&minimal_run.step);

    // Performance test
    const performance_test = b.addExecutable(.{
        .name = "performance-test",
        .root_source_file = b.path("src/performance_test.zig"),
        .target = target,
        .optimize = optimize,
    });

    b.installArtifact(performance_test);

    const performance_run = b.addRunArtifact(performance_test);
    const performance_step = b.step("perf", "Run performance tests");
    performance_step.dependOn(&performance_run.step);

    // CLI test
    const cli_test = b.addExecutable(.{
        .name = "syncmesh-cli",
        .root_source_file = b.path("src/cli_test.zig"),
        .target = target,
        .optimize = optimize,
    });

    b.installArtifact(cli_test);

    const cli_run = b.addRunArtifact(cli_test);
    const cli_step = b.step("cli", "Run CLI test");
    cli_step.dependOn(&cli_run.step);
}