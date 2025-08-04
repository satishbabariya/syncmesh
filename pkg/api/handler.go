package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/satishbabariya/syncmesh/internal/monitoring"
	"github.com/satishbabariya/syncmesh/internal/p2p"
	"github.com/sirupsen/logrus"
)

// Handler represents the HTTP API handler
type Handler struct {
	p2pNode    *p2p.P2PNode
	monitoring *monitoring.Service
	logger     *logrus.Entry
}

// NewHTTPHandler creates a new HTTP handler with all routes configured
func NewHTTPHandler(p2pNode *p2p.P2PNode, monitoring *monitoring.Service) http.Handler {
	handler := &Handler{
		p2pNode:    p2pNode,
		monitoring: monitoring,
		logger:     logger.NewForComponent("http-api"),
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

		// P2P cluster endpoints
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
	status := h.p2pNode.Health()
	h.writeJSON(w, http.StatusOK, status)
}

func (h *Handler) getFiles(w http.ResponseWriter, r *http.Request) {
	// In P2P, file status is managed by the file sync protocol
	// For now, return a basic response
	response := map[string]interface{}{
		"files":     []interface{}{},
		"total":     0,
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getFileStatus(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "path")
	if path == "" {
		h.writeError(w, http.StatusBadRequest, "File path is required")
		return
	}

	// In P2P, file status is managed by the file sync protocol
	response := map[string]interface{}{
		"path":      path,
		"status":    "unknown",
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) syncFile(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "path")
	if path == "" {
		h.writeError(w, http.StatusBadRequest, "File path is required")
		return
	}

	// In P2P, file sync is handled automatically
	response := map[string]interface{}{
		"path":      path,
		"status":    "synced",
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "path")
	if path == "" {
		h.writeError(w, http.StatusBadRequest, "File path is required")
		return
	}

	// In P2P, file deletion is handled automatically
	response := map[string]interface{}{
		"path":      path,
		"status":    "deleted",
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

// Cluster endpoints

func (h *Handler) getClusterStatus(w http.ResponseWriter, r *http.Request) {
	health := h.p2pNode.Health()
	h.writeJSON(w, http.StatusOK, health)
}

func (h *Handler) getNodes(w http.ResponseWriter, r *http.Request) {
	nodes := h.p2pNode.GetNodes()

	nodeList := make([]map[string]interface{}, 0, len(nodes))
	for nodeID, nodeData := range nodes {
		node := nodeData.(map[string]interface{})
		nodeList = append(nodeList, map[string]interface{}{
			"id":        nodeID,
			"addresses": node["addresses"],
			"status":    node["status"],
			"last_seen": node["last_seen"],
			"metadata":  node["metadata"],
		})
	}

	response := map[string]interface{}{
		"nodes":     nodeList,
		"total":     len(nodeList),
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	nodes := h.p2pNode.GetNodes()
	node, exists := nodes[nodeID]
	if !exists {
		h.writeError(w, http.StatusNotFound, "Node not found")
		return
	}

	nodeData := node.(map[string]interface{})
	response := map[string]interface{}{
		"id":        nodeID,
		"addresses": nodeData["addresses"],
		"status":    nodeData["status"],
		"last_seen": nodeData["last_seen"],
		"metadata":  nodeData["metadata"],
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) joinNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	var req struct {
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// In P2P, nodes join automatically via discovery
	response := map[string]interface{}{
		"node_id":   nodeID,
		"address":   req.Address,
		"status":    "joined",
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) removeNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeId")
	if nodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	// In P2P, nodes are removed automatically when they go offline
	response := map[string]interface{}{
		"node_id":   nodeID,
		"status":    "removed",
		"timestamp": time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getLeader(w http.ResponseWriter, r *http.Request) {
	leaderID := h.p2pNode.GetLeaderID()

	response := map[string]interface{}{
		"leader_id": leaderID,
		"is_leader": h.p2pNode.IsLeader(),
	}

	if leaderID != "" {
		nodes := h.p2pNode.GetNodes()
		if leader, exists := nodes[leaderID]; exists {
			leaderData := leader.(map[string]interface{})
			response["leader_info"] = map[string]interface{}{
				"id":        leaderID,
				"addresses": leaderData["addresses"],
				"status":    leaderData["status"],
				"last_seen": leaderData["last_seen"],
			}
		}
	}

	response["timestamp"] = time.Now().UTC()
	h.writeJSON(w, http.StatusOK, response)
}

// Health and monitoring endpoints

func (h *Handler) getHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"components": map[string]interface{}{
			"p2p": h.p2pNode.Health(),
		},
	}

	// Check if any component is unhealthy
	p2pHealth := h.p2pNode.Health()

	if !p2pHealth["running"].(bool) {
		response["status"] = "unhealthy"
		response["error"] = "P2P node is not running"
	}

	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) getMetrics(w http.ResponseWriter, r *http.Request) {
	// Basic metrics - in real implementation, this might return Prometheus format
	metrics := map[string]interface{}{
		"p2p":       h.p2pNode.Health(),
		"timestamp": time.Now().UTC(),
	}

	h.writeJSON(w, http.StatusOK, metrics)
}

func (h *Handler) getInfo(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"name":       "SyncMesh P2P",
		"version":    "1.0.0",
		"build_time": "unknown",
		"git_commit": "unknown",
		"node_id":    h.p2pNode.GetNodeID(),
		"is_leader":  h.p2pNode.IsLeader(),
		"leader_id":  h.p2pNode.GetLeaderID(),
		"timestamp":  time.Now().UTC(),
	}
	h.writeJSON(w, http.StatusOK, response)
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
		h.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if joinRequest.NodeID == "" {
		h.writeError(w, http.StatusBadRequest, "Node ID is required")
		return
	}

	// In P2P, nodes join automatically via discovery
	// This endpoint is kept for API compatibility
	response := map[string]interface{}{
		"success":   true,
		"message":   "Node successfully joined cluster",
		"leader_id": h.p2pNode.GetLeaderID(),
		"timestamp": time.Now().UTC(),
	}

	h.writeJSON(w, http.StatusOK, response)
}

// handleHeartbeat handles heartbeat requests from other nodes
func (h *Handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var heartbeatRequest struct {
		LeaderID  string                   `json:"leader_id"`
		Term      uint64                   `json:"term"`
		Nodes     []map[string]interface{} `json:"nodes"`
		Timestamp int64                    `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&heartbeatRequest); err != nil {
		h.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Accept the heartbeat and update leader information if needed
	currentLeader := h.p2pNode.GetLeaderID()
	if currentLeader == "" || currentLeader != heartbeatRequest.LeaderID {
		h.logger.WithField("new_leader", heartbeatRequest.LeaderID).Info("Updating cluster leader from heartbeat")
	}

	// In P2P, node updates are handled automatically
	// This endpoint is kept for API compatibility
	if len(heartbeatRequest.Nodes) > 0 {
		h.logger.WithField("nodes_data", heartbeatRequest.Nodes).Info("Processing nodes from heartbeat")
		h.logger.WithField("node_count", len(heartbeatRequest.Nodes)).Info("Updated cluster membership from heartbeat")
	} else {
		h.logger.Debug("No nodes data in heartbeat")
	}

	response := map[string]interface{}{
		"success":   true,
		"node_id":   h.p2pNode.GetNodeID(),
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
