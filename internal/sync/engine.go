package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/satishbabariya/syncmesh/internal/p2p"
	"github.com/sirupsen/logrus"
)

// Engine handles file synchronization
type Engine struct {
	config      *Config
	p2pNode     *p2p.P2PNode
	logger      *logrus.Entry
	watcher     *fsnotify.Watcher
	watchedDirs map[string]bool

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	runningMu sync.RWMutex
	wg        sync.WaitGroup
}

// Config holds sync engine configuration
type Config struct {
	WatchedPaths      []string
	ExcludePatterns   []string
	IncludePatterns   []string
	SyncInterval      time.Duration
	BatchSize         int
	MaxRetries        int
	RetryBackoff      time.Duration
	ConflictResolution string
	ChecksumAlgorithm string
	CompressionEnabled bool
	CompressionLevel   int
}

// NewEngine creates a new sync engine
func NewEngine(config *Config, p2pNode *p2p.P2PNode) *Engine {
	return &Engine{
		config:      config,
		p2pNode:     p2pNode,
		logger:      p2pNode.GetLogger().WithField("component", "sync-engine"),
		watchedDirs: make(map[string]bool),
	}
}

// Start starts the sync engine
func (e *Engine) Start(ctx context.Context) error {
	e.runningMu.Lock()
	defer e.runningMu.Unlock()

	if e.running {
		return fmt.Errorf("sync engine already running")
	}

	e.ctx, e.cancel = context.WithCancel(ctx)

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	e.watcher = watcher

	// Add watched paths
	for _, path := range e.config.WatchedPaths {
		if err := e.addWatchedPath(path); err != nil {
			e.logger.WithError(err).WithField("path", path).Warn("Failed to add watched path")
		}
	}

	// Start file watching
	e.wg.Add(1)
	go e.watchFiles()

	e.running = true
	e.logger.Info("Sync engine started successfully")
	return nil
}

// Stop stops the sync engine
func (e *Engine) Stop() error {
	e.runningMu.Lock()
	defer e.runningMu.Unlock()

	if !e.running {
		return nil
	}

	e.cancel()

	if e.watcher != nil {
		e.watcher.Close()
	}

	e.wg.Wait()
	e.running = false
	e.logger.Info("Sync engine stopped")
	return nil
}

// addWatchedPath adds a path to the file watcher
func (e *Engine) addWatchedPath(path string) error {
	// Check if path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		e.logger.WithField("path", path).Warn("Watched path does not exist, creating directory")
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	// Add to watcher
	if err := e.watcher.Add(path); err != nil {
		return fmt.Errorf("failed to add path to watcher: %w", err)
	}

	e.watchedDirs[path] = true
	e.logger.WithField("path", path).Info("Added path to file watcher")
	return nil
}

// watchFiles monitors file system events
func (e *Engine) watchFiles() {
	defer e.wg.Done()

	for {
		select {
		case <-e.ctx.Done():
			return
		case event, ok := <-e.watcher.Events:
			if !ok {
				return
			}
			e.handleFileEvent(event)
		case err, ok := <-e.watcher.Errors:
			if !ok {
				return
			}
			e.logger.WithError(err).Error("File watcher error")
		}
	}
}

// handleFileEvent handles a file system event
func (e *Engine) handleFileEvent(event fsnotify.Event) {
	// Skip temporary files and hidden files
	if strings.HasPrefix(filepath.Base(event.Name), ".") || 
	   strings.HasSuffix(event.Name, ".tmp") ||
	   strings.HasSuffix(event.Name, ".swp") {
		return
	}

	// Determine operation type
	var operation string
	switch {
	case event.Op&fsnotify.Create == fsnotify.Create:
		operation = "create"
	case event.Op&fsnotify.Write == fsnotify.Write:
		operation = "modify"
	case event.Op&fsnotify.Remove == fsnotify.Remove:
		operation = "delete"
	case event.Op&fsnotify.Rename == fsnotify.Rename:
		operation = "rename"
	default:
		return
	}

	e.logger.WithFields(logrus.Fields{
		"file":      event.Name,
		"operation": operation,
		"op":        event.Op.String(),
	}).Debug("File system event detected")

	// Handle the file event
	go e.processFileEvent(event.Name, operation)
}

// processFileEvent processes a file event
func (e *Engine) processFileEvent(filePath, operation string) {
	// Skip if file doesn't match patterns
	if !e.matchesFilePatterns(filePath) {
		e.logger.WithField("file", filePath).Debug("File doesn't match patterns, skipping")
		return
	}

	// Create file event
	fileEvent := p2p.FileEvent{
		Path:      filePath,
		Operation: operation,
		Timestamp: time.Now(),
		FromPeer:  e.p2pNode.GetPubSub().GetHostID(),
		Version:   1, // Simple versioning for now
		Metadata:  make(map[string]string),
	}

	// Get file info for create/modify operations
	if operation != "delete" {
		if info, err := os.Stat(filePath); err == nil {
			fileEvent.Size = info.Size()
			fileEvent.ModTime = info.ModTime()
			
			// Calculate checksum
			if checksum, err := e.calculateChecksum(filePath); err == nil {
				fileEvent.Checksum = checksum
			}
		}
	}

	// Publish file event to P2P network
	if err := e.p2pNode.GetFileSync().PublishFileEvent(fileEvent); err != nil {
		e.logger.WithError(err).WithField("file", filePath).Error("Failed to publish file event")
	} else {
		e.logger.WithFields(logrus.Fields{
			"file":      filePath,
			"operation": operation,
			"size":      fileEvent.Size,
		}).Info("File event published to P2P network")
	}
}

// matchesFilePatterns checks if a file matches the include/exclude patterns
func (e *Engine) matchesFilePatterns(filePath string) bool {
	fileName := filepath.Base(filePath)
	
	// Check exclude patterns
	for _, pattern := range e.config.ExcludePatterns {
		if matched, _ := filepath.Match(pattern, fileName); matched {
			return false
		}
	}

	// Check include patterns
	if len(e.config.IncludePatterns) > 0 {
		for _, pattern := range e.config.IncludePatterns {
			if matched, _ := filepath.Match(pattern, fileName); matched {
				return true
			}
		}
		return false
	}

	return true
}

// calculateChecksum calculates SHA256 checksum of a file
func (e *Engine) calculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// SyncFile manually syncs a specific file
func (e *Engine) SyncFile(filePath string) error {
	if info, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("file not found: %w", err)
	} else {
		// Calculate checksum
		checksum, err := e.calculateChecksum(filePath)
		if err != nil {
			return fmt.Errorf("failed to calculate checksum: %w", err)
		}

		fileEvent := p2p.FileEvent{
			Path:      filePath,
			Operation: "modify",
			Timestamp: time.Now(),
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			Checksum:  checksum,
			FromPeer:  e.p2pNode.GetPubSub().GetHostID(),
			Version:   1,
			Metadata:  make(map[string]string),
		}

		return e.p2pNode.GetFileSync().PublishFileEvent(fileEvent)
	}
}

// GetWatchedPaths returns the currently watched paths
func (e *Engine) GetWatchedPaths() []string {
	paths := make([]string, 0, len(e.watchedDirs))
	for path := range e.watchedDirs {
		paths = append(paths, path)
	}
	return paths
}

// AddWatchedPath adds a new path to watch
func (e *Engine) AddWatchedPath(path string) error {
	return e.addWatchedPath(path)
}
