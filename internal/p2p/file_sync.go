package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sirupsen/logrus"
)

// FileSyncProtocol handles file synchronization across the P2P network
type FileSyncProtocol struct {
	node   *P2PNode
	topic  *MockTopic
	sub    *MockSubscription
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

	// Create pubsub topic
	topic, err := f.node.pubsub.Join(f.node.config.PubSub.FileSyncTopic)
	if err != nil {
		return fmt.Errorf("failed to join file sync topic: %w", err)
	}
	f.topic = topic

	// Subscribe to the topic
	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to file sync topic: %w", err)
	}
	f.sub = sub

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

	// Start message handler
	f.wg.Add(1)
	go f.handleMessages()

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

	// Close subscription
	if f.sub != nil {
		f.sub.Cancel()
	}

	// Close topic
	if f.topic != nil {
		f.topic.Close()
	}

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
	msg := &FileSyncMessage{
		Type:      event.Operation,
		FilePath:  event.Path,
		Checksum:  event.Checksum,
		Size:      event.Size,
		Timestamp: event.Timestamp.Unix(),
		FromPeer:  f.node.host.ID().String(),
		Version:   event.Version,
		Metadata:  event.Metadata,
	}

	// Add file data for create/modify operations
	if event.Operation != "delete" {
		fileData, err := f.readFileData(event.Path)
		if err != nil {
			return fmt.Errorf("failed to read file data: %w", err)
		}
		msg.FileData = fileData
	}

	// Serialize message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal file sync message: %w", err)
	}

	// Publish to topic
	if err := f.topic.Publish(f.ctx, data); err != nil {
		return fmt.Errorf("failed to publish file sync message: %w", err)
	}

	f.logger.WithField("file", event.Path).Info("File event published successfully")
	return nil
}

// GetFileState returns the state of a file
func (f *FileSyncProtocol) GetFileState(path string) (*FileState, error) {
	f.stateMutex.RLock()
	defer f.stateMutex.RUnlock()

	state, exists := f.fileStates[path]
	if !exists {
		return nil, fmt.Errorf("file state not found: %s", path)
	}

	return state, nil
}

// UpdateFileState updates the state of a file
func (f *FileSyncProtocol) UpdateFileState(path string, state *FileState) error {
	f.stateMutex.Lock()
	defer f.stateMutex.Unlock()

	f.fileStates[path] = state
	return nil
}

// handleMessages handles incoming file sync messages
func (f *FileSyncProtocol) handleMessages() {
	defer f.wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		default:
			msg, err := f.sub.Next(f.ctx)
			if err != nil {
				f.logger.WithError(err).Error("Failed to get next message")
				continue
			}

			// Skip messages from self
			if msg.ReceivedFrom == f.node.host.ID().String() {
				continue
			}

			go f.processMessage(msg)
		}
	}
}

// processMessage processes a file sync message
func (f *FileSyncProtocol) processMessage(msg *MockMessage) {
	f.logger.WithFields(logrus.Fields{
		"from_peer": msg.ReceivedFrom,
		"data_size": len(msg.Data),
	}).Debug("Processing file sync message")

	// Parse message
	var fileMsg FileSyncMessage
	if err := json.Unmarshal(msg.Data, &fileMsg); err != nil {
		f.logger.WithError(err).Error("Failed to unmarshal file sync message")
		return
	}

	// Create file event
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

	// Check for conflicts
	if f.hasConflict(event) {
		f.logger.WithField("file", event.Path).Warn("Conflict detected, resolving")
		if !f.resolveConflict(event) {
			f.logger.WithField("file", event.Path).Error("Failed to resolve conflict")
			return
		}
	}

	// Apply file operation
	if err := f.applyFileOperation(event, fileMsg.FileData); err != nil {
		f.logger.WithError(err).WithField("file", event.Path).Error("Failed to apply file operation")
		return
	}

	// Update file state
	f.updateFileState(event)

	f.logger.WithField("file", event.Path).Info("File operation applied successfully")
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
	if state.LastSyncTime.After(recentModTime) {
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

// updateFileState updates the file state
func (f *FileSyncProtocol) updateFileState(event FileEvent) {
	f.stateMutex.Lock()
	defer f.stateMutex.Unlock()

	state, exists := f.fileStates[event.Path]
	if !exists {
		state = &FileState{
			Path: event.Path,
		}
		f.fileStates[event.Path] = state
	}

	state.Size = event.Size
	state.Checksum = event.Checksum
	state.ModTime = event.Timestamp
	state.Version = event.Version
	state.LastSyncTime = time.Now()
	state.SyncStatus = "synced"
	state.FromPeer = event.FromPeer
	state.Metadata = event.Metadata
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
