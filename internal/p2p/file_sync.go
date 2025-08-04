package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sirupsen/logrus"
)

// FileSyncProtocol handles file synchronization across the P2P network
type FileSyncProtocol struct {
	node   *P2PNode
	logger *logrus.Entry

	// File operations
	fileQueue chan FileOperation
	workers   []*FileWorker

	// State management
	fileStates map[string]*FileState
	stateMutex sync.RWMutex

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	runningMu sync.RWMutex
	wg        sync.WaitGroup
}

// NewFileSyncProtocol creates a new file sync protocol
func NewFileSyncProtocol(node *P2PNode) *FileSyncProtocol {
	return &FileSyncProtocol{
		node:       node,
		logger:     node.logger.WithField("component", "file-sync"),
		fileQueue:  make(chan FileOperation, 1000),
		fileStates: make(map[string]*FileState),
	}
}

// Initialize initializes the file sync protocol
func (f *FileSyncProtocol) Initialize() error {
	f.logger.Info("Initializing file sync protocol")

	// The pubsub service is already initialized in the P2P node
	f.logger.Info("File sync protocol initialized successfully")
	return nil
}

// Start starts the file sync protocol
func (f *FileSyncProtocol) Start(ctx context.Context) error {
	f.runningMu.Lock()
	if f.running {
		f.runningMu.Unlock()
		return fmt.Errorf("file sync protocol is already running")
	}
	f.running = true
	f.runningMu.Unlock()

	f.ctx, f.cancel = context.WithCancel(ctx)

	f.logger.Info("Starting file sync protocol")

	// Start file workers
	f.startWorkers()

	f.logger.Info("File sync protocol started successfully")
	return nil
}

// Stop stops the file sync protocol
func (f *FileSyncProtocol) Stop() {
	f.runningMu.Lock()
	if !f.running {
		f.runningMu.Unlock()
		return
	}
	f.running = false
	f.runningMu.Unlock()

	f.logger.Info("Stopping file sync protocol")

	if f.cancel != nil {
		f.cancel()
	}

	// Stop workers
	f.stopWorkers()

	// Wait for all goroutines to finish
	f.wg.Wait()

	f.logger.Info("File sync protocol stopped")
}

// PublishFileEvent publishes a file event to the P2P network
func (f *FileSyncProtocol) PublishFileEvent(event FileEvent) error {
	f.logger.WithFields(logrus.Fields{
		"file":      event.Path,
		"operation": event.Operation,
		"size":      event.Size,
	}).Info("Publishing file event to P2P network")

	// Create file sync message
	fileMsg := FileSyncMessage{
		Type:      "file_event",
		FilePath:  event.Path,
		Checksum:  event.Checksum,
		Size:      event.Size,
		Timestamp: time.Now().Unix(),
		FromPeer:  f.node.GetPubSub().GetHostID().String(),
		Version:   event.Version,
		Metadata:  event.Metadata,
	}

	// Serialize message
	data, err := json.Marshal(fileMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal file sync message: %w", err)
	}

	// Publish to P2P network
	if err := f.node.GetPubSub().Publish(data); err != nil {
		return fmt.Errorf("failed to publish file event: %w", err)
	}

	// Update local file state
	f.updateFileState(event)

	f.logger.WithField("file", event.Path).Info("File event published successfully")
	return nil
}

// GetFileState returns the file state for a given path
func (f *FileSyncProtocol) GetFileState(path string) *FileState {
	f.stateMutex.RLock()
	defer f.stateMutex.RUnlock()

	state, exists := f.fileStates[path]
	if !exists {
		return nil
	}

	return &FileState{
		Path:         state.Path,
		Size:         state.Size,
		ModTime:      state.ModTime,
		Checksum:     state.Checksum,
		LastSync:     state.LastSync,
		SyncStatus:   state.SyncStatus,
		ErrorMessage: state.ErrorMessage,
	}
}

// UpdateFileState updates the state of a file
func (f *FileSyncProtocol) UpdateFileState(path string, state *FileState) error {
	f.stateMutex.Lock()
	defer f.stateMutex.Unlock()

	f.fileStates[path] = state
	return nil
}

// processMessage processes a file sync message from pubsub
func (f *FileSyncProtocol) processMessage(data []byte, fromPeer string) {
	f.logger.WithFields(logrus.Fields{
		"from_peer": fromPeer,
		"data_size": len(data),
	}).Debug("Processing file sync message")

	// Parse message
	var fileMsg FileSyncMessage
	if err := json.Unmarshal(data, &fileMsg); err != nil {
		f.logger.WithError(err).Error("Failed to unmarshal file sync message")
		return
	}

	// Skip messages from self
	if fileMsg.FromPeer == f.node.GetPubSub().GetHostID().String() {
		return
	}

	f.logger.WithFields(logrus.Fields{
		"type":      fileMsg.Type,
		"from_peer": fileMsg.FromPeer,
		"file":      fileMsg.FilePath,
	}).Info("Processing file sync message from peer")

	// Handle different message types
	switch fileMsg.Type {
	case "file_event":
		// Convert FileSyncMessage to FileEvent
		event := FileEvent{
			Path:      fileMsg.FilePath,
			Operation: fileMsg.Type,
			Timestamp: time.Unix(fileMsg.Timestamp, 0),
			Size:      fileMsg.Size,
			Checksum:  fileMsg.Checksum,
			FromPeer:  peer.ID(fileMsg.FromPeer),
			Version:   fileMsg.Version,
			Metadata:  fileMsg.Metadata,
		}
		f.handleFileEvent(event)
	case "file_request":
		f.handleFileRequest(fileMsg)
	case "file_response":
		f.handleFileResponse(fileMsg)
	default:
		f.logger.WithField("type", fileMsg.Type).Warn("Unknown message type")
	}
}

// hasConflict checks if there's a conflict with this file event
func (f *FileSyncProtocol) hasConflict(event FileEvent) bool {
	f.stateMutex.RLock()
	defer f.stateMutex.RUnlock()

	state, exists := f.fileStates[event.Path]
	if !exists {
		return false // No conflict for new files
	}

	// Check if file was recently modified (within last 5 seconds)
	recentModTime := time.Now().Add(-5 * time.Second)
	if state.LastSync.After(recentModTime) {
		return true
	}

	// Check if checksums differ (indicating different content)
	if state.Checksum != "" && event.Checksum != "" && state.Checksum != event.Checksum {
		return true
	}

	return false
}

// resolveConflict applies conflict resolution strategy
func (f *FileSyncProtocol) resolveConflict(event FileEvent) bool {
	switch f.node.config.FileSync.ConflictResolution {
	case "timestamp":
		return f.resolveByTimestamp(event)
	case "size":
		return f.resolveBySize(event)
	case "manual":
		return f.resolveManually(event)
	default:
		// Default to timestamp-based resolution
		return f.resolveByTimestamp(event)
	}
}

// resolveByTimestamp resolves conflict by choosing the most recent file
func (f *FileSyncProtocol) resolveByTimestamp(event FileEvent) bool {
	f.stateMutex.RLock()
	state, exists := f.fileStates[event.Path]
	f.stateMutex.RUnlock()

	if !exists {
		return true // No conflict, proceed
	}

	// If current file is newer, keep it
	if event.Timestamp.After(state.ModTime) {
		f.logger.WithField("file", event.Path).Info("Resolved conflict by timestamp - keeping newer file")
		return true
	}

	// If existing file is newer, skip this change
	f.logger.WithField("file", event.Path).Info("Resolved conflict by timestamp - keeping existing newer file")
	return false
}

// resolveBySize resolves conflict by choosing the larger file
func (f *FileSyncProtocol) resolveBySize(event FileEvent) bool {
	f.stateMutex.RLock()
	state, exists := f.fileStates[event.Path]
	f.stateMutex.RUnlock()

	if !exists {
		return true // No conflict, proceed
	}

	// If current file is larger, keep it
	if event.Size > state.Size {
		f.logger.WithField("file", event.Path).Info("Resolved conflict by size - keeping larger file")
		return true
	}

	// If existing file is larger, skip this change
	f.logger.WithField("file", event.Path).Info("Resolved conflict by size - keeping existing larger file")
	return false
}

// resolveManually marks file for manual resolution
func (f *FileSyncProtocol) resolveManually(event FileEvent) bool {
	f.stateMutex.Lock()
	defer f.stateMutex.Unlock()

	state, exists := f.fileStates[event.Path]
	if !exists {
		state = &FileState{
			Path: event.Path,
		}
		f.fileStates[event.Path] = state
	}

	// Mark as conflict for manual resolution
	state.SyncStatus = "conflict"

	f.logger.WithField("file", event.Path).Warn("File marked for manual conflict resolution")
	return false // Don't proceed with sync until manually resolved
}

// applyFileOperation applies a file operation to the local filesystem
func (f *FileSyncProtocol) applyFileOperation(event FileEvent, fileData []byte) error {
	switch event.Operation {
	case "create", "modify":
		return f.createOrModifyFile(event, fileData)
	case "delete":
		return f.deleteFile(event)
	default:
		return fmt.Errorf("unsupported operation: %s", event.Operation)
	}
}

// createOrModifyFile creates or modifies a file
func (f *FileSyncProtocol) createOrModifyFile(event FileEvent, fileData []byte) error {
	// Ensure directory exists
	dir := filepath.Dir(event.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create or overwrite file
	file, err := os.Create(event.Path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Write file data
	if _, err := file.Write(fileData); err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	f.logger.WithField("file", event.Path).Info("File created/modified successfully")
	return nil
}

// deleteFile deletes a file
func (f *FileSyncProtocol) deleteFile(event FileEvent) error {
	if err := os.Remove(event.Path); err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, which is fine for delete operations
			return nil
		}
		return fmt.Errorf("failed to delete file: %w", err)
	}

	f.logger.WithField("file", event.Path).Info("File deleted successfully")
	return nil
}

// updateFileState updates the local file state
func (f *FileSyncProtocol) updateFileState(event FileEvent) {
	f.stateMutex.Lock()
	defer f.stateMutex.Unlock()

	f.fileStates[event.Path] = &FileState{
		Path:       event.Path,
		Size:       event.Size,
		ModTime:    event.ModTime,
		Checksum:   event.Checksum,
		LastSync:   time.Now(),
		SyncStatus: "synced",
	}
}

// readFileData reads file data for transmission
func (f *FileSyncProtocol) readFileData(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// startWorkers starts file processing workers
func (f *FileSyncProtocol) startWorkers() {
	workerCount := 3 // Number of concurrent workers

	for i := 0; i < workerCount; i++ {
		worker := &FileWorker{
			ID:       i,
			queue:    f.fileQueue,
			stopChan: make(chan struct{}),
		}
		f.workers = append(f.workers, worker)

		f.wg.Add(1)
		go f.workerLoop(worker)
	}
}

// stopWorkers stops all file processing workers
func (f *FileSyncProtocol) stopWorkers() {
	for _, worker := range f.workers {
		close(worker.stopChan)
	}
}

// workerLoop is the main loop for a file worker
func (f *FileSyncProtocol) workerLoop(worker *FileWorker) {
	defer f.wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-worker.stopChan:
			return
		case operation := <-worker.queue:
			f.processFileOperation(operation)
		}
	}
}

// processFileOperation processes a file operation
func (f *FileSyncProtocol) processFileOperation(operation FileOperation) {
	f.logger.WithFields(logrus.Fields{
		"file":      operation.Event.Path,
		"operation": operation.Event.Operation,
		"worker_id": operation.Event.FromPeer.String(),
	}).Debug("Processing file operation")

	// Process the file operation
	// This is where you'd implement the actual file processing logic
	// For now, we'll just log it
	f.logger.WithField("file", operation.Event.Path).Info("File operation processed")
}

// handleFileEvent handles a file event from another node
func (f *FileSyncProtocol) handleFileEvent(event FileEvent) {
	f.logger.WithFields(logrus.Fields{
		"file":      event.Path,
		"operation": event.Operation,
		"from_peer": event.FromPeer,
		"size":      event.Size,
	}).Info("Handling file event from peer")

	// Check if we need to sync this file
	if f.shouldSyncFile(event) {
		f.logger.WithField("file", event.Path).Info("File needs synchronization")

		// Check for conflicts
		if f.hasConflict(event) {
			f.logger.WithField("file", event.Path).Warn("File conflict detected")
			if !f.resolveConflict(event) {
				f.logger.WithField("file", event.Path).Info("Conflict resolution rejected the file")
				return
			}
		}

		// Request the file from the peer
		go f.requestFileFromPeer(event)
	} else {
		f.logger.WithField("file", event.Path).Debug("File does not need synchronization")
	}
}

// requestFileFromPeer requests a file from a peer
func (f *FileSyncProtocol) requestFileFromPeer(event FileEvent) {
	f.logger.WithFields(logrus.Fields{
		"file":      event.Path,
		"from_peer": event.FromPeer,
	}).Info("Requesting file from peer")

	// Create file request message
	requestMsg := FileSyncMessage{
		Type:      "file_request",
		FilePath:  event.Path,
		Timestamp: time.Now().Unix(),
		FromPeer:  f.node.GetPubSub().GetHostID().String(),
	}

	// Serialize and publish request
	data, err := json.Marshal(requestMsg)
	if err != nil {
		f.logger.WithError(err).Error("Failed to marshal file request")
		return
	}

	// Publish request to P2P network
	if err := f.node.GetPubSub().Publish(data); err != nil {
		f.logger.WithError(err).Error("Failed to publish file request")
		return
	}

	f.logger.WithField("file", event.Path).Info("File request published to P2P network")
}

// handleFileRequest handles a file request from another node
func (f *FileSyncProtocol) handleFileRequest(msg FileSyncMessage) {
	f.logger.WithField("file", msg.FilePath).Info("Handling file request from peer")

	// Read the requested file
	fileData, err := f.readFileData(msg.FilePath)
	if err != nil {
		f.logger.WithError(err).WithField("file", msg.FilePath).Error("Failed to read requested file")
		return
	}

	// Create file response message
	responseMsg := FileSyncMessage{
		Type:      "file_response",
		FilePath:  msg.FilePath,
		Timestamp: time.Now().Unix(),
		FromPeer:  f.node.GetPubSub().GetHostID().String(),
		Size:      int64(len(fileData)),
		// Note: In a real implementation, you'd need to handle large files differently
		// For now, we'll include the file data in the message (suitable for small files)
		FileData: fileData,
	}

	// Serialize and publish response
	data, err := json.Marshal(responseMsg)
	if err != nil {
		f.logger.WithError(err).Error("Failed to marshal file response")
		return
	}

	// Publish response to P2P network
	if err := f.node.GetPubSub().Publish(data); err != nil {
		f.logger.WithError(err).Error("Failed to publish file response")
		return
	}

	f.logger.WithField("file", msg.FilePath).Info("File response published to P2P network")
}

// handleFileResponse handles a file response from another node
func (f *FileSyncProtocol) handleFileResponse(msg FileSyncMessage) {
	f.logger.WithFields(logrus.Fields{
		"file":      msg.FilePath,
		"from_peer": msg.FromPeer,
		"size":      msg.Size,
	}).Info("Handling file response from peer")

	// Create the file locally
	if err := f.createOrModifyFile(FileEvent{
		Path:      msg.FilePath,
		Operation: "create",
		Timestamp: time.Unix(msg.Timestamp, 0),
		Size:      msg.Size,
		FromPeer:  peer.ID(msg.FromPeer),
	}, msg.FileData); err != nil {
		f.logger.WithError(err).WithField("file", msg.FilePath).Error("Failed to create file from peer response")
		return
	}

	f.logger.WithField("file", msg.FilePath).Info("Successfully synced file from peer")
}

// shouldSyncFile determines if a file should be synchronized
func (f *FileSyncProtocol) shouldSyncFile(event FileEvent) bool {
	// Check if file is in watched paths
	if !f.isFileInWatchedPaths(event.Path) {
		return false
	}

	// Check if file matches include/exclude patterns
	if !f.matchesFilePatterns(event.Path) {
		return false
	}

	// Check if we already have this file with same checksum
	f.stateMutex.RLock()
	existingState, exists := f.fileStates[event.Path]
	f.stateMutex.RUnlock()

	if exists && existingState.Checksum == event.Checksum {
		return false
	}

	return true
}

// isFileInWatchedPaths checks if a file is in the watched paths
func (f *FileSyncProtocol) isFileInWatchedPaths(filePath string) bool {
	// Check if file is in any of the watched paths
	watchedPaths := []string{
		"/app/data",
		"/opt/tomcat/webapps/zenoptics/resources",
	}

	for _, watchedPath := range watchedPaths {
		if strings.HasPrefix(filePath, watchedPath) {
			return true
		}
	}
	return false
}

// matchesFilePatterns checks if a file matches the include/exclude patterns
func (f *FileSyncProtocol) matchesFilePatterns(filePath string) bool {
	// For now, accept all files
	// This can be enhanced with pattern matching
	return true
}
