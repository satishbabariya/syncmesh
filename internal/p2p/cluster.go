package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/sirupsen/logrus"
)

// ClusterProtocol handles P2P-based cluster coordination (replaces Raft)
type ClusterProtocol struct {
	node   *P2PNode
	topic  interface{} // Will be pubsub.Topic
	sub    interface{} // Will be pubsub.Subscription
	logger *logrus.Entry

	// Cluster state
	peers     map[peer.ID]*PeerState
	metadata  map[string]interface{}
	consensus *P2PConsensus

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	runningMu sync.RWMutex
	wg        sync.WaitGroup
}

// NewClusterProtocol creates a new cluster protocol
func NewClusterProtocol(node *P2PNode) *ClusterProtocol {
	return &ClusterProtocol{
		node:     node,
		logger:   node.logger.WithField("component", "cluster"),
		peers:    make(map[peer.ID]*PeerState),
		metadata: make(map[string]interface{}),
		consensus: &P2PConsensus{
			State: FOLLOWER,
			Votes: make(map[peer.ID]bool),
		},
	}
}

// Initialize initializes the cluster protocol
func (c *ClusterProtocol) Initialize() error {
	c.logger.Info("Initializing cluster protocol")

	// In a full implementation, you'd join the cluster topic here
	// For now, we'll use a mock implementation
	c.logger.Info("Cluster protocol initialized successfully")
	return nil
}

// Start starts the cluster protocol
func (c *ClusterProtocol) Start(ctx context.Context) error {
	c.runningMu.Lock()
	if c.running {
		c.runningMu.Unlock()
		return fmt.Errorf("cluster protocol is already running")
	}
	c.running = true
	c.runningMu.Unlock()

	c.ctx, c.cancel = context.WithCancel(ctx)

	c.logger.Info("Starting cluster protocol")

	// Start message handler
	c.wg.Add(1)
	go c.handleMessages()

	// Start periodic tasks
	go c.periodicTasks()

	c.logger.Info("Cluster protocol started successfully")
	return nil
}

// Stop stops the cluster protocol
func (c *ClusterProtocol) Stop() {
	c.runningMu.Lock()
	if !c.running {
		c.runningMu.Unlock()
		return
	}
	c.running = false
	c.runningMu.Unlock()

	c.logger.Info("Stopping cluster protocol")

	if c.cancel != nil {
		c.cancel()
	}

	// Wait for all goroutines to finish
	c.wg.Wait()

	c.logger.Info("Cluster protocol stopped")
}

// GetClusterState returns the current cluster state
func (c *ClusterProtocol) GetClusterState() *ClusterState {
	c.node.stateMutex.RLock()
	defer c.node.stateMutex.RUnlock()

	return c.node.clusterState
}

// UpdateClusterState updates the cluster state
func (c *ClusterProtocol) UpdateClusterState(state *ClusterState) error {
	c.node.stateMutex.Lock()
	defer c.node.stateMutex.Unlock()

	c.node.clusterState = state
	return nil
}

// GetLeader returns the current leader
func (c *ClusterProtocol) GetLeader() peer.ID {
	c.node.stateMutex.RLock()
	defer c.node.stateMutex.RUnlock()

	return c.node.clusterState.Leader
}

// IsLeader returns true if this node is the leader
func (c *ClusterProtocol) IsLeader() bool {
	return c.GetLeader() == c.node.host.ID()
}

// handleMessages handles incoming cluster messages
func (c *ClusterProtocol) handleMessages() {
	defer c.wg.Done()

	// Mock message handling for now
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			// Process cluster messages
			c.processClusterMessage(&ClusterMessage{
				Type:      "heartbeat",
				PeerID:    c.node.host.ID().String(),
				Timestamp: time.Now().Unix(),
			})
		}
	}
}

// processClusterMessage processes a cluster message
func (c *ClusterProtocol) processClusterMessage(msg *ClusterMessage) {
	c.logger.WithFields(logrus.Fields{
		"type":      msg.Type,
		"peer_id":   msg.PeerID,
		"timestamp": msg.Timestamp,
	}).Debug("Processing cluster message")

	switch msg.Type {
	case "heartbeat":
		c.handleHeartbeat(msg)
	case "leader_election":
		c.handleLeaderElection(msg)
	case "peer_join":
		c.handlePeerJoin(msg)
	case "peer_leave":
		c.handlePeerLeave(msg)
	default:
		c.logger.WithField("type", msg.Type).Warn("Unknown cluster message type")
	}
}

// handleHeartbeat handles heartbeat messages
func (c *ClusterProtocol) handleHeartbeat(msg *ClusterMessage) {
	peerID := peer.ID(msg.PeerID)

	// Update peer status
	c.node.updatePeerStatus(peerID, "active")

	// If this is a leader heartbeat, update leader information
	if peerID == c.GetLeader() {
		c.node.stateMutex.Lock()
		c.node.clusterState.LastUpdate = time.Now()
		c.node.stateMutex.Unlock()
	}
}

// handleLeaderElection handles leader election messages
func (c *ClusterProtocol) handleLeaderElection(msg *ClusterMessage) {
	c.logger.WithField("candidate", msg.PeerID).Info("Leader election in progress")

	// Simple leader election: lowest peer ID becomes leader
	candidateID := peer.ID(msg.PeerID)
	currentLeader := c.GetLeader()

	if currentLeader == "" || candidateID.String() < currentLeader.String() {
		c.node.stateMutex.Lock()
		c.node.clusterState.Leader = candidateID
		c.node.clusterState.Term++
		c.node.clusterState.LastUpdate = time.Now()
		c.node.stateMutex.Unlock()

		c.logger.WithField("new_leader", candidateID.String()).Info("New leader elected")
	}
}

// handlePeerJoin handles peer join messages
func (c *ClusterProtocol) handlePeerJoin(msg *ClusterMessage) {
	peerID := peer.ID(msg.PeerID)

	// Add peer to cluster
	peerState := &PeerState{
		ID:       peerID,
		Status:   "active",
		LastSeen: time.Now(),
		Metadata: msg.Metadata,
	}

	c.node.stateMutex.Lock()
	c.node.peerStates[peerID] = peerState
	c.node.clusterState.Peers[peerID] = peerState
	c.node.stateMutex.Unlock()

	c.logger.WithField("peer_id", peerID.String()).Info("Peer joined cluster")
}

// handlePeerLeave handles peer leave messages
func (c *ClusterProtocol) handlePeerLeave(msg *ClusterMessage) {
	peerID := peer.ID(msg.PeerID)

	// Remove peer from cluster
	c.node.stateMutex.Lock()
	delete(c.node.peerStates, peerID)
	delete(c.node.clusterState.Peers, peerID)
	c.node.stateMutex.Unlock()

	c.logger.WithField("peer_id", peerID.String()).Info("Peer left cluster")

	// If the leaving peer was the leader, start leader election
	if peerID == c.GetLeader() {
		c.startLeaderElection()
	}
}

// startLeaderElection starts a leader election
func (c *ClusterProtocol) startLeaderElection() {
	c.logger.Info("Starting leader election")

	// Create leader election message
	msg := &ClusterMessage{
		Type:      "leader_election",
		PeerID:    c.node.host.ID().String(),
		Timestamp: time.Now().Unix(),
	}

	// Publish to cluster topic
	c.publishClusterMessage(msg)
}

// publishClusterMessage publishes a cluster message
func (c *ClusterProtocol) publishClusterMessage(msg *ClusterMessage) {
	// Serialize message
	_, err := json.Marshal(msg)
	if err != nil {
		c.logger.WithError(err).Error("Failed to marshal cluster message")
		return
	}

	// In a full implementation, you'd publish to the cluster topic
	c.logger.WithField("type", msg.Type).Debug("Cluster message published")
}

// periodicTasks performs periodic cluster tasks
func (c *ClusterProtocol) periodicTasks() {
	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(c.node.config.Cluster.HeartbeatInterval)
	defer heartbeatTicker.Stop()

	// Leader election ticker
	electionTicker := time.NewTicker(c.node.config.Cluster.ElectionTimeout)
	defer electionTicker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-heartbeatTicker.C:
			c.sendHeartbeat()
		case <-electionTicker.C:
			c.checkLeaderHealth()
		}
	}
}

// sendHeartbeat sends a heartbeat message
func (c *ClusterProtocol) sendHeartbeat() {
	msg := &ClusterMessage{
		Type:      "heartbeat",
		PeerID:    c.node.host.ID().String(),
		Timestamp: time.Now().Unix(),
		Metadata: map[string]string{
			"node_id": c.node.host.ID().String(),
		},
	}

	c.publishClusterMessage(msg)
}

// checkLeaderHealth checks the health of the current leader
func (c *ClusterProtocol) checkLeaderHealth() {
	leader := c.GetLeader()
	if leader == "" {
		// No leader, start election
		c.startLeaderElection()
		return
	}

	// Check if leader is still active
	c.node.stateMutex.RLock()
	leaderState, exists := c.node.peerStates[leader]
	c.node.stateMutex.RUnlock()

	if !exists || leaderState.Status != "active" {
		c.logger.WithField("leader", leader.String()).Warn("Leader is inactive, starting election")
		c.startLeaderElection()
	}
}
