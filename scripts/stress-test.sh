#!/bin/bash

# SyncMesh Stress Test Script
# Tests high-volume file operations to validate enterprise performance

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

print_header() {
    echo -e "${BLUE}[STRESS-TEST]${NC} $1"
}

print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_metric() {
    echo -e "${CYAN}[METRIC]${NC} $1"
}

# Configuration
TOTAL_FILES=1000
CONCURRENT_WORKERS=10
FILE_SIZE_KB=100
BURST_COUNT=50
TEST_DURATION=60

print_header "🚀 ENTERPRISE STRESS TEST STARTING"
echo "Configuration:"
echo "  - Total Files: $TOTAL_FILES"
echo "  - Concurrent Workers: $CONCURRENT_WORKERS"
echo "  - File Size: ${FILE_SIZE_KB}KB"
echo "  - Burst Operations: $BURST_COUNT"
echo "  - Test Duration: ${TEST_DURATION}s"
echo ""

# Cleanup function
cleanup() {
    print_status "Cleaning up test files..."
    docker exec -u root syncmesh-node1 sh -c "rm -rf /sync-data/stress-test-*" 2>/dev/null || true
    docker exec -u root syncmesh-node1 sh -c "rm -rf /sync-data/large-file-*" 2>/dev/null || true
    docker exec -u root syncmesh-node1 sh -c "rm -rf /sync-data/burst-*" 2>/dev/null || true
}

# Baseline metrics
get_metrics() {
    local prefix="$1"
    local sync_status=$(curl -s http://localhost:8080/api/v1/sync/status)
    local health_status=$(curl -s http://localhost:8080/api/v1/health)
    
    local total_files=$(echo "$sync_status" | jq -r '.total_files // 0')
    local synced_files=$(echo "$sync_status" | jq -r '.synced_files // 0')
    local pending_files=$(echo "$sync_status" | jq -r '.pending_files // 0')
    local queue_size=$(echo "$sync_status" | jq -r '.queue_size // 0')
    local events_queue=$(echo "$sync_status" | jq -r '.events_queue // 0')
    
    print_metric "$prefix - Total Files: $total_files, Synced: $synced_files, Pending: $pending_files, Queue: $queue_size, Events: $events_queue"
    
    # Return values for comparison
    echo "$total_files $synced_files $pending_files $queue_size $events_queue"
}

# Test 1: High-volume file creation
test_high_volume_creation() {
    print_header "TEST 1: High-Volume File Creation ($TOTAL_FILES files)"
    
    local start_time=$(date +%s)
    get_metrics "BEFORE"
    
    # Create files in parallel batches
    for ((batch=0; batch<$TOTAL_FILES; batch+=100)); do
        {
            for ((i=batch; i<batch+100 && i<$TOTAL_FILES; i++)); do
                docker exec -u root syncmesh-node1 sh -c "
                    echo 'Stress test file $i - $(date) - Content: $(head -c ${FILE_SIZE_KB}000 /dev/zero | tr '\0' 'A')' > /sync-data/stress-test-$i.txt
                " &
            done
            wait
        }
        
        if ((batch % 200 == 0)); then
            print_status "Created $((batch + 100)) files..."
            get_metrics "PROGRESS"
        fi
    done
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_status "File creation completed in ${duration}s"
    sleep 10 # Allow sync to catch up
    
    get_metrics "AFTER"
    
    # Calculate throughput
    local throughput=$((TOTAL_FILES / duration))
    print_metric "Creation Throughput: $throughput files/second"
}

# Test 2: Concurrent file modifications
test_concurrent_modifications() {
    print_header "TEST 2: Concurrent File Modifications"
    
    local start_time=$(date +%s)
    get_metrics "BEFORE"
    
    # Modify files concurrently
    for ((worker=0; worker<$CONCURRENT_WORKERS; worker++)); do
        {
            for ((i=0; i<100; i++)); do
                local file_id=$((worker * 100 + i))
                if [ $file_id -lt $TOTAL_FILES ]; then
                    docker exec -u root syncmesh-node1 sh -c "
                        echo 'MODIFIED - Worker $worker - Iteration $i - $(date)' >> /sync-data/stress-test-$file_id.txt
                        echo 'Additional content line 2' >> /sync-data/stress-test-$file_id.txt
                    " &
                fi
            done
            wait
        } &
    done
    wait
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_status "Concurrent modifications completed in ${duration}s"
    sleep 10
    
    get_metrics "AFTER"
    print_metric "Modification Rate: $((TOTAL_FILES / duration)) modifications/second"
}

# Test 3: Large file operations
test_large_files() {
    print_header "TEST 3: Large File Operations (10MB files)"
    
    local start_time=$(date +%s)
    get_metrics "BEFORE"
    
    # Create 10 large files (10MB each)
    for ((i=0; i<10; i++)); do
        {
            docker exec -u root syncmesh-node1 sh -c "
                head -c 10485760 /dev/zero | tr '\0' 'L' > /sync-data/large-file-$i.dat
                echo 'Large file $i metadata - $(date)' > /sync-data/large-file-$i.meta
            " &
        }
    done
    wait
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_status "Large file creation completed in ${duration}s"
    sleep 15 # Allow more time for large files
    
    get_metrics "AFTER"
    
    local total_mb=$((10 * 10))
    local throughput_mb=$((total_mb / duration))
    print_metric "Large File Throughput: ${throughput_mb}MB/second"
}

# Test 4: Burst operations
test_burst_operations() {
    print_header "TEST 4: Burst Operations ($BURST_COUNT rapid operations)"
    
    local start_time=$(date +%s)
    get_metrics "BEFORE"
    
    # Rapid burst of operations
    for ((i=0; i<$BURST_COUNT; i++)); do
        docker exec -u root syncmesh-node1 sh -c "
            echo 'Burst operation $i - $(date +%s.%N)' > /sync-data/burst-$i.txt
            mkdir -p /sync-data/burst-dir-$i
            echo 'Nested burst file' > /sync-data/burst-dir-$i/nested.txt
            echo 'Config: {\"id\": $i, \"timestamp\": \"$(date)\"}' > /sync-data/burst-$i.json
        " &
    done
    wait
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_status "Burst operations completed in ${duration}s"
    sleep 5
    
    get_metrics "AFTER"
    
    local burst_rate=$((BURST_COUNT * 4 / duration)) # 4 operations per iteration
    print_metric "Burst Rate: $burst_rate operations/second"
}

# Test 5: Mixed workload simulation
test_mixed_workload() {
    print_header "TEST 5: Mixed Workload Simulation (${TEST_DURATION}s)"
    
    local start_time=$(date +%s)
    get_metrics "BEFORE"
    
    local operations=0
    
    # Run mixed operations for specified duration
    while [ $(($(date +%s) - start_time)) -lt $TEST_DURATION ]; do
        {
            # Random operation type
            case $((RANDOM % 4)) in
                0) # Create file
                    docker exec -u root syncmesh-node1 sh -c "echo 'Mixed workload - Create - $(date +%s.%N)' > /sync-data/mixed-create-$operations.txt" &
                    ;;
                1) # Modify existing file
                    if [ $operations -gt 10 ]; then
                        local target=$((RANDOM % operations))
                        docker exec -u root syncmesh-node1 sh -c "echo 'MODIFIED - $(date)' >> /sync-data/mixed-create-$target.txt 2>/dev/null || true" &
                    fi
                    ;;
                2) # Create directory structure
                    docker exec -u root syncmesh-node1 sh -c "mkdir -p /sync-data/mixed-dir-$operations && echo 'Dir content' > /sync-data/mixed-dir-$operations/file.txt" &
                    ;;
                3) # Create JSON config
                    docker exec -u root syncmesh-node1 sh -c "echo '{\"operation\": $operations, \"type\": \"mixed\", \"timestamp\": \"$(date)\"}' > /sync-data/mixed-config-$operations.json" &
                    ;;
            esac
            
            operations=$((operations + 1))
            
            # Brief pause to avoid overwhelming
            sleep 0.1
        }
    done
    wait
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    print_status "Mixed workload completed: $operations operations in ${duration}s"
    sleep 10
    
    get_metrics "AFTER"
    
    local ops_per_sec=$((operations / duration))
    print_metric "Mixed Workload Rate: $ops_per_sec operations/second"
}

# Monitor system during stress
monitor_system() {
    print_header "Monitoring System Performance"
    
    # Docker stats
    echo "=== CONTAINER RESOURCE USAGE ==="
    docker stats --no-stream --format "table {{.Container}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}" | grep gluster
    
    echo -e "\n=== CLUSTER STATUS ==="
    curl -s http://localhost:8080/api/v1/cluster/status | jq '.'
    
    echo -e "\n=== ALL NODE HEALTH ==="
    for port in 8080 8180 8280; do
        echo "Node on port $port:"
        curl -s "http://localhost:$port/api/v1/health" | jq '.components.sync // "not available"'
    done
    
    echo -e "\n=== PROMETHEUS METRICS ==="
    curl -s http://localhost:9090/metrics | grep -E "(sync_operations_total|files_in_queue|go_routines)" | head -10
}

# Performance validation
validate_performance() {
    print_header "Performance Validation"
    
    local final_metrics=$(get_metrics "FINAL")
    read total synced pending queue events <<< "$final_metrics"
    
    echo "Final System State:"
    echo "  Total Files: $total"
    echo "  Synced Files: $synced"
    echo "  Pending Files: $pending"
    echo "  Queue Size: $queue"
    echo "  Events Queue: $events"
    
    # Performance criteria
    local sync_rate=0
    if [ "$total" -gt 0 ]; then
        sync_rate=$((synced * 100 / total))
    fi
    
    echo -e "\nPerformance Analysis:"
    echo "  Sync Success Rate: ${sync_rate}%"
    
    if [ "$sync_rate" -ge 95 ]; then
        print_status "✅ EXCELLENT: Sync rate >= 95%"
    elif [ "$sync_rate" -ge 85 ]; then
        print_warning "⚠️  GOOD: Sync rate >= 85%"
    else
        print_warning "❌ NEEDS IMPROVEMENT: Sync rate < 85%"
    fi
    
    if [ "$pending" -le 10 ]; then
        print_status "✅ EXCELLENT: Low pending queue"
    elif [ "$pending" -le 50 ]; then
        print_warning "⚠️  MODERATE: Pending queue manageable"
    else
        print_warning "❌ HIGH: Large pending queue"
    fi
    
    if [ "$queue" -le 20 ]; then
        print_status "✅ EXCELLENT: Low processing queue"
    else
        print_warning "⚠️  MODERATE: Processing queue building up"
    fi
}

# Main execution
main() {
    print_header "Starting Enterprise Stress Test Suite"
    
    # Cleanup any previous test files
    cleanup
    
    # Run stress tests
    test_high_volume_creation
    test_concurrent_modifications
    test_large_files
    test_burst_operations
    test_mixed_workload
    
    # Monitor system
    monitor_system
    
    # Validate performance
    validate_performance
    
    print_header "🎉 Stress Test Suite Completed!"
    
    # Cleanup
    read -p "Clean up test files? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        cleanup
        print_status "Test files cleaned up"
    fi
}

# Handle interrupts
trap cleanup EXIT

# Run main function
main "$@"