package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/sirupsen/logrus"
)

// Service provides monitoring, metrics, and health checking capabilities
type Service struct {
	config *config.MonitoringConfig
	logger *logrus.Entry

	// HTTP server for metrics and health endpoints
	server *http.Server

	// Metrics
	registry *prometheus.Registry
	metrics  *Metrics

	// Health checking
	healthChecks map[string]HealthCheck
	healthMutex  sync.RWMutex

	// Statistics
	stats *Statistics

	// Lifecycle management
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	runningMu sync.RWMutex
}

// HealthCheck represents a health check function
type HealthCheck func() HealthStatus

// HealthStatus represents the status of a health check
type HealthStatus struct {
	Name      string        `json:"name"`
	Status    string        `json:"status"` // healthy, unhealthy, unknown
	Message   string        `json:"message,omitempty"`
	Details   interface{}   `json:"details,omitempty"`
	LastCheck time.Time     `json:"last_check"`
	Duration  time.Duration `json:"duration"`
}

// Statistics holds various runtime statistics
type Statistics struct {
	StartTime        time.Time
	TotalRequests    int64
	SuccessfulSyncs  int64
	FailedSyncs      int64
	FilesProcessed   int64
	BytesTransferred int64
	ErrorCount       int64
}

// Metrics holds Prometheus metrics
type Metrics struct {
	// HTTP metrics
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	// Sync metrics
	SyncOperationsTotal *prometheus.CounterVec
	SyncDuration        *prometheus.HistogramVec
	FilesInQueue        prometheus.Gauge
	ActiveConnections   prometheus.Gauge

	// System metrics
	GoRoutines  prometheus.Gauge
	MemoryUsage prometheus.Gauge
	CPUUsage    prometheus.Gauge

	// Cluster metrics
	ClusterNodes  prometheus.Gauge
	ClusterLeader prometheus.Gauge

	// Error metrics
	ErrorsTotal *prometheus.CounterVec
}

// NewService creates a new monitoring service
func NewService(config *config.MonitoringConfig) (*Service, error) {
	logger := logger.NewForComponent("monitoring")

	service := &Service{
		config:       config,
		logger:       logger,
		registry:     prometheus.NewRegistry(),
		healthChecks: make(map[string]HealthCheck),
		stats: &Statistics{
			StartTime: time.Now(),
		},
	}

	// Initialize metrics
	service.initializeMetrics()

	// Register default health checks
	service.registerDefaultHealthChecks()

	return service, nil
}

// Start starts the monitoring service
func (s *Service) Start(ctx context.Context) error {
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		return fmt.Errorf("monitoring service is already running")
	}
	s.running = true
	s.runningMu.Unlock()

	s.ctx, s.cancel = context.WithCancel(ctx)

	if !s.config.Enabled {
		s.logger.Info("Monitoring disabled, skipping start")
		return nil
	}

	s.logger.Info("Starting monitoring service")

	// Start HTTP server
	if err := s.startHTTPServer(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Start background workers
	s.startWorkers()

	s.logger.WithField("port", s.config.MetricsPort).Info("Monitoring service started")
	return nil
}

// Stop stops the monitoring service
func (s *Service) Stop() {
	s.runningMu.Lock()
	if !s.running {
		s.runningMu.Unlock()
		return
	}
	s.running = false
	s.runningMu.Unlock()

	s.logger.Info("Stopping monitoring service")

	if s.cancel != nil {
		s.cancel()
	}

	// Stop HTTP server
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	}

	// Wait for workers to finish
	s.wg.Wait()

	s.logger.Info("Monitoring service stopped")
}

// initializeMetrics initializes Prometheus metrics
func (s *Service) initializeMetrics() {
	s.metrics = &Metrics{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "endpoint", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "endpoint"},
		),
		SyncOperationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "sync_operations_total",
				Help: "Total number of sync operations",
			},
			[]string{"operation", "status"},
		),
		SyncDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "sync_duration_seconds",
				Help:    "Sync operation duration in seconds",
				Buckets: []float64{0.1, 0.5, 1.0, 2.5, 5.0, 10.0},
			},
			[]string{"operation"},
		),
		FilesInQueue: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "files_in_queue",
				Help: "Number of files waiting to be synced",
			},
		),
		ActiveConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "active_connections",
				Help: "Number of active connections",
			},
		),
		GoRoutines: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "go_routines",
				Help: "Number of goroutines",
			},
		),
		MemoryUsage: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "memory_usage_bytes",
				Help: "Memory usage in bytes",
			},
		),
		CPUUsage: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "cpu_usage_percent",
				Help: "CPU usage percentage",
			},
		),
		ClusterNodes: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "cluster_nodes",
				Help: "Number of nodes in cluster",
			},
		),
		ClusterLeader: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "cluster_leader",
				Help: "Whether this node is the cluster leader (1) or not (0)",
			},
		),
		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "errors_total",
				Help: "Total number of errors",
			},
			[]string{"type", "component"},
		),
	}

	// Register metrics
	s.registry.MustRegister(
		s.metrics.HTTPRequestsTotal,
		s.metrics.HTTPRequestDuration,
		s.metrics.SyncOperationsTotal,
		s.metrics.SyncDuration,
		s.metrics.FilesInQueue,
		s.metrics.ActiveConnections,
		s.metrics.GoRoutines,
		s.metrics.MemoryUsage,
		s.metrics.CPUUsage,
		s.metrics.ClusterNodes,
		s.metrics.ClusterLeader,
		s.metrics.ErrorsTotal,
	)
}

// startHTTPServer starts the HTTP server for metrics and health endpoints
func (s *Service) startHTTPServer() error {
	mux := http.NewServeMux()

	// Metrics endpoint
	if s.config.PrometheusEnabled {
		mux.Handle(s.config.MetricsPath, promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))
	}

	// Health endpoint
	mux.HandleFunc(s.config.HealthPath, s.healthHandler)

	// Statistics endpoint
	mux.HandleFunc("/stats", s.statsHandler)

	// Info endpoint
	mux.HandleFunc("/info", s.infoHandler)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.MetricsPort),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.WithError(err).Error("HTTP server failed")
		}
	}()

	return nil
}

// startWorkers starts background monitoring workers
func (s *Service) startWorkers() {
	// System metrics collector
	s.wg.Add(1)
	go s.systemMetricsWorker()

	// Health check worker
	s.wg.Add(1)
	go s.healthCheckWorker()
}

// systemMetricsWorker collects system metrics
func (s *Service) systemMetricsWorker() {
	defer s.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.collectSystemMetrics()
		}
	}
}

// healthCheckWorker runs periodic health checks
func (s *Service) healthCheckWorker() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runHealthChecks()
		}
	}
}

// collectSystemMetrics collects system-level metrics
func (s *Service) collectSystemMetrics() {
	// Goroutines
	s.metrics.GoRoutines.Set(float64(runtime.NumGoroutine()))

	// Memory usage
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.metrics.MemoryUsage.Set(float64(m.Alloc))

	// CPU usage would require additional libraries in real implementation
	// s.metrics.CPUUsage.Set(cpuPercent)
}

// healthHandler handles health check requests
func (s *Service) healthHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		s.metrics.HTTPRequestsTotal.WithLabelValues(r.Method, "/health", "200").Inc()
		s.metrics.HTTPRequestDuration.WithLabelValues(r.Method, "/health").Observe(duration.Seconds())
	}()

	statuses := s.getHealthStatuses()

	overall := "healthy"
	for _, status := range statuses {
		if status.Status == "unhealthy" {
			overall = "unhealthy"
			break
		}
	}

	response := map[string]interface{}{
		"status":    overall,
		"timestamp": time.Now().UTC(),
		"uptime":    time.Since(s.stats.StartTime).String(),
		"checks":    statuses,
	}

	w.Header().Set("Content-Type", "application/json")
	if overall == "unhealthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	json.NewEncoder(w).Encode(response)
}

// statsHandler handles statistics requests
func (s *Service) statsHandler(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"start_time":        s.stats.StartTime,
		"uptime":            time.Since(s.stats.StartTime).String(),
		"total_requests":    atomic.LoadInt64(&s.stats.TotalRequests),
		"successful_syncs":  atomic.LoadInt64(&s.stats.SuccessfulSyncs),
		"failed_syncs":      atomic.LoadInt64(&s.stats.FailedSyncs),
		"files_processed":   atomic.LoadInt64(&s.stats.FilesProcessed),
		"bytes_transferred": atomic.LoadInt64(&s.stats.BytesTransferred),
		"error_count":       atomic.LoadInt64(&s.stats.ErrorCount),
		"goroutines":        runtime.NumGoroutine(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// infoHandler handles info requests
func (s *Service) infoHandler(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"service":    "gluster-cluster",
		"version":    "1.0.0",
		"go_version": runtime.Version(),
		"build_time": "unknown", // Would be set during build
		"git_commit": "unknown", // Would be set during build
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// registerDefaultHealthChecks registers default health checks
func (s *Service) registerDefaultHealthChecks() {
	s.RegisterHealthCheck("memory", s.memoryHealthCheck)
	s.RegisterHealthCheck("goroutines", s.goroutineHealthCheck)
}

// RegisterHealthCheck registers a new health check
func (s *Service) RegisterHealthCheck(name string, check HealthCheck) {
	s.healthMutex.Lock()
	defer s.healthMutex.Unlock()
	s.healthChecks[name] = check
}

// runHealthChecks runs all registered health checks
func (s *Service) runHealthChecks() {
	s.healthMutex.RLock()
	checks := make(map[string]HealthCheck)
	for name, check := range s.healthChecks {
		checks[name] = check
	}
	s.healthMutex.RUnlock()

	for name, check := range checks {
		go func(name string, check HealthCheck) {
			start := time.Now()
			status := check()
			status.Name = name
			status.LastCheck = start
			status.Duration = time.Since(start)

			// Log unhealthy status
			if status.Status == "unhealthy" {
				s.logger.WithFields(logrus.Fields{
					"check":   name,
					"message": status.Message,
				}).Warn("Health check failed")
			}
		}(name, check)
	}
}

// getHealthStatuses returns current health check statuses
func (s *Service) getHealthStatuses() []HealthStatus {
	s.healthMutex.RLock()
	defer s.healthMutex.RUnlock()

	statuses := make([]HealthStatus, 0, len(s.healthChecks))
	for name, check := range s.healthChecks {
		start := time.Now()
		status := check()
		status.Name = name
		status.LastCheck = start
		status.Duration = time.Since(start)
		statuses = append(statuses, status)
	}

	return statuses
}

// memoryHealthCheck checks memory usage
func (s *Service) memoryHealthCheck() HealthStatus {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Consider unhealthy if using more than 1GB
	const maxMemory = 1024 * 1024 * 1024

	status := "healthy"
	message := fmt.Sprintf("Memory usage: %d MB", m.Alloc/1024/1024)

	if m.Alloc > maxMemory {
		status = "unhealthy"
		message = fmt.Sprintf("High memory usage: %d MB", m.Alloc/1024/1024)
	}

	return HealthStatus{
		Status:  status,
		Message: message,
		Details: map[string]interface{}{
			"alloc_bytes":       m.Alloc,
			"total_alloc_bytes": m.TotalAlloc,
			"sys_bytes":         m.Sys,
			"num_gc":            m.NumGC,
		},
	}
}

// goroutineHealthCheck checks goroutine count
func (s *Service) goroutineHealthCheck() HealthStatus {
	count := runtime.NumGoroutine()

	// Consider unhealthy if more than 1000 goroutines
	const maxGoroutines = 1000

	status := "healthy"
	message := fmt.Sprintf("Goroutines: %d", count)

	if count > maxGoroutines {
		status = "unhealthy"
		message = fmt.Sprintf("High goroutine count: %d", count)
	}

	return HealthStatus{
		Status:  status,
		Message: message,
		Details: map[string]interface{}{
			"count": count,
		},
	}
}

// RecordHTTPRequest records HTTP request metrics
func (s *Service) RecordHTTPRequest(method, endpoint, status string, duration time.Duration) {
	s.metrics.HTTPRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
	s.metrics.HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
	atomic.AddInt64(&s.stats.TotalRequests, 1)
}

// RecordSyncOperation records sync operation metrics
func (s *Service) RecordSyncOperation(operation, status string, duration time.Duration) {
	s.metrics.SyncOperationsTotal.WithLabelValues(operation, status).Inc()
	s.metrics.SyncDuration.WithLabelValues(operation).Observe(duration.Seconds())

	if status == "success" {
		atomic.AddInt64(&s.stats.SuccessfulSyncs, 1)
	} else {
		atomic.AddInt64(&s.stats.FailedSyncs, 1)
	}
}

// RecordError records error metrics
func (s *Service) RecordError(errorType, component string) {
	s.metrics.ErrorsTotal.WithLabelValues(errorType, component).Inc()
	atomic.AddInt64(&s.stats.ErrorCount, 1)
}

// UpdateQueueSize updates the files in queue metric
func (s *Service) UpdateQueueSize(size int) {
	s.metrics.FilesInQueue.Set(float64(size))
}

// UpdateActiveConnections updates the active connections metric
func (s *Service) UpdateActiveConnections(count int) {
	s.metrics.ActiveConnections.Set(float64(count))
}

// UpdateClusterMetrics updates cluster-related metrics
func (s *Service) UpdateClusterMetrics(nodeCount int, isLeader bool) {
	s.metrics.ClusterNodes.Set(float64(nodeCount))

	leaderValue := 0.0
	if isLeader {
		leaderValue = 1.0
	}
	s.metrics.ClusterLeader.Set(leaderValue)
}
