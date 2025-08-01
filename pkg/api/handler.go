package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/satishbabariya/syncmesh/internal/cluster"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/satishbabariya/syncmesh/internal/monitoring"
	"github.com/satishbabariya/syncmesh/internal/sync"
	"github.com/sirupsen/logrus"
)

// Handler represents the HTTP API handler
type Handler struct {
	syncEngine     *sync.Engine
	clusterManager *cluster.Manager
	monitoring     *monitoring.Service
	logger         *logrus.Entry
}

// NewHTTPHandler creates a new HTTP handler with all routes configured
func NewHTTPHandler(syncEngine *sync.Engine, clusterManager *cluster.Manager, monitoring *monitoring.Service) http.Handler {
	handler := &Handler{
		syncEngine:     syncEngine,
		clusterManager: clusterManager,
		monitoring:     monitoring,
		logger:         logger.NewForComponent("http-api"),
	}

	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(handler.corsMiddleware)

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Sync endpoints
		r.Route("/sync", func(r chi.Router) {
			r.Get("/status", handler.getSyncStatus)
			r.Get("/files", handler.getFiles)
			r.Get("/files/{path}", handler.getFileStatus)
			r.Post("/files/{path}/sync", handler.syncFile)
			r.Delete("/files/{path}", handler.deleteFile)
			r.Post("/receive", handler.receiveFile)
		})

		// Cluster endpoints
		r.Route("/cluster", func(r chi.Router) {
			r.Get("/status", handler.getClusterStatus)
			r.Get("/nodes", handler.getNodes)
			r.Get("/nodes/{nodeId}", handler.getNode)
			r.Post("/nodes/{nodeId}/join", handler.joinNode)
			r.Delete("/nodes/{nodeId}", handler.removeNode)
			r.Get("/leader", handler.getLeader)
			r.Post("/join", handler.handleClusterJoin)
			r.Post("/heartbeat", handler.handleHeartbeat)
		})

		// Health and monitoring endpoints
		r.Get("/health", handler.getHealth)
		r.Get("/metrics", handler.getMetrics)
		r.Get("/info", handler.getInfo)
	})

	// Static routes
	r.Get("/", handler.getIndex)

	return r
}

// corsMiddleware adds CORS headers
func (h *Handler) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Sync endpoints

func (h *Handler) getSyncStatus(w http.ResponseWriter, r *http.Request) {
	status := h.syncEngine.Health()
	h.writeJSON(w, http.StatusOK, status)
}

func (h *Handler) getFiles(w http.ResponseWriter, r *http.Request) {
	// Mock response - in real implementation, get from sync engine
	files := []map[string]interface{}{
		{
			"path":        "/data/file1.txt",
			"size":        1024,
			"checksum":    "abc123",
			"mod_time":    time.Now().Unix(),
			"sync_status": "synced",
			"version":     1,
		},
		{
			"path":        "/data/file2.txt",
			"size":        2048,
			"checksum":    "def456",
			"mod_time":    time.Now().Unix(),
			"sync_status": "pending",
			"version":     2,
		},
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"files": files,
		"total": len(files),
	})
}

func (h *Handler) getFileStatus(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "path")
	if path == "" {
		h.writeError(w, http.StatusBadRequest, "File path is required")
		return
	}

	// Mock response - in real implementation, get from sync engine
	fileStatus := map[string]interface{}{
		"path":        path,
		"exists":      true,
		"size":        1024,
		"checksum":    "abc123",
		"mod_time":    time.Now().Unix(),
		"sync_status": "synced",
		"version":     1,
		"last_sync":   time.Now().Unix(),
	}

	h.writeJSON(w, http.StatusOK, fileStatus)
}

func (h *Handler) syncFile(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "path")
	if path == "" {
		h.writeError(w, http.StatusBadRequest, "File path is required")
		return
	}

	// Mock response - in real implementation, trigger sync
	h.logger.WithField("file", path).Info("Triggering file sync")

	response := map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Sync triggered for file: %s", path),
		"path":    path,
	}

	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "path")
	if path == "" {
		h.writeError(w, http.StatusBadRequest, "File path is required")
		return
	}

	// Mock response - in real implementation, delete file and sync
	h.logger.WithField("file", path).Info("Deleting file")

	response := map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("File deleted: %s", path),
		"path":    path,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Cluster endpoints

func (h *Handler) getClusterStatus(w http.ResponseWriter, r *http.Request) {
	health := h.clusterManager.Health()
	h.writeJSON(w, http.StatusOK, health)
}

func (h *Handler) getNodes(w http.ResponseWriter, r *http.Request) {
	nodes := h.clusterManager.GetNodes()

	nodeList := make([]map[string]interface{}, 0, len(nodes))
	for _, node := range nodes {
		nodeList = append(nodeList, map[string]interface{}{
			"id":        node.ID,
			"address":   node.Address,
			"status":    node.Status,
			"is_leader": node.IsLeader,
			"version":   node.Version,
			"last_seen": node.LastSeen.Unix(),
			"joined_at": node.JoinedAt.Unix(),
			"metadata":  node.Metadata,
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": nodeList,
		"total": len(nodeList),
	})
}

func (h *Handler) getNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	nodes := h.clusterManager.GetNodes()
	node, exists := nodes[nodeID]
	if !exists {
		h.writeError(w, http.StatusNotFound, "Node not found")
		return
	}

	nodeInfo := map[string]interface{}{
		"id":        node.ID,
		"address":   node.Address,
		"status":    node.Status,
		"is_leader": node.IsLeader,
		"version":   node.Version,
		"last_seen": node.LastSeen.Unix(),
		"joined_at": node.JoinedAt.Unix(),
		"metadata":  node.Metadata,
	}

	h.writeJSON(w, http.StatusOK, nodeInfo)
}

func (h *Handler) joinNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	var req struct {
		Address  string            `json:"address"`
		Version  string            `json:"version"`
		Metadata map[string]string `json:"metadata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	node := &cluster.Node{
		ID:       nodeID,
		Address:  req.Address,
		Status:   "active",
		LastSeen: time.Now(),
		Version:  req.Version,
		Metadata: req.Metadata,
		JoinedAt: time.Now(),
	}

	if err := h.clusterManager.AddNode(node); err != nil {
		h.writeError(w, http.StatusConflict, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Node %s joined cluster", nodeID),
		"node_id": nodeID,
	})
}

func (h *Handler) removeNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	if err := h.clusterManager.RemoveNode(nodeID); err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Node %s removed from cluster", nodeID),
		"node_id": nodeID,
	})
}

func (h *Handler) getLeader(w http.ResponseWriter, r *http.Request) {
	leaderID := h.clusterManager.GetLeaderID()

	response := map[string]interface{}{
		"leader_id": leaderID,
		"is_leader": h.clusterManager.IsLeader(),
	}

	if leaderID != "" {
		nodes := h.clusterManager.GetNodes()
		if leader, exists := nodes[leaderID]; exists {
			response["leader_info"] = map[string]interface{}{
				"id":        leader.ID,
				"address":   leader.Address,
				"status":    leader.Status,
				"version":   leader.Version,
				"last_seen": leader.LastSeen.Unix(),
			}
		}
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Health and monitoring endpoints

func (h *Handler) getHealth(w http.ResponseWriter, r *http.Request) {
	// Aggregate health from all components
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"components": map[string]interface{}{
			"sync":    h.syncEngine.Health(),
			"cluster": h.clusterManager.Health(),
		},
	}

	// Check if any component is unhealthy
	syncHealth := h.syncEngine.Health()
	clusterHealth := h.clusterManager.Health()

	if !syncHealth["running"].(bool) || !clusterHealth["running"].(bool) {
		health["status"] = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	h.writeJSON(w, 0, health) // Don't override status code if already set
}

func (h *Handler) getMetrics(w http.ResponseWriter, r *http.Request) {
	// Basic metrics - in real implementation, this might return Prometheus format
	metrics := map[string]interface{}{
		"sync":      h.syncEngine.Health(),
		"cluster":   h.clusterManager.Health(),
		"timestamp": time.Now().UTC(),
	}

	h.writeJSON(w, http.StatusOK, metrics)
}

func (h *Handler) getInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"service":    "gluster-cluster",
		"version":    "1.0.0",
		"build_time": "unknown",
		"git_commit": "unknown",
		"node_id":    h.clusterManager.GetNodeID(),
		"is_leader":  h.clusterManager.IsLeader(),
		"leader_id":  h.clusterManager.GetLeaderID(),
		"timestamp":  time.Now().UTC(),
	}

	h.writeJSON(w, http.StatusOK, info)
}

// Static endpoints

func (h *Handler) getIndex(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"service": "Gluster Cluster File Sync",
		"version": "1.0.0",
		"status":  "running",
		"api":     "/api/v1",
		"docs":    "/api/v1/docs",
		"health":  "/api/v1/health",
		"metrics": "/api/v1/metrics",
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Helper methods

func (h *Handler) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if statusCode > 0 {
		w.WriteHeader(statusCode)
	}

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.WithError(err).Error("Failed to write JSON response")
	}
}

func (h *Handler) writeError(w http.ResponseWriter, statusCode int, message string) {
	h.logger.WithFields(logrus.Fields{
		"status_code": statusCode,
		"message":     message,
	}).Warn("HTTP error response")

	response := map[string]interface{}{
		"error":     true,
		"message":   message,
		"timestamp": time.Now().UTC(),
	}

	h.writeJSON(w, statusCode, response)
}

func (h *Handler) getIntParam(r *http.Request, key string, defaultValue int) int {
	if value := r.URL.Query().Get(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func (h *Handler) getBoolParam(r *http.Request, key string, defaultValue bool) bool {
	if value := r.URL.Query().Get(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

// handleClusterJoin handles cluster join requests from other nodes
func (h *Handler) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	var joinRequest struct {
		NodeID   string            `json:"node_id"`
		Address  string            `json:"address"`
		Version  string            `json:"version"`
		Metadata map[string]string `json:"metadata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&joinRequest); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid join request format")
		return
	}

	h.logger.WithFields(logrus.Fields{
		"node_id": joinRequest.NodeID,
		"address": joinRequest.Address,
	}).Info("Received cluster join request")

	// Only allow join if this node is the leader
	if !h.clusterManager.IsLeader() {
		h.writeError(w, http.StatusServiceUnavailable, "This node is not the cluster leader")
		return
	}

	// Add or update the node in the cluster
	newNode := &cluster.Node{
		ID:       joinRequest.NodeID,
		Address:  joinRequest.Address,
		Status:   "active",
		LastSeen: time.Now(),
		Version:  joinRequest.Version,
		Metadata: joinRequest.Metadata,
		IsLeader: false,
		JoinedAt: time.Now(),
	}

	if err := h.clusterManager.AddNode(newNode); err != nil {
		// If node already exists, update it instead of failing
		if strings.Contains(err.Error(), "already exists") {
			h.logger.WithField("node_id", joinRequest.NodeID).Info("Node already exists, updating it")
			if updateErr := h.clusterManager.UpdateNodeStatus(joinRequest.NodeID, "active"); updateErr != nil {
				h.logger.WithError(updateErr).Error("Failed to update existing node")
				h.writeError(w, http.StatusInternalServerError, "Failed to update existing node")
				return
			}
		} else {
			h.logger.WithError(err).Error("Failed to add node to cluster")
			h.writeError(w, http.StatusInternalServerError, "Failed to add node to cluster")
			return
		}
	}

	response := map[string]interface{}{
		"success":   true,
		"message":   "Node successfully joined cluster",
		"leader_id": h.clusterManager.GetLeaderID(),
		"timestamp": time.Now().UTC(),
	}

	h.writeJSON(w, http.StatusOK, response)
}

// handleHeartbeat handles heartbeat requests from other nodes
func (h *Handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	// Read the raw body first to log it
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	h.logger.WithField("heartbeat_payload", string(bodyBytes)).Info("Raw heartbeat received")

	var heartbeatRequest struct {
		LeaderID  string                   `json:"leader_id"`
		Term      int64                    `json:"term"`
		Timestamp int64                    `json:"timestamp"`
		Nodes     []map[string]interface{} `json:"nodes"`
	}

	if err := json.Unmarshal(bodyBytes, &heartbeatRequest); err != nil {
		h.logger.WithError(err).Error("Failed to parse heartbeat JSON")
		h.writeError(w, http.StatusBadRequest, "Invalid heartbeat format")
		return
	}

	h.logger.WithFields(logrus.Fields{
		"leader_id":   heartbeatRequest.LeaderID,
		"nodes_count": len(heartbeatRequest.Nodes),
		"has_nodes":   heartbeatRequest.Nodes != nil,
	}).Info("Received heartbeat with cluster data")

	// Accept the heartbeat and update leader information if needed
	currentLeader := h.clusterManager.GetLeaderID()
	if currentLeader == "" || currentLeader != heartbeatRequest.LeaderID {
		h.logger.WithField("new_leader", heartbeatRequest.LeaderID).Info("Updating cluster leader from heartbeat")
		// In a real Raft implementation, you would validate the term and other Raft logic
	}

	// Update cluster membership from heartbeat data
	if len(heartbeatRequest.Nodes) > 0 {
		h.logger.WithField("nodes_data", heartbeatRequest.Nodes).Info("Processing nodes from heartbeat")
		h.clusterManager.UpdateNodesFromHeartbeat(heartbeatRequest.LeaderID, heartbeatRequest.Nodes)
		h.logger.WithField("node_count", len(heartbeatRequest.Nodes)).Info("Updated cluster membership from heartbeat")
	} else {
		h.logger.Warn("Heartbeat received but no nodes data included")
	}

	response := map[string]interface{}{
		"success":   true,
		"node_id":   h.clusterManager.GetNodeID(),
		"timestamp": time.Now().Unix(),
	}

	h.writeJSON(w, http.StatusOK, response)
}

// receiveFile handles file synchronization from other nodes
func (h *Handler) receiveFile(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Received file sync request from another node")

	// Parse multipart form
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100 MB limit
		h.writeError(w, http.StatusBadRequest, "Failed to parse multipart form")
		return
	}

	// Get metadata from form
	operation := r.FormValue("operation")
	filePath := r.FormValue("path")
	checksum := r.FormValue("checksum")

	if filePath == "" || operation == "" {
		h.writeError(w, http.StatusBadRequest, "Missing required fields: path, operation")
		return
	}

	h.logger.WithFields(logrus.Fields{
		"file":      filePath,
		"operation": operation,
		"checksum":  checksum,
	}).Info("Processing received file")

	// Get the uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "Failed to get uploaded file")
		return
	}
	defer file.Close()

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		h.logger.WithError(err).Error("Failed to create directory")
		h.writeError(w, http.StatusInternalServerError, "Failed to create directory")
		return
	}

	// Create the destination file
	destFile, err := os.Create(filePath)
	if err != nil {
		h.logger.WithError(err).Error("Failed to create destination file")
		h.writeError(w, http.StatusInternalServerError, "Failed to create file")
		return
	}
	defer destFile.Close()

	// Copy the file content
	bytesWritten, err := io.Copy(destFile, file)
	if err != nil {
		h.logger.WithError(err).Error("Failed to write file content")
		h.writeError(w, http.StatusInternalServerError, "Failed to write file")
		return
	}

	h.logger.WithFields(logrus.Fields{
		"file":          filePath,
		"bytes_written": bytesWritten,
		"original_name": header.Filename,
	}).Info("Successfully received and saved file")

	response := map[string]interface{}{
		"success":       true,
		"file":          filePath,
		"bytes_written": bytesWritten,
		"operation":     operation,
		"timestamp":     time.Now().UTC(),
	}

	h.writeJSON(w, http.StatusOK, response)
}
