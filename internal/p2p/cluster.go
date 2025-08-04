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
				PeerID:    c.node.pubsub.GetHostID(),
				LeaderID:  c.node.clusterState.Leader,
				Timestamp: time.Now(),
				Metadata:  make(map[string]interface{}),
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
	c.node.stateMutex.Lock()
	defer c.node.stateMutex.Unlock()

	// Update peer state
	peerState := &PeerState{
		ID:       msg.PeerID,
		Status:   "active",
		LastSeen: time.Now(),
		Metadata: msg.Metadata,
	}

	c.node.peerStates[msg.PeerID] = peerState
	c.node.clusterState.LastUpdate = time.Now()

	c.logger.Debugf("Heartbeat from peer %s", msg.PeerID)
}

// handleLeaderElection handles leader election messages
func (c *ClusterProtocol) handleLeaderElection(msg *ClusterMessage) {
	c.node.stateMutex.Lock()
	defer c.node.stateMutex.Unlock()

	// Update cluster state
	c.node.clusterState.Leader = msg.LeaderID
	c.node.clusterState.LastUpdate = time.Now()

	// Update metadata
	if msg.Metadata != nil {
		for k, v := range msg.Metadata {
			c.node.clusterState.Metadata[k] = v
		}
	}

	c.logger.Infof("Leader election: new leader is %s", msg.LeaderID)
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

// startLeaderElection starts the leader election process
func (c *ClusterProtocol) startLeaderElection() error {
	msg := &ClusterMessage{
		Type:      "leader_election",
		PeerID:    c.node.pubsub.GetHostID(),
		LeaderID:  c.node.pubsub.GetHostID(),
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"candidate": c.node.pubsub.GetHostID().String(),
			"term":      "1",
		},
	}

	return c.publishClusterMessage("leader_election", msg.Metadata)
}

// publishClusterMessage publishes a cluster message
func (c *ClusterProtocol) publishClusterMessage(msgType string, data map[string]interface{}) error {
	msg := &ClusterMessage{
		Type:      msgType,
		Timestamp: time.Now(),
		LeaderID:  c.node.clusterState.Leader,
		Metadata:  make(map[string]interface{}),
	}

	// Copy metadata
	for k, v := range c.node.clusterState.Metadata {
		msg.Metadata[k] = v
	}

	// Add message-specific data
	for k, v := range data {
		msg.Metadata[k] = v
	}

	// Serialize and publish
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster message: %w", err)
	}

	return c.node.pubsub.Publish(msgBytes)
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
func (c *ClusterProtocol) sendHeartbeat() error {
	msg := &ClusterMessage{
		Type:      "heartbeat",
		PeerID:    c.node.pubsub.GetHostID(),
		LeaderID:  c.node.clusterState.Leader,
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}

	// Add metadata
	msg.Metadata["node_id"] = c.node.pubsub.GetHostID().String()
	msg.Metadata["status"] = "active"

	return c.publishClusterMessage("heartbeat", msg.Metadata)
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
