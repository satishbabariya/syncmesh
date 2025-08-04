package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/docker"
	"github.com/sirupsen/logrus"
)

// P2PNode represents a P2P node in the network
type P2PNode struct {
	// libp2p core components
	host   host.Host
	pubsub *RealPubSubService
	dht    DHTService

	// Custom protocols
	fileSync    *FileSyncProtocol
	clusterSync *ClusterProtocol
	discovery   *DiscoveryProtocol

	// State management
	fileStates   map[string]*FileState
	peerStates   map[peer.ID]*PeerState
	clusterState *ClusterState

	// Configuration
	config *config.P2PConfig
	logger *logrus.Logger

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	runningMu sync.RWMutex

	// Mutex for state access
	stateMutex sync.RWMutex
}

// FileState represents the state of a file in the P2P network
type FileState struct {
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	ModTime      time.Time `json:"mod_time"`
	Checksum     string    `json:"checksum"`
	LastSync     time.Time `json:"last_sync"`
	SyncStatus   string    `json:"sync_status"` // "synced", "pending", "error"
	ErrorMessage string    `json:"error_message,omitempty"`
}

// PeerState represents the state of a peer in the P2P network
type PeerState struct {
	ID        peer.ID                `json:"id"`
	Addresses []multiaddr.Multiaddr  `json:"addresses"`
	Status    string                 `json:"status"` // "active", "inactive", "disconnected"
	LastSeen  time.Time              `json:"last_seen"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// ClusterState represents the cluster state
type ClusterState struct {
	Leader     peer.ID                `json:"leader"`
	Peers      map[peer.ID]*PeerState `json:"peers"`
	Metadata   map[string]interface{} `json:"metadata"`
	LastUpdate time.Time              `json:"last_update"`
}

// NewP2PNode creates a new P2P node
func NewP2PNode(config *config.P2PConfig, dockerClient *docker.Client) *P2PNode {
	ctx, cancel := context.WithCancel(context.Background())

	// Create logger
	logger := logrus.New()

	// Create real pubsub service
	pubsub := NewRealPubSubService(ctx, logger)

	node := &P2PNode{
		config:     config,
		logger:     logger,
		pubsub:     pubsub,
		ctx:        ctx,
		cancel:     cancel,
		fileStates: make(map[string]*FileState),
		peerStates: make(map[peer.ID]*PeerState),
		clusterState: &ClusterState{
			Peers:    make(map[peer.ID]*PeerState),
			Metadata: make(map[string]interface{}),
		},
	}

	// Initialize protocols
	node.initializeProtocols()

	return node
}

// initializeProtocols initializes all P2P protocols
func (n *P2PNode) initializeProtocols() {
	// Initialize file sync protocol
	n.fileSync = NewFileSyncProtocol(n)

	// Initialize cluster protocol
	n.clusterSync = NewClusterProtocol(n)

	// Initialize discovery protocol
	n.discovery = NewDiscoveryProtocol(n)

	// Set up file sync callback in pubsub service
	n.pubsub.SetFileSyncCallback(func(data []byte, fromPeer string) {
		n.fileSync.processMessage(data, fromPeer)
	})
}

// Start starts the P2P node
func (n *P2PNode) Start() error {
	n.runningMu.Lock()
	defer n.runningMu.Unlock()

	if n.running {
		return nil
	}

	// Start pubsub service
	if err := n.pubsub.Start(n.config); err != nil {
		return fmt.Errorf("failed to start pubsub: %w", err)
	}

	// Start protocols
	if err := n.fileSync.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start file sync protocol: %w", err)
	}

	if err := n.clusterSync.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start cluster protocol: %w", err)
	}

	if err := n.discovery.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start discovery protocol: %w", err)
	}

	// Start periodic tasks
	n.wg.Add(1)
	go n.startPeriodicTasks()

	n.running = true
	n.logger.Info("P2P node started successfully")
	return nil
}

// Stop stops the P2P node
func (n *P2PNode) Stop() error {
	n.runningMu.Lock()
	defer n.runningMu.Unlock()

	if !n.running {
		return nil
	}

	n.cancel()
	n.wg.Wait()

	// Stop pubsub
	if err := n.pubsub.Stop(); err != nil {
		n.logger.Errorf("Failed to stop pubsub: %v", err)
	}

	// Stop protocols
	n.fileSync.Stop()
	n.clusterSync.Stop()
	n.discovery.Stop()

	n.running = false
	n.logger.Info("P2P node stopped")
	return nil
}

// startPeriodicTasks starts periodic background tasks
func (n *P2PNode) startPeriodicTasks() {
	defer n.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.checkPeerHealth()
			n.cleanupClusterState()
		}
	}
}

// checkPeerHealth checks the health of connected peers
func (n *P2PNode) checkPeerHealth() {
	n.stateMutex.Lock()
	defer n.stateMutex.Unlock()

	for _, peerState := range n.peerStates {
		// Update last seen time
		peerState.LastSeen = time.Now()

		// Check if peer is still active (simplified check)
		if time.Since(peerState.LastSeen) > 2*time.Minute {
			peerState.Status = "inactive"
		} else {
			peerState.Status = "active"
		}
	}
}

// cleanupClusterState cleans up old cluster state
func (n *P2PNode) cleanupClusterState() {
	n.stateMutex.Lock()
	defer n.stateMutex.Unlock()

	// Remove peers that haven't been seen for more than 5 minutes
	cutoff := time.Now().Add(-5 * time.Minute)
	for peerID, peerState := range n.peerStates {
		if peerState.LastSeen.Before(cutoff) {
			delete(n.peerStates, peerID)
		}
	}
}

// updatePeerStatus updates the status of a peer
func (n *P2PNode) updatePeerStatus(peerID peer.ID, status string) {
	n.stateMutex.Lock()
	defer n.stateMutex.Unlock()

	if peer, exists := n.peerStates[peerID]; exists {
		peer.Status = status
		peer.LastSeen = time.Now()
	}
}

// GetNodeID returns the node ID
func (n *P2PNode) GetNodeID() string {
	return n.pubsub.GetHostID().String()
}

// GetPeers returns the list of connected peers
func (n *P2PNode) GetPeers() int {
	peers := n.pubsub.GetPeers()
	return len(peers)
}

// GetClusterStatus returns the cluster status
func (n *P2PNode) GetClusterStatus() *ClusterState {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()
	return n.clusterState
}

// getActivePeerCount returns the number of active peers
func (n *P2PNode) getActivePeerCount() int {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()

	count := 0
	for _, peer := range n.peerStates {
		if peer.Status == "active" {
			count++
		}
	}
	return count
}

// Health returns the health status of the P2P node
func (n *P2PNode) Health() map[string]interface{} {
	n.runningMu.RLock()
	defer n.runningMu.RUnlock()

	peers := n.pubsub.GetPeers()
	hostID := n.pubsub.GetHostID()

	return map[string]interface{}{
		"running":     n.running,
		"node_id":     hostID.String(),
		"peers":       len(peers),
		"leader":      n.clusterState.Leader.String(),
		"is_leader":   n.clusterState.Leader == hostID,
		"last_update": n.clusterState.LastUpdate,
	}
}

// GetNodes returns all nodes in the cluster
func (n *P2PNode) GetNodes() map[string]interface{} {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()

	nodes := make(map[string]interface{})
	for peerID, peer := range n.peerStates {
		nodes[peerID.String()] = map[string]interface{}{
			"id":        peerID.String(),
			"addresses": peer.Addresses,
			"status":    peer.Status,
			"last_seen": peer.LastSeen,
			"metadata":  peer.Metadata,
		}
	}

	// Add self
	hostID := n.pubsub.GetHostID()
	nodes[hostID.String()] = map[string]interface{}{
		"id":        hostID.String(),
		"addresses": n.pubsub.host.Addrs(),
		"status":    "active",
		"last_seen": time.Now(),
		"metadata":  map[string]string{"self": "true"},
	}

	return nodes
}

// GetLeaderID returns the leader ID
func (n *P2PNode) GetLeaderID() string {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()
	return n.clusterState.Leader.String()
}

// IsLeader returns whether this node is the leader
func (n *P2PNode) IsLeader() bool {
	return n.GetLeaderID() == n.pubsub.GetHostID().String()
}

// AddNode adds a node to the cluster (no-op for P2P)
func (n *P2PNode) AddNode(nodeID string, addresses []string) error {
	// P2P handles node discovery automatically
	return nil
}

// RemoveNode removes a node from the cluster (no-op for P2P)
func (n *P2PNode) RemoveNode(nodeID string) error {
	// P2P handles node removal automatically
	return nil
}

// UpdateNodeStatus updates the status of a node (no-op for P2P)
func (n *P2PNode) UpdateNodeStatus(nodeID string, status string) error {
	// P2P handles node status automatically
	return nil
}

// UpdateNodesFromHeartbeat updates nodes from heartbeat (no-op for P2P)
func (n *P2PNode) UpdateNodesFromHeartbeat(nodes map[string]interface{}) error {
	// P2P handles node updates automatically
	return nil
}

// GetActiveNodes returns active nodes
func (n *P2PNode) GetActiveNodes() map[string]interface{} {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()

	activeNodes := make(map[string]interface{})
	for peerID, peer := range n.peerStates {
		if peer.Status == "active" {
			activeNodes[peerID.String()] = map[string]interface{}{
				"id":        peerID.String(),
				"addresses": peer.Addresses,
				"status":    peer.Status,
				"last_seen": peer.LastSeen,
				"metadata":  peer.Metadata,
			}
		}
	}

	// Add self
	hostID := n.pubsub.GetHostID()
	activeNodes[hostID.String()] = map[string]interface{}{
		"id":        hostID.String(),
		"addresses": n.pubsub.host.Addrs(),
		"status":    "active",
		"last_seen": time.Now(),
		"metadata":  map[string]string{"self": "true"},
	}

	return activeNodes
}

// GetPubSub returns the pubsub service
func (n *P2PNode) GetPubSub() *RealPubSubService {
	return n.pubsub
}

// GetFileSync returns the file sync protocol
func (n *P2PNode) GetFileSync() *FileSyncProtocol {
	return n.fileSync
}

// GetLogger returns the logger
func (n *P2PNode) GetLogger() *logrus.Logger {
	return n.logger
}
