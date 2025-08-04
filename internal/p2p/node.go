package p2p

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/docker"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/sirupsen/logrus"
)

// P2PNode represents a P2P node in the distributed file system
type P2PNode struct {
	// libp2p core components
	host   host.Host
	pubsub *MockPubSubService
	dht    DHTService

	// Custom protocols
	fileSync    *FileSyncProtocol
	clusterSync *ClusterProtocol
	discovery   *DiscoveryProtocol

	// State management
	fileStates   map[string]*FileState
	peerStates   map[peer.ID]*PeerState
	clusterState *ClusterState
	stateMutex   sync.RWMutex

	// Configuration
	config *config.P2PConfig
	logger *logrus.Entry

	// Lifecycle management
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	runningMu sync.RWMutex
}

// FileState represents the state of a file in the P2P network
type FileState struct {
	Path         string
	Size         int64
	Checksum     string
	ModTime      time.Time
	Version      uint64
	LastSyncTime time.Time
	SyncStatus   string // pending, syncing, synced, conflict
	FromPeer     peer.ID
	Metadata     map[string]string
}

// PeerState represents the state of a peer in the network
type PeerState struct {
	ID        peer.ID
	Addresses []string
	Status    string // active, inactive, failed
	LastSeen  time.Time
	Metadata  map[string]string
}

// ClusterState represents the cluster coordination state
type ClusterState struct {
	Peers      map[peer.ID]*PeerState
	Leader     peer.ID
	Term       uint64
	Metadata   map[string]interface{}
	LastUpdate time.Time
}

// NewP2PNode creates a new P2P node
func NewP2PNode(config *config.P2PConfig, dockerClient *docker.Client) (*P2PNode, error) {
	logger := logger.NewForComponent("p2p-node")

	// Generate private key for this node
	privKey, err := generatePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create libp2p host
	host, err := createHost(config, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	// Create mock pubsub service
	pubsub := NewMockPubSubService()

	node := &P2PNode{
		host:       host,
		pubsub:     pubsub,
		config:     config,
		logger:     logger,
		fileStates: make(map[string]*FileState),
		peerStates: make(map[peer.ID]*PeerState),
		clusterState: &ClusterState{
			Peers:    make(map[peer.ID]*PeerState),
			Metadata: make(map[string]interface{}),
		},
	}

	// Initialize protocols
	if err := node.initializeProtocols(); err != nil {
		return nil, fmt.Errorf("failed to initialize protocols: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"node_id": host.ID().String(),
		"address": host.Addrs()[0].String(),
	}).Info("P2P node created successfully")

	return node, nil
}

// Start starts the P2P node
func (n *P2PNode) Start(ctx context.Context) error {
	n.runningMu.Lock()
	if n.running {
		n.runningMu.Unlock()
		return fmt.Errorf("P2P node is already running")
	}
	n.running = true
	n.runningMu.Unlock()

	n.ctx, n.cancel = context.WithCancel(ctx)

	n.logger.WithField("node_id", n.host.ID().String()).Info("Starting P2P node")

	// Start discovery
	if err := n.discovery.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start discovery: %w", err)
	}

	// Start file sync protocol
	if err := n.fileSync.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start file sync protocol: %w", err)
	}

	// Start cluster sync protocol
	if err := n.clusterSync.Start(n.ctx); err != nil {
		return fmt.Errorf("failed to start cluster sync protocol: %w", err)
	}

	// Start periodic tasks
	n.startPeriodicTasks()

	n.logger.Info("P2P node started successfully")
	return nil
}

// Stop stops the P2P node
func (n *P2PNode) Stop() {
	n.runningMu.Lock()
	if !n.running {
		n.runningMu.Unlock()
		return
	}
	n.running = false
	n.runningMu.Unlock()

	n.logger.Info("Stopping P2P node")

	if n.cancel != nil {
		n.cancel()
	}

	// Stop all protocols
	if n.discovery != nil {
		n.discovery.Stop()
	}
	if n.fileSync != nil {
		n.fileSync.Stop()
	}
	if n.clusterSync != nil {
		n.clusterSync.Stop()
	}

	// Close libp2p host
	if n.host != nil {
		n.host.Close()
	}

	// Wait for all goroutines to finish
	n.wg.Wait()

	n.logger.Info("P2P node stopped")
}

// Health checks the health of the P2P node
func (n *P2PNode) Health() map[string]interface{} {
	n.runningMu.RLock()
	defer n.runningMu.RUnlock()

	return map[string]interface{}{
		"running":     n.running,
		"node_id":     n.host.ID().String(),
		"peers":       len(n.peerStates),
		"leader":      n.clusterState.Leader.String(),
		"is_leader":   n.clusterState.Leader == n.host.ID(),
		"last_update": n.clusterState.LastUpdate,
	}
}

// GetNodes returns all known nodes
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
	nodes[n.host.ID().String()] = map[string]interface{}{
		"id":        n.host.ID().String(),
		"addresses": n.host.Addrs(),
		"status":    "active",
		"last_seen": time.Now(),
		"metadata":  map[string]string{"self": "true"},
	}

	return nodes
}

// GetLeaderID returns the current leader ID
func (n *P2PNode) GetLeaderID() string {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()

	return n.clusterState.Leader.String()
}

// IsLeader returns true if this node is the leader
func (n *P2PNode) IsLeader() bool {
	return n.GetLeaderID() == n.host.ID().String()
}

// AddNode adds a node to the cluster (for API compatibility)
func (n *P2PNode) AddNode(node interface{}) error {
	// In P2P, nodes are discovered automatically
	// This method is kept for API compatibility
	return nil
}

// RemoveNode removes a node from the cluster (for API compatibility)
func (n *P2PNode) RemoveNode(nodeID string) error {
	// In P2P, nodes are removed automatically when they go offline
	// This method is kept for API compatibility
	return nil
}

// UpdateNodeStatus updates the status of a node (for API compatibility)
func (n *P2PNode) UpdateNodeStatus(nodeID string, status string) error {
	// In P2P, node status is managed automatically
	// This method is kept for API compatibility
	return nil
}

// UpdateNodesFromHeartbeat updates nodes from heartbeat (for API compatibility)
func (n *P2PNode) UpdateNodesFromHeartbeat(leaderID string, nodes map[string]interface{}) {
	// In P2P, node updates are handled automatically
	// This method is kept for API compatibility
}

// GetActiveNodes returns the number of active nodes
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

	// Add self as active
	activeNodes[n.host.ID().String()] = map[string]interface{}{
		"id":        n.host.ID().String(),
		"addresses": n.host.Addrs(),
		"status":    "active",
		"last_seen": time.Now(),
		"metadata":  map[string]string{"self": "true"},
	}

	return activeNodes
}

// initializeProtocols initializes all P2P protocols
func (n *P2PNode) initializeProtocols() error {
	// Initialize discovery protocol
	n.discovery = NewDiscoveryProtocol(n)
	if err := n.discovery.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize discovery protocol: %w", err)
	}

	// Initialize file sync protocol
	n.fileSync = NewFileSyncProtocol(n)
	if err := n.fileSync.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize file sync protocol: %w", err)
	}

	// Initialize cluster sync protocol
	n.clusterSync = NewClusterProtocol(n)
	if err := n.clusterSync.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize cluster sync protocol: %w", err)
	}

	return nil
}

// startPeriodicTasks starts periodic maintenance tasks
func (n *P2PNode) startPeriodicTasks() {
	// Peer health check
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-n.ctx.Done():
				return
			case <-ticker.C:
				n.checkPeerHealth()
			}
		}
	}()

	// Cluster state cleanup
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-n.ctx.Done():
				return
			case <-ticker.C:
				n.cleanupClusterState()
			}
		}
	}()
}

// checkPeerHealth checks the health of all peers
func (n *P2PNode) checkPeerHealth() {
	n.stateMutex.RLock()
	peers := make([]*PeerState, 0, len(n.peerStates))
	for _, peer := range n.peerStates {
		peers = append(peers, peer)
	}
	n.stateMutex.RUnlock()

	now := time.Now()
	timeout := 2 * time.Minute

	for _, peer := range peers {
		if now.Sub(peer.LastSeen) > timeout {
			n.logger.WithField("peer_id", peer.ID.String()).Warn("Peer marked as inactive")
			n.updatePeerStatus(peer.ID, "inactive")
		}
	}
}

// cleanupClusterState cleans up old cluster state
func (n *P2PNode) cleanupClusterState() {
	n.stateMutex.Lock()
	defer n.stateMutex.Unlock()

	// Remove inactive peers
	now := time.Now()
	timeout := 5 * time.Minute

	for peerID, peer := range n.peerStates {
		if now.Sub(peer.LastSeen) > timeout {
			delete(n.peerStates, peerID)
			delete(n.clusterState.Peers, peerID)
			n.logger.WithField("peer_id", peerID.String()).Info("Removed inactive peer")
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

	if peer, exists := n.clusterState.Peers[peerID]; exists {
		peer.Status = status
		peer.LastSeen = time.Now()
	}
}

// GetNodeID returns this node's ID
func (n *P2PNode) GetNodeID() string {
	return n.host.ID().String()
}

// GetPeers returns all known peers
func (n *P2PNode) GetPeers() map[string]*PeerState {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()

	peers := make(map[string]*PeerState)
	for id, peer := range n.peerStates {
		peers[id.String()] = peer
	}

	return peers
}

// GetClusterStatus returns the current cluster status
func (n *P2PNode) GetClusterStatus() map[string]interface{} {
	n.stateMutex.RLock()
	defer n.stateMutex.RUnlock()

	return map[string]interface{}{
		"node_id":      n.host.ID().String(),
		"total_peers":  len(n.peerStates),
		"active_peers": n.getActivePeerCount(),
		"leader":       n.clusterState.Leader.String(),
		"term":         n.clusterState.Term,
		"last_update":  n.clusterState.LastUpdate,
	}
}

// getActivePeerCount returns the number of active peers
func (n *P2PNode) getActivePeerCount() int {
	count := 0
	for _, peer := range n.peerStates {
		if peer.Status == "active" {
			count++
		}
	}
	return count
}

// Helper functions

// generatePrivateKey generates a private key for the P2P node
func generatePrivateKey() (crypto.PrivKey, error) {
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, rand.Reader)
	return priv, err
}

// createHost creates a libp2p host
func createHost(config *config.P2PConfig, privKey crypto.PrivKey) (host.Host, error) {
	// Create libp2p host options
	opts := []libp2p.Option{
		libp2p.Identity(privKey),
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/%s/tcp/%d", config.Host, config.Port)),
		libp2p.Security(noise.ID, noise.New),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
	}

	// Create host
	host, err := libp2p.New(opts...)
	if err != nil {
		return nil, err
	}

	return host, nil
}
