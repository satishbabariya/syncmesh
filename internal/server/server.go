package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/docker"
	"github.com/satishbabariya/syncmesh/internal/monitoring"
	"github.com/satishbabariya/syncmesh/internal/p2p"
	"github.com/satishbabariya/syncmesh/internal/sync"
	"github.com/satishbabariya/syncmesh/pkg/api"
	"github.com/satishbabariya/syncmesh/pkg/grpc"
	"github.com/sirupsen/logrus"
)

// Server represents the main application server
type Server struct {
	config       *config.Config
	dockerClient *docker.Client
	p2pNode      *p2p.P2PNode
	syncEngine   *sync.Engine
	monitoring   *monitoring.Service
	httpServer   *http.Server
	grpcServer   *grpc.Server
	logger       *logrus.Entry
}

// New creates a new server instance
func New(config *config.Config) *Server {
	// Create Docker client
	dockerClient, err := docker.NewClient(config.Docker)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create Docker client")
	}

	// Create monitoring service
	monitoringService, err := monitoring.NewService(&config.Monitoring)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create monitoring service")
	}

	// Create P2P node
	p2pNode := p2p.NewP2PNode(&config.P2P, dockerClient)

	// Create sync engine
	syncConfig := &sync.Config{
		WatchedPaths:       config.Sync.WatchedPaths,
		ExcludePatterns:    config.Sync.ExcludePatterns,
		IncludePatterns:    config.Sync.IncludePatterns,
		SyncInterval:       config.Sync.Interval,
		BatchSize:          config.Sync.BatchSize,
		MaxRetries:         config.Sync.MaxRetries,
		RetryBackoff:       config.Sync.RetryBackoff,
		ConflictResolution: config.Sync.ConflictResolution,
		ChecksumAlgorithm:  config.Sync.ChecksumAlgorithm,
		CompressionEnabled: config.Sync.CompressionEnabled,
		CompressionLevel:   config.Sync.CompressionLevel,
	}
	syncEngine := sync.NewEngine(syncConfig, p2pNode)

	server := &Server{
		config:       config,
		dockerClient: dockerClient,
		p2pNode:      p2pNode,
		syncEngine:   syncEngine,
		monitoring:   monitoringService,
		httpServer:   nil,
		grpcServer:   nil,
		logger:       logrus.NewEntry(logrus.New()),
	}

	return server
}

// Start starts the server and all its components
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("Starting SyncMesh P2P server")

	// Start components in order
	if err := s.startComponents(); err != nil {
		return fmt.Errorf("failed to start components: %w", err)
	}

	// Start network services
	if err := s.startNetworkServices(); err != nil {
		return fmt.Errorf("failed to start network services: %w", err)
	}

	s.logger.WithFields(logrus.Fields{
		"http_port": s.config.Server.Port,
		"grpc_port": s.config.Server.GRPCPort,
		"p2p_port":  s.config.P2P.Port,
	}).Info("Server started successfully")

	// Wait for context cancellation
	<-ctx.Done()
	s.logger.Info("Context cancelled, shutting down")

	return s.Stop()
}

// Stop gracefully stops the server
func (s *Server) Stop() error {
	s.logger.Info("Stopping server")

	// Stop network services
	s.stopNetworkServices()

	// Stop components
	s.stopComponents()

	s.logger.Info("Server stopped successfully")
	return nil
}

// startComponents starts all server components
func (s *Server) startComponents() error {
	// Start P2P node
	if err := s.p2pNode.Start(); err != nil {
		return fmt.Errorf("failed to start P2P node: %w", err)
	}

	// Start sync engine
	if err := s.syncEngine.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start sync engine: %w", err)
	}

	// Start monitoring service
	if err := s.monitoring.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start monitoring service: %w", err)
	}

	return nil
}

// startNetworkServices starts HTTP and gRPC servers
func (s *Server) startNetworkServices() error {
	// Start HTTP server
	httpMux := api.NewHTTPHandler(s.p2pNode, s.monitoring)
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port),
		Handler:      httpMux,
		ReadTimeout:  s.config.Server.ReadTimeout,
		WriteTimeout: s.config.Server.WriteTimeout,
		IdleTimeout:  s.config.Server.IdleTimeout,
	}

	// Start gRPC server
	var err error
	s.grpcServer, err = grpc.NewServer(s.config, s.p2pNode, s.monitoring)
	if err != nil {
		return fmt.Errorf("failed to create gRPC server: %w", err)
	}

	// Start HTTP server
	go func() {
		s.logger.WithField("address", s.httpServer.Addr).Info("Starting HTTP server")
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.WithError(err).Error("HTTP server error")
		}
	}()

	// Start gRPC server
	go func() {
		lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.GRPCPort))
		if err != nil {
			s.logger.WithError(err).Error("Failed to create gRPC listener")
			return
		}

		s.logger.WithField("address", lis.Addr().String()).Info("Starting gRPC server")
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.WithError(err).Error("gRPC server error")
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
			s.logger.WithError(err).Error("Failed to shutdown HTTP server")
		}
	}

	// Stop gRPC server
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
}

// stopComponents stops all server components
func (s *Server) stopComponents() error {
	// Stop sync engine
	s.syncEngine.Stop()

	// Stop P2P node
	if err := s.p2pNode.Stop(); err != nil {
		s.logger.WithError(err).Error("Failed to stop P2P node")
	}

	// Stop monitoring service
	s.monitoring.Stop()

	return nil
}

// Health returns the health status of the server
func (s *Server) Health() map[string]interface{} {
	return map[string]interface{}{
		"status": "healthy",
		"p2p":    s.p2pNode.Health(),
		"sync": map[string]interface{}{
			"watched_paths": s.syncEngine.GetWatchedPaths(),
			"running":       true,
		},
		"monitoring": map[string]interface{}{
			"status": "healthy",
		},
	}
}
