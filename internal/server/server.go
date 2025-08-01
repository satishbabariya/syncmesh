package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/satishbabariya/syncmesh/internal/cluster"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/docker"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/satishbabariya/syncmesh/internal/monitoring"
	syncengine "github.com/satishbabariya/syncmesh/internal/sync"
	"github.com/satishbabariya/syncmesh/pkg/api"
	"github.com/satishbabariya/syncmesh/pkg/grpc"
	"github.com/sirupsen/logrus"
)

// Server represents the main application server
type Server struct {
	config *config.Config
	logger *logrus.Entry

	// Core components
	syncEngine     *syncengine.Engine
	clusterManager *cluster.Manager
	dockerClient   *docker.Client
	monitoring     *monitoring.Service

	// Network components
	httpServer *http.Server
	grpcServer *grpc.Server

	// Lifecycle management
	wg       sync.WaitGroup
	shutdown chan struct{}
	mu       sync.RWMutex
	running  bool
}

// New creates a new server instance
func New(cfg *config.Config) (*Server, error) {
	log := logger.NewForComponent("server")

	s := &Server{
		config:   cfg,
		logger:   log,
		shutdown: make(chan struct{}),
	}

	// Initialize components
	if err := s.initializeComponents(); err != nil {
		return nil, fmt.Errorf("failed to initialize components: %w", err)
	}

	return s, nil
}

// Start starts the server and all its components
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server is already running")
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("Starting gluster-cluster server")

	// Start components in order
	if err := s.startComponents(ctx); err != nil {
		return fmt.Errorf("failed to start components: %w", err)
	}

	// Start network services
	if err := s.startNetworkServices(); err != nil {
		return fmt.Errorf("failed to start network services: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"node_id":   s.config.NodeID,
		"http_port": s.config.Server.Port,
		"grpc_port": s.config.Server.GRPCPort,
	}).Info("Server started successfully")

	// Wait for shutdown signal
	select {
	case <-ctx.Done():
		s.logger.Info("Context cancelled, shutting down")
	case <-s.shutdown:
		s.logger.Info("Shutdown signal received")
	}

	return s.Stop()
}

// Stop gracefully stops the server
func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	s.logger.Info("Stopping server")

	// Close shutdown channel
	close(s.shutdown)

	// Stop network services
	s.stopNetworkServices()

	// Stop components
	s.stopComponents()

	// Wait for all goroutines to finish
	s.wg.Wait()

	s.logger.Info("Server stopped successfully")
	return nil
}

// initializeComponents initializes all server components
func (s *Server) initializeComponents() error {
	var err error

	// Initialize Docker client
	s.dockerClient, err = docker.NewClient(s.config.Docker)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Initialize cluster manager
	s.clusterManager, err = cluster.NewManager(&s.config.Cluster, s.config.NodeID)
	if err != nil {
		return fmt.Errorf("failed to create cluster manager: %w", err)
	}

	// Initialize sync engine
	s.syncEngine, err = syncengine.NewEngine(&s.config.Sync, s.dockerClient, s.clusterManager)
	if err != nil {
		return fmt.Errorf("failed to create sync engine: %w", err)
	}

	// Initialize monitoring service
	s.monitoring, err = monitoring.NewService(&s.config.Monitoring)
	if err != nil {
		return fmt.Errorf("failed to create monitoring service: %w", err)
	}

	return nil
}

// startComponents starts all server components
func (s *Server) startComponents(ctx context.Context) error {
	// Start cluster manager
	if err := s.clusterManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start cluster manager: %w", err)
	}

	// Start sync engine
	if err := s.syncEngine.Start(ctx); err != nil {
		return fmt.Errorf("failed to start sync engine: %w", err)
	}

	// Start monitoring service
	if err := s.monitoring.Start(ctx); err != nil {
		return fmt.Errorf("failed to start monitoring service: %w", err)
	}

	return nil
}

// startNetworkServices starts HTTP and gRPC servers
func (s *Server) startNetworkServices() error {
	// Start HTTP server
	httpMux := api.NewHTTPHandler(s.syncEngine, s.clusterManager, s.monitoring)
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port),
		Handler:      httpMux,
		ReadTimeout:  s.config.Server.ReadTimeout,
		WriteTimeout: s.config.Server.WriteTimeout,
		IdleTimeout:  s.config.Server.IdleTimeout,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.logger.WithField("addr", s.httpServer.Addr).Info("Starting HTTP server")
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.WithError(err).Error("HTTP server failed")
		}
	}()

	// Start gRPC server
	var err error
	s.grpcServer, err = grpc.NewServer(s.config, s.syncEngine, s.clusterManager)
	if err != nil {
		return fmt.Errorf("failed to create gRPC server: %w", err)
	}

	grpcAddr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.GRPCPort)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on gRPC port: %w", err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.logger.WithField("addr", grpcAddr).Info("Starting gRPC server")
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.WithError(err).Error("gRPC server failed")
		}
	}()

	return nil
}

// stopNetworkServices stops HTTP and gRPC servers
func (s *Server) stopNetworkServices() {
	// Stop HTTP server
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.logger.WithError(err).Error("Failed to gracefully shutdown HTTP server")
		}
	}

	// Stop gRPC server
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
}

// stopComponents stops all server components
func (s *Server) stopComponents() {
	if s.monitoring != nil {
		s.monitoring.Stop()
	}

	if s.syncEngine != nil {
		s.syncEngine.Stop()
	}

	if s.clusterManager != nil {
		s.clusterManager.Stop()
	}

	if s.dockerClient != nil {
		s.dockerClient.Close()
	}
}

// Health returns the health status of the server
func (s *Server) Health() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	health := map[string]interface{}{
		"status":    "healthy",
		"node_id":   s.config.NodeID,
		"running":   s.running,
		"timestamp": time.Now().UTC(),
	}

	// Add component health status
	if s.clusterManager != nil {
		health["cluster"] = s.clusterManager.Health()
	}

	if s.syncEngine != nil {
		health["sync"] = s.syncEngine.Health()
	}

	if s.dockerClient != nil {
		health["docker"] = s.dockerClient.Health()
	}

	return health
}
