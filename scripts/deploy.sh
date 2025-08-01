#!/bin/bash

# SyncMesh Deployment Script
# This script demonstrates how to deploy and test the SyncMesh file sync cluster

set -e

echo "🚀 SyncMesh Deployment Script"
echo "===================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_header() {
    echo -e "${BLUE}[STEP]${NC} $1"
}

# Check prerequisites
check_prerequisites() {
    print_header "Checking prerequisites..."
    
    if ! command -v docker &> /dev/null; then
        print_error "Docker is not installed. Please install Docker first."
        exit 1
    fi
    
    if ! command -v docker-compose &> /dev/null; then
        print_error "Docker Compose is not installed. Please install Docker Compose first."
        exit 1
    fi
    
    if ! command -v go &> /dev/null; then
        print_error "Go is not installed. Please install Go 1.21+ first."
        exit 1
    fi
    
    print_status "All prerequisites are satisfied"
}

# Build the application
build_application() {
    print_header "Building application..."
    
    if [ ! -f "main.go" ]; then
        print_error "main.go not found. Please run this script from the project root."
        exit 1
    fi
    
    go mod tidy
    go build -o gluster-cluster ./main.go
    
    print_status "Application built successfully"
}

# Start the cluster
start_cluster() {
    print_header "Starting cluster..."
    
    # Stop any existing containers
    docker-compose down 2>/dev/null || true
    
    # Start the cluster
    docker-compose up -d
    
    print_status "Cluster is starting up..."
    
    # Wait for services to be ready
    print_status "Waiting for services to be ready (this may take a few minutes)..."
    sleep 10
    
    # Check if services are running
    if ! docker-compose ps | grep -q "Up"; then
        print_error "Some services failed to start. Check logs with: docker-compose logs"
        exit 1
    fi
    
    print_status "Cluster started successfully"
}

# Test the cluster
test_cluster() {
    print_header "Testing cluster functionality..."
    
    # Wait a bit more for full startup
    sleep 15
    
    # Test health endpoints
    echo "Testing health endpoints..."
    
    for port in 8080 8180 8280; do
        echo "Testing node on port $port..."
        if curl -s "http://localhost:$port/api/v1/health" | grep -q "healthy"; then
            print_status "Node on port $port is healthy"
        else
            print_warning "Node on port $port may not be ready yet"
        fi
    done
    
    # Test cluster status
    echo ""
    echo "Cluster status:"
    curl -s "http://localhost:8080/api/v1/cluster/status" | jq '.' 2>/dev/null || curl -s "http://localhost:8080/api/v1/cluster/status"
    
    # Test sync status
    echo ""
    echo "Sync status:"
    curl -s "http://localhost:8080/api/v1/sync/status" | jq '.' 2>/dev/null || curl -s "http://localhost:8080/api/v1/sync/status"
    
    print_status "Cluster testing completed"
}

# Show cluster information
show_cluster_info() {
    print_header "Cluster Information"
    
    echo ""
    echo "🌐 Web Endpoints:"
    echo "  Node 1: http://localhost:8080"
    echo "  Node 2: http://localhost:8180"
    echo "  Node 3: http://localhost:8280"
    echo ""
    echo "📊 Monitoring:"
    echo "  Prometheus: http://localhost:9091"
    echo "  Grafana: http://localhost:3000 (admin/admin)"
    echo ""
    echo "🔍 API Endpoints:"
    echo "  Health: http://localhost:8080/api/v1/health"
    echo "  Cluster Status: http://localhost:8080/api/v1/cluster/status"
    echo "  Sync Status: http://localhost:8080/api/v1/sync/status"
    echo "  Metrics: http://localhost:9090/metrics"
    echo ""
    echo "📝 Useful Commands:"
    echo "  View logs: docker-compose logs -f"
    echo "  Stop cluster: docker-compose down"
    echo "  Restart cluster: docker-compose restart"
    echo "  View running containers: docker-compose ps"
    echo ""
}

# Create test files
create_test_files() {
    print_header "Creating test files..."
    
    # Create test files in the shared volume
    docker exec gluster-node1 sh -c 'echo "Test file 1 - $(date)" > /sync-data/test1.txt'
    docker exec gluster-node1 sh -c 'echo "Test file 2 - $(date)" > /sync-data/test2.txt'
    docker exec gluster-node1 sh -c 'mkdir -p /sync-data/subdir && echo "Subdirectory file - $(date)" > /sync-data/subdir/test3.txt'
    
    print_status "Test files created in /sync-data/"
    
    # List files
    echo "Files in sync volume:"
    docker exec gluster-node1 find /sync-data -type f -exec ls -la {} \;
}

# Main execution
main() {
    case "${1:-all}" in
        "prereq")
            check_prerequisites
            ;;
        "build")
            check_prerequisites
            build_application
            ;;
        "start")
            start_cluster
            ;;
        "test")
            test_cluster
            ;;
        "info")
            show_cluster_info
            ;;
        "files")
            create_test_files
            ;;
        "stop")
            print_header "Stopping cluster..."
            docker-compose down
            print_status "Cluster stopped"
            ;;
        "clean")
            print_header "Cleaning up..."
            docker-compose down -v
            docker system prune -f
            rm -f gluster-cluster
            print_status "Cleanup completed"
            ;;
        "all"|*)
            check_prerequisites
            build_application
            start_cluster
            test_cluster
            create_test_files
            show_cluster_info
            ;;
    esac
}

# Help message
if [[ "${1}" == "-h" || "${1}" == "--help" ]]; then
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  all      - Run complete deployment (default)"
    echo "  prereq   - Check prerequisites only"
    echo "  build    - Build application only"
    echo "  start    - Start cluster only"
    echo "  test     - Test cluster only"
    echo "  info     - Show cluster information"
    echo "  files    - Create test files"
    echo "  stop     - Stop cluster"
    echo "  clean    - Stop cluster and clean up"
    echo ""
    echo "Examples:"
    echo "  $0           # Full deployment"
    echo "  $0 start     # Start cluster"
    echo "  $0 test      # Test cluster"
    echo "  $0 stop      # Stop cluster"
    echo "  $0 clean     # Clean up everything"
    exit 0
fi

# Run main function
main "$1"

print_status "Script completed successfully! 🎉"