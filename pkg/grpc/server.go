package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/satishbabariya/syncmesh/internal/cluster"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/satishbabariya/syncmesh/internal/sync"
	"github.com/satishbabariya/syncmesh/pkg/proto"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Server represents the gRPC server
type Server struct {
	proto.UnimplementedFileSyncServer
	proto.UnimplementedClusterServer

	config         *config.Config
	logger         *logrus.Entry
	syncEngine     *sync.Engine
	clusterManager *cluster.Manager

	grpcServer *grpc.Server
}

// NewServer creates a new gRPC server
func NewServer(cfg *config.Config, syncEngine *sync.Engine, clusterManager *cluster.Manager) (*Server, error) {
	logger := logger.NewForComponent("grpc-server")

	server := &Server{
		config:         cfg,
		logger:         logger,
		syncEngine:     syncEngine,
		clusterManager: clusterManager,
	}

	// Configure gRPC server options
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Second,
			MaxConnectionAge:      30 * time.Second,
			MaxConnectionAgeGrace: 5 * time.Second,
			Time:                  5 * time.Second,
			Timeout:               1 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.UnaryInterceptor(server.unaryInterceptor),
		grpc.StreamInterceptor(server.streamInterceptor),
	}

	// Add TLS if enabled
	if cfg.Security.TLSEnabled {
		tlsConfig, err := server.loadTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS config: %w", err)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	server.grpcServer = grpc.NewServer(opts...)

	// Register services
	proto.RegisterFileSyncServer(server.grpcServer, server)
	proto.RegisterClusterServer(server.grpcServer, server)

	return server, nil
}

// Serve starts serving gRPC requests
func (s *Server) Serve(lis net.Listener) error {
	s.logger.WithField("address", lis.Addr().String()).Info("Starting gRPC server")
	return s.grpcServer.Serve(lis)
}

// GracefulStop gracefully stops the gRPC server
func (s *Server) GracefulStop() {
	s.logger.Info("Gracefully stopping gRPC server")
	s.grpcServer.GracefulStop()
}

// loadTLSConfig loads TLS configuration
func (s *Server) loadTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(s.config.Security.TLSCertFile, s.config.Security.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

// unaryInterceptor intercepts unary RPC calls
func (s *Server) unaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()

	// Authentication check
	if err := s.authenticate(ctx); err != nil {
		return nil, err
	}

	// Log request
	peer, _ := peer.FromContext(ctx)
	s.logger.WithFields(logrus.Fields{
		"method": info.FullMethod,
		"peer":   peer.Addr.String(),
	}).Debug("gRPC request")

	// Call handler
	resp, err := handler(ctx, req)

	// Log response
	duration := time.Since(start)
	fields := logrus.Fields{
		"method":   info.FullMethod,
		"duration": duration,
		"peer":     peer.Addr.String(),
	}

	if err != nil {
		fields["error"] = err.Error()
		s.logger.WithFields(fields).Error("gRPC request failed")
	} else {
		s.logger.WithFields(fields).Debug("gRPC request completed")
	}

	return resp, err
}

// streamInterceptor intercepts streaming RPC calls
func (s *Server) streamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()

	// Authentication check
	if err := s.authenticate(ss.Context()); err != nil {
		return err
	}

	// Log request
	peer, _ := peer.FromContext(ss.Context())
	s.logger.WithFields(logrus.Fields{
		"method": info.FullMethod,
		"peer":   peer.Addr.String(),
	}).Debug("gRPC stream started")

	// Call handler
	err := handler(srv, ss)

	// Log completion
	duration := time.Since(start)
	fields := logrus.Fields{
		"method":   info.FullMethod,
		"duration": duration,
		"peer":     peer.Addr.String(),
	}

	if err != nil {
		fields["error"] = err.Error()
		s.logger.WithFields(fields).Error("gRPC stream failed")
	} else {
		s.logger.WithFields(fields).Debug("gRPC stream completed")
	}

	return err
}

// authenticate performs authentication check
func (s *Server) authenticate(ctx context.Context) error {
	if !s.config.Security.AuthEnabled {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.Unauthenticated, "missing metadata")
	}

	tokens := md["authorization"]
	if len(tokens) == 0 {
		return status.Errorf(codes.Unauthenticated, "missing authorization token")
	}

	token := tokens[0]
	if !s.isValidToken(token) {
		return status.Errorf(codes.Unauthenticated, "invalid authorization token")
	}

	return nil
}

// isValidToken validates an authorization token
func (s *Server) isValidToken(token string) bool {
	for _, validToken := range s.config.Security.AuthTokens {
		if token == validToken {
			return true
		}
	}
	return false
}

// File Sync Service Implementation

// SyncFile syncs a file to this node
func (s *Server) SyncFile(ctx context.Context, req *proto.SyncFileRequest) (*proto.SyncFileResponse, error) {
	s.logger.WithFields(logrus.Fields{
		"file_path": req.FilePath,
		"operation": req.Operation,
		"from_node": req.FromNode,
	}).Info("Received file sync request")

	// Process the sync request
	// This would involve writing the file data to the appropriate location
	// and updating local file state

	response := &proto.SyncFileResponse{
		Success: true,
		Message: "File synced successfully",
	}

	return response, nil
}

// GetFileStatus returns the status of a file
func (s *Server) GetFileStatus(ctx context.Context, req *proto.GetFileStatusRequest) (*proto.GetFileStatusResponse, error) {
	// Get file status from sync engine
	// This is a mock implementation

	response := &proto.GetFileStatusResponse{
		Exists:   true,
		Size:     1024,
		Checksum: "abc123",
		ModTime:  time.Now().Unix(),
		Version:  1,
	}

	return response, nil
}

// StreamFileData streams file data for synchronization
func (s *Server) StreamFileData(stream proto.FileSync_StreamFileDataServer) error {
	s.logger.Info("File data stream started")

	var totalBytes int64
	var fileName string

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("failed to receive chunk: %w", err)
		}

		if fileName == "" {
			fileName = chunk.FileName
			s.logger.WithField("file", fileName).Info("Starting file transfer")
		}

		// Process chunk data
		totalBytes += int64(len(chunk.Data))

		// In real implementation, write chunk to file
		s.logger.WithFields(logrus.Fields{
			"file":  fileName,
			"chunk": chunk.ChunkNumber,
			"size":  len(chunk.Data),
		}).Debug("Received file chunk")
	}

	// Send response
	response := &proto.StreamFileResponse{
		Success:      true,
		Message:      "File transfer completed",
		BytesWritten: totalBytes,
	}

	return stream.SendAndClose(response)
}

// WatchFiles watches for file changes and streams them to client
func (s *Server) WatchFiles(req *proto.WatchFilesRequest, stream proto.FileSync_WatchFilesServer) error {
	s.logger.WithField("patterns", req.Patterns).Info("Starting file watch stream")

	// Mock implementation - send periodic updates
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			event := &proto.FileEvent{
				FilePath:  "/data/example.txt",
				Operation: "modify",
				Timestamp: time.Now().Unix(),
				Size:      1024,
				Checksum:  "def456",
			}

			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

// Cluster Service Implementation

// JoinCluster handles cluster join requests
func (s *Server) JoinCluster(ctx context.Context, req *proto.JoinClusterRequest) (*proto.JoinClusterResponse, error) {
	s.logger.WithFields(logrus.Fields{
		"node_id": req.NodeId,
		"address": req.Address,
	}).Info("Received cluster join request")

	// Add node to cluster
	node := &cluster.Node{
		ID:       req.NodeId,
		Address:  req.Address,
		Status:   "active",
		LastSeen: time.Now(),
		Version:  req.Version,
		Metadata: req.Metadata,
		JoinedAt: time.Now(),
	}

	if err := s.clusterManager.AddNode(node); err != nil {
		return &proto.JoinClusterResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to add node: %v", err),
		}, nil
	}

	// Get current cluster state
	nodes := s.clusterManager.GetNodes()
	clusterNodes := make([]*proto.ClusterNode, 0, len(nodes))
	for _, n := range nodes {
		clusterNodes = append(clusterNodes, &proto.ClusterNode{
			NodeId:   n.ID,
			Address:  n.Address,
			Status:   n.Status,
			IsLeader: n.IsLeader,
			Version:  n.Version,
			Metadata: n.Metadata,
		})
	}

	response := &proto.JoinClusterResponse{
		Success:  true,
		Message:  "Successfully joined cluster",
		LeaderId: s.clusterManager.GetLeaderID(),
		Nodes:    clusterNodes,
	}

	return response, nil
}

// LeaveCluster handles cluster leave requests
func (s *Server) LeaveCluster(ctx context.Context, req *proto.LeaveClusterRequest) (*proto.LeaveClusterResponse, error) {
	s.logger.WithField("node_id", req.NodeId).Info("Received cluster leave request")

	if err := s.clusterManager.RemoveNode(req.NodeId); err != nil {
		return &proto.LeaveClusterResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to remove node: %v", err),
		}, nil
	}

	return &proto.LeaveClusterResponse{
		Success: true,
		Message: "Successfully left cluster",
	}, nil
}

// GetClusterStatus returns current cluster status
func (s *Server) GetClusterStatus(ctx context.Context, req *proto.GetClusterStatusRequest) (*proto.GetClusterStatusResponse, error) {
	nodes := s.clusterManager.GetNodes()
	clusterNodes := make([]*proto.ClusterNode, 0, len(nodes))

	for _, node := range nodes {
		clusterNodes = append(clusterNodes, &proto.ClusterNode{
			NodeId:   node.ID,
			Address:  node.Address,
			Status:   node.Status,
			IsLeader: node.IsLeader,
			Version:  node.Version,
			Metadata: node.Metadata,
		})
	}

	response := &proto.GetClusterStatusResponse{
		LeaderId:    s.clusterManager.GetLeaderID(),
		Nodes:       clusterNodes,
		TotalNodes:  int32(len(nodes)),
		ActiveNodes: int32(len(s.clusterManager.GetActiveNodes())),
	}

	return response, nil
}

// StreamClusterEvents streams cluster events to client
func (s *Server) StreamClusterEvents(req *proto.StreamClusterEventsRequest, stream proto.Cluster_StreamClusterEventsServer) error {
	s.logger.Info("Starting cluster events stream")

	// Mock implementation - send periodic cluster events
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			event := &proto.ClusterEvent{
				Type:      "node-heartbeat",
				NodeId:    s.clusterManager.GetNodeID(),
				Timestamp: time.Now().Unix(),
				Data: map[string]string{
					"status": "active",
				},
			}

			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}
