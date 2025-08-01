package sync

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/satishbabariya/syncmesh/internal/cluster"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/docker"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/sirupsen/logrus"
)

// Engine handles file synchronization across cluster nodes
type Engine struct {
	config         *config.SyncConfig
	logger         *logrus.Entry
	dockerClient   *docker.Client
	clusterManager *cluster.Manager

	// File watching and processing
	watcher    *fsnotify.Watcher
	fileEvents chan FileEvent
	syncQueue  chan SyncTask

	// State management
	fileStates  map[string]*FileState
	statesMutex sync.RWMutex

	// Lifecycle management
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	runningMu sync.RWMutex
}

// FileEvent represents a file system event
type FileEvent struct {
	Path      string
	Operation string // create, modify, delete
	Timestamp time.Time
	Size      int64
	Checksum  string
}

// FileState represents the current state of a file
type FileState struct {
	Path         string
	Size         int64
	ModTime      time.Time
	Checksum     string
	Version      uint64
	LastSyncTime time.Time
	SyncStatus   string // pending, syncing, synced, conflict
	ConflictInfo *ConflictInfo
}

// ConflictInfo contains information about file conflicts
type ConflictInfo struct {
	ConflictTime time.Time
	LocalState   *FileState
	RemoteStates map[string]*FileState
	Resolution   string // manual, timestamp, size
}

// SyncTask represents a file synchronization task
type SyncTask struct {
	FileEvent   FileEvent
	Priority    int
	RetryCount  int
	LastAttempt time.Time
	TargetNodes []string
}

// NewEngine creates a new sync engine
func NewEngine(config *config.SyncConfig, dockerClient *docker.Client, clusterManager *cluster.Manager) (*Engine, error) {
	logger := logger.NewForComponent("sync-engine")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	engine := &Engine{
		config:         config,
		logger:         logger,
		dockerClient:   dockerClient,
		clusterManager: clusterManager,
		watcher:        watcher,
		fileEvents:     make(chan FileEvent, 1000),
		syncQueue:      make(chan SyncTask, 1000),
		fileStates:     make(map[string]*FileState),
	}

	return engine, nil
}

// Start starts the sync engine
func (e *Engine) Start(ctx context.Context) error {
	e.runningMu.Lock()
	if e.running {
		e.runningMu.Unlock()
		return fmt.Errorf("sync engine is already running")
	}
	e.running = true
	e.runningMu.Unlock()

	e.ctx, e.cancel = context.WithCancel(ctx)

	e.logger.Info("Starting sync engine")

	// Start watching Docker volumes
	if err := e.startVolumeWatching(); err != nil {
		return fmt.Errorf("failed to start volume watching: %w", err)
	}

	// Start worker goroutines
	e.startWorkers()

	e.logger.Info("Sync engine started successfully")
	return nil
}

// Stop stops the sync engine
func (e *Engine) Stop() {
	e.runningMu.Lock()
	if !e.running {
		e.runningMu.Unlock()
		return
	}
	e.running = false
	e.runningMu.Unlock()

	e.logger.Info("Stopping sync engine")

	if e.cancel != nil {
		e.cancel()
	}

	if e.watcher != nil {
		e.watcher.Close()
	}

	// Wait for all workers to finish
	e.wg.Wait()

	e.logger.Info("Sync engine stopped")
}

// startVolumeWatching starts watching Docker volumes for changes
func (e *Engine) startVolumeWatching() error {
	volumes, err := e.dockerClient.GetWatchedVolumes()
	if err != nil {
		return fmt.Errorf("failed to get watched volumes: %w", err)
	}

	for _, volume := range volumes {
		mountPath := volume.GetMountPath()
		if err := e.watcher.Add(mountPath); err != nil {
			e.logger.WithError(err).WithField("path", mountPath).Error("Failed to watch volume")
			continue
		}

		e.logger.WithField("path", mountPath).Info("Started watching volume")

		// Perform initial scan of the volume
		if err := e.scanVolume(mountPath); err != nil {
			e.logger.WithError(err).WithField("path", mountPath).Error("Failed to scan volume")
		}
	}

	return nil
}

// startWorkers starts the worker goroutines
func (e *Engine) startWorkers() {
	// File event processor
	e.wg.Add(1)
	go e.fileEventWorker()

	// Sync task processor
	for i := 0; i < 3; i++ { // Multiple workers for parallel processing
		e.wg.Add(1)
		go e.syncWorker(i)
	}

	// File system watcher
	e.wg.Add(1)
	go e.watcherWorker()

	// Periodic sync scheduler
	e.wg.Add(1)
	go e.periodicSyncWorker()
}

// fileEventWorker processes file events
func (e *Engine) fileEventWorker() {
	defer e.wg.Done()

	for {
		select {
		case <-e.ctx.Done():
			return
		case event := <-e.fileEvents:
			e.processFileEvent(event)
		}
	}
}

// syncWorker processes sync tasks
func (e *Engine) syncWorker(workerID int) {
	defer e.wg.Done()

	logger := e.logger.WithField("worker_id", workerID)

	for {
		select {
		case <-e.ctx.Done():
			return
		case task := <-e.syncQueue:
			if err := e.processSyncTask(task); err != nil {
				logger.WithError(err).WithField("file", task.FileEvent.Path).Error("Failed to process sync task")

				// Retry logic
				if task.RetryCount < e.config.MaxRetries {
					task.RetryCount++
					task.LastAttempt = time.Now()

					// Exponential backoff
					delay := time.Duration(task.RetryCount) * e.config.RetryBackoff
					time.AfterFunc(delay, func() {
						select {
						case e.syncQueue <- task:
						default:
							logger.WithField("file", task.FileEvent.Path).Warn("Sync queue full, dropping retry task")
						}
					})
				} else {
					logger.WithField("file", task.FileEvent.Path).Error("Max retries exceeded, giving up")
				}
			}
		}
	}
}

// watcherWorker handles file system watcher events
func (e *Engine) watcherWorker() {
	defer e.wg.Done()

	for {
		select {
		case <-e.ctx.Done():
			return
		case event, ok := <-e.watcher.Events:
			if !ok {
				return
			}

			// Filter out events based on patterns
			if e.shouldIgnoreFile(event.Name) {
				continue
			}

			fileEvent := e.convertWatcherEvent(event)
			select {
			case e.fileEvents <- fileEvent:
			default:
				e.logger.WithField("file", fileEvent.Path).Warn("File events queue full, dropping event")
			}

		case err, ok := <-e.watcher.Errors:
			if !ok {
				return
			}
			e.logger.WithError(err).Error("File watcher error")
		}
	}
}

// periodicSyncWorker performs periodic synchronization
func (e *Engine) periodicSyncWorker() {
	defer e.wg.Done()

	ticker := time.NewTicker(e.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.performPeriodicSync()
		}
	}
}

// processFileEvent processes a file event
func (e *Engine) processFileEvent(event FileEvent) {
	e.logger.WithFields(logrus.Fields{
		"file":      event.Path,
		"operation": event.Operation,
		"size":      event.Size,
	}).Debug("Processing file event")

	// Calculate checksum if file exists
	if event.Operation != "delete" {
		checksum, err := e.calculateChecksum(event.Path)
		if err != nil {
			e.logger.WithError(err).WithField("file", event.Path).Error("Failed to calculate checksum")
			return
		}
		event.Checksum = checksum
	}

	// Update file state
	e.updateFileState(event)

	// Create sync task
	task := SyncTask{
		FileEvent:   event,
		Priority:    e.calculatePriority(event),
		RetryCount:  0,
		LastAttempt: time.Now(),
		TargetNodes: e.clusterManager.GetActiveNodes(),
	}

	// Queue for synchronization
	select {
	case e.syncQueue <- task:
	default:
		e.logger.WithField("file", event.Path).Warn("Sync queue full, dropping task")
	}
}

// processSyncTask processes a synchronization task
func (e *Engine) processSyncTask(task SyncTask) error {
	e.logger.WithFields(logrus.Fields{
		"file":         task.FileEvent.Path,
		"operation":    task.FileEvent.Operation,
		"target_nodes": len(task.TargetNodes),
		"retry_count":  task.RetryCount,
	}).Info("Processing sync task")

	// Check if this node is the leader for this file
	if !e.clusterManager.IsLeaderForFile(task.FileEvent.Path) {
		e.logger.WithField("file", task.FileEvent.Path).Debug("Not leader for this file, skipping")
		return nil
	}

	// Sync to all target nodes
	for _, nodeID := range task.TargetNodes {
		if nodeID == e.clusterManager.GetNodeID() {
			continue // Skip self
		}

		if err := e.syncToNode(task.FileEvent, nodeID); err != nil {
			e.logger.WithError(err).WithFields(logrus.Fields{
				"file": task.FileEvent.Path,
				"node": nodeID,
			}).Error("Failed to sync to node")
			return err
		}
	}

	// Update sync status
	e.updateSyncStatus(task.FileEvent.Path, "synced")

	return nil
}

// syncToNode synchronizes a file to a specific node
func (e *Engine) syncToNode(event FileEvent, nodeID string) error {
	// This would typically use gRPC to send the file to the target node
	// For now, we'll log the operation
	e.logger.WithFields(logrus.Fields{
		"file":      event.Path,
		"node":      nodeID,
		"operation": event.Operation,
	}).Info("Syncing file to node")

	// TODO: Implement actual gRPC call to sync file

	return nil
}

// calculateChecksum calculates the checksum of a file
func (e *Engine) calculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var hasher hash.Hash
	switch e.config.ChecksumAlgorithm {
	case "sha256":
		hasher = sha256.New()
	case "md5":
		hasher = md5.New()
	default:
		return "", fmt.Errorf("unsupported checksum algorithm: %s", e.config.ChecksumAlgorithm)
	}

	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// updateFileState updates the state of a file
func (e *Engine) updateFileState(event FileEvent) {
	e.statesMutex.Lock()
	defer e.statesMutex.Unlock()

	state, exists := e.fileStates[event.Path]
	if !exists {
		state = &FileState{
			Path: event.Path,
		}
		e.fileStates[event.Path] = state
	}

	state.Size = event.Size
	state.ModTime = event.Timestamp
	state.Checksum = event.Checksum
	state.Version++
	state.SyncStatus = "pending"
}

// updateSyncStatus updates the sync status of a file
func (e *Engine) updateSyncStatus(filePath, status string) {
	e.statesMutex.Lock()
	defer e.statesMutex.Unlock()

	if state, exists := e.fileStates[filePath]; exists {
		state.SyncStatus = status
		if status == "synced" {
			state.LastSyncTime = time.Now()
		}
	}
}

// shouldIgnoreFile checks if a file should be ignored based on patterns
func (e *Engine) shouldIgnoreFile(filePath string) bool {
	filename := filepath.Base(filePath)

	// Check exclude patterns
	for _, pattern := range e.config.ExcludePatterns {
		if matched, _ := filepath.Match(pattern, filename); matched {
			return true
		}
	}

	// Check include patterns (if any)
	if len(e.config.IncludePatterns) > 0 {
		for _, pattern := range e.config.IncludePatterns {
			if matched, _ := filepath.Match(pattern, filename); matched {
				return false
			}
		}
		return true // Not in include list
	}

	return false
}

// convertWatcherEvent converts fsnotify event to our FileEvent
func (e *Engine) convertWatcherEvent(event fsnotify.Event) FileEvent {
	operation := "modify"
	if event.Op&fsnotify.Create == fsnotify.Create {
		operation = "create"
	} else if event.Op&fsnotify.Remove == fsnotify.Remove {
		operation = "delete"
	}

	var size int64
	if operation != "delete" {
		if info, err := os.Stat(event.Name); err == nil {
			size = info.Size()
		}
	}

	return FileEvent{
		Path:      event.Name,
		Operation: operation,
		Timestamp: time.Now(),
		Size:      size,
	}
}

// calculatePriority calculates the priority of a sync task
func (e *Engine) calculatePriority(event FileEvent) int {
	// Higher priority for smaller files and critical operations
	priority := 50

	if event.Operation == "delete" {
		priority += 20
	}

	if event.Size < 1024*1024 { // < 1MB
		priority += 10
	}

	return priority
}

// scanVolume performs an initial scan of a volume
func (e *Engine) scanVolume(volumePath string) error {
	return filepath.Walk(volumePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if e.shouldIgnoreFile(path) {
			return nil
		}

		event := FileEvent{
			Path:      path,
			Operation: "create",
			Timestamp: info.ModTime(),
			Size:      info.Size(),
		}

		select {
		case e.fileEvents <- event:
		default:
			e.logger.WithField("file", path).Warn("File events queue full during scan")
		}

		return nil
	})
}

// performPeriodicSync performs periodic synchronization check
func (e *Engine) performPeriodicSync() {
	e.logger.Debug("Performing periodic sync check")

	e.statesMutex.RLock()
	pendingFiles := make([]string, 0)
	for path, state := range e.fileStates {
		if state.SyncStatus == "pending" || time.Since(state.LastSyncTime) > e.config.Interval*2 {
			pendingFiles = append(pendingFiles, path)
		}
	}
	e.statesMutex.RUnlock()

	for _, path := range pendingFiles {
		// Re-queue for sync
		if info, err := os.Stat(path); err == nil {
			event := FileEvent{
				Path:      path,
				Operation: "modify",
				Timestamp: info.ModTime(),
				Size:      info.Size(),
			}

			select {
			case e.fileEvents <- event:
			default:
				e.logger.WithField("file", path).Warn("File events queue full during periodic sync")
			}
		}
	}
}

// Health returns the health status of the sync engine
func (e *Engine) Health() map[string]interface{} {
	e.runningMu.RLock()
	running := e.running
	e.runningMu.RUnlock()

	e.statesMutex.RLock()
	totalFiles := len(e.fileStates)
	var pendingFiles, syncedFiles, conflictFiles int
	for _, state := range e.fileStates {
		switch state.SyncStatus {
		case "pending", "syncing":
			pendingFiles++
		case "synced":
			syncedFiles++
		case "conflict":
			conflictFiles++
		}
	}
	e.statesMutex.RUnlock()

	return map[string]interface{}{
		"running":        running,
		"total_files":    totalFiles,
		"pending_files":  pendingFiles,
		"synced_files":   syncedFiles,
		"conflict_files": conflictFiles,
		"queue_size":     len(e.syncQueue),
		"events_queue":   len(e.fileEvents),
	}
}
