package cluster

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/sirupsen/logrus"
)

// Manager handles cluster coordination and consensus
type Manager struct {
	config *config.ClusterConfig
	logger *logrus.Entry
	nodeID string

	// Cluster state
	nodes      map[string]*Node
	nodesMutex sync.RWMutex

	// Leadership state
	isLeader    bool
	leaderID    string
	leaderMutex sync.RWMutex

	// Lifecycle management
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	runningMu sync.RWMutex
}

// Node represents a cluster node
type Node struct {
	ID       string
	Address  string
	Status   string // active, inactive, failed
	LastSeen time.Time
	Version  string
	Metadata map[string]string
	IsLeader bool
	JoinedAt time.Time
}

// NewManager creates a new cluster manager
func NewManager(config *config.ClusterConfig, nodeID string) (*Manager, error) {
	logger := logger.NewForComponent("cluster-manager")

	manager := &Manager{
		config: config,
		logger: logger,
		nodeID: nodeID,
		nodes:  make(map[string]*Node),
	}

	// Initialize self node
	selfNode := &Node{
		ID:       nodeID,
		Address:  config.BindAddr,
		Status:   "active",
		LastSeen: time.Now(),
		Version:  "1.0.0",
		Metadata: map[string]string{
			"role": "sync-node",
		},
		JoinedAt: time.Now(),
	}
	manager.nodes[nodeID] = selfNode

	return manager, nil
}

// Start starts the cluster manager
func (m *Manager) Start(ctx context.Context) error {
	m.runningMu.Lock()
	if m.running {
		m.runningMu.Unlock()
		return fmt.Errorf("cluster manager is already running")
	}
	m.running = true
	m.runningMu.Unlock()

	m.ctx, m.cancel = context.WithCancel(ctx)

	m.logger.WithField("node_id", m.nodeID).Info("Starting cluster manager")

	// Start based on cluster mode
	switch m.config.Mode {
	case "raft":
		return m.startRaftMode()
	case "consul":
		return m.startConsulMode()
	case "etcd":
		return m.startEtcdMode()
	default:
		return fmt.Errorf("unsupported cluster mode: %s", m.config.Mode)
	}
}

// Stop stops the cluster manager
func (m *Manager) Stop() {
	m.runningMu.Lock()
	if !m.running {
		m.runningMu.Unlock()
		return
	}
	m.running = false
	m.runningMu.Unlock()

	m.logger.Info("Stopping cluster manager")

	if m.cancel != nil {
		m.cancel()
	}

	// Wait for all goroutines to finish
	m.wg.Wait()

	m.logger.Info("Cluster manager stopped")
}

// startRaftMode starts the manager in Raft consensus mode
func (m *Manager) startRaftMode() error {
	m.logger.Info("Starting in Raft mode")

	// Start Raft consensus algorithm (simplified implementation)
	m.wg.Add(1)
	go m.raftWorker()

	// Start heartbeat worker
	m.wg.Add(1)
	go m.heartbeatWorker()

	// Start leader election if bootstrap or no leader
	if m.config.Bootstrap || len(m.nodes) == 1 {
		m.becomeLeader()
	}

	// Join existing cluster if join addresses are provided
	if len(m.config.JoinAddresses) > 0 && !m.config.Bootstrap {
		m.wg.Add(1)
		go m.joinCluster()
	}

	return nil
}

// startConsulMode starts the manager in Consul mode
func (m *Manager) startConsulMode() error {
	m.logger.Info("Starting in Consul mode")

	// In a real implementation, you would:
	// 1. Connect to Consul
	// 2. Register this node
	// 3. Start watching for cluster changes
	// 4. Implement leader election using Consul sessions

	m.wg.Add(1)
	go m.consulWorker()

	return nil
}

// startEtcdMode starts the manager in etcd mode
func (m *Manager) startEtcdMode() error {
	m.logger.Info("Starting in etcd mode")

	// In a real implementation, you would:
	// 1. Connect to etcd
	// 2. Register this node
	// 3. Start watching for cluster changes
	// 4. Implement leader election using etcd

	m.wg.Add(1)
	go m.etcdWorker()

	return nil
}

// raftWorker implements simplified Raft consensus
func (m *Manager) raftWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.HeartbeatTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.performRaftTick()
		}
	}
}

// consulWorker handles Consul integration
func (m *Manager) consulWorker() {
	defer m.wg.Done()

	// Mock implementation
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.logger.Debug("Consul heartbeat")
			// In real implementation, update Consul session and check leadership
		}
	}
}

// etcdWorker handles etcd integration
func (m *Manager) etcdWorker() {
	defer m.wg.Done()

	// Mock implementation
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.logger.Debug("etcd heartbeat")
			// In real implementation, update etcd lease and check leadership
		}
	}
}

// heartbeatWorker sends periodic heartbeats
func (m *Manager) heartbeatWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.HeartbeatTimeout / 3)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.sendHeartbeat()
		}
	}
}

// joinCluster attempts to join an existing cluster
func (m *Manager) joinCluster() {
	defer m.wg.Done()

	for _, addr := range m.config.JoinAddresses {
		m.logger.WithField("address", addr).Info("Attempting to join cluster")

		// In real implementation, you would send a join request to the address
		// For now, we'll simulate adding nodes
		nodeID := fmt.Sprintf("node-%s", addr)
		node := &Node{
			ID:       nodeID,
			Address:  addr,
			Status:   "active",
			LastSeen: time.Now(),
			Version:  "1.0.0",
			Metadata: map[string]string{
				"role": "sync-node",
			},
			JoinedAt: time.Now(),
		}

		m.nodesMutex.Lock()
		m.nodes[nodeID] = node
		m.nodesMutex.Unlock()

		m.logger.WithField("node_id", nodeID).Info("Added node to cluster")
	}
}

// performRaftTick performs one Raft consensus tick
func (m *Manager) performRaftTick() {
	// Check if we're still the leader
	if m.IsLeader() {
		m.sendHeartbeat()
		m.checkNodeHealth()
	} else {
		// Check if we should start leader election
		if m.shouldStartElection() {
			m.startLeaderElection()
		}
	}
}

// sendHeartbeat sends heartbeat to all nodes
func (m *Manager) sendHeartbeat() {
	m.nodesMutex.RLock()
	nodes := make([]*Node, 0, len(m.nodes))
	for _, node := range m.nodes {
		if node.ID != m.nodeID {
			nodes = append(nodes, node)
		}
	}
	m.nodesMutex.RUnlock()

	for _, node := range nodes {
		// In real implementation, send actual heartbeat
		m.logger.WithField("target_node", node.ID).Debug("Sending heartbeat")
	}

	// Update self last seen
	m.nodesMutex.Lock()
	if selfNode, exists := m.nodes[m.nodeID]; exists {
		selfNode.LastSeen = time.Now()
	}
	m.nodesMutex.Unlock()
}

// checkNodeHealth checks the health of all nodes
func (m *Manager) checkNodeHealth() {
	now := time.Now()
	timeout := m.config.HeartbeatTimeout * 3

	m.nodesMutex.Lock()
	defer m.nodesMutex.Unlock()

	for _, node := range m.nodes {
		if node.ID == m.nodeID {
			continue
		}

		if now.Sub(node.LastSeen) > timeout {
			if node.Status == "active" {
				node.Status = "failed"
				m.logger.WithField("node_id", node.ID).Warn("Node marked as failed")
			}
		}
	}
}

// shouldStartElection determines if leader election should start
func (m *Manager) shouldStartElection() bool {
	// Check if there's no leader or leader is failed
	m.leaderMutex.RLock()
	leaderID := m.leaderID
	m.leaderMutex.RUnlock()

	if leaderID == "" {
		return true
	}

	m.nodesMutex.RLock()
	defer m.nodesMutex.RUnlock()

	if leader, exists := m.nodes[leaderID]; exists {
		return leader.Status == "failed"
	}

	return true
}

// startLeaderElection starts the leader election process
func (m *Manager) startLeaderElection() {
	m.logger.Info("Starting leader election")

	// Simplified leader election - in real implementation, use Raft algorithm
	activeNodes := m.GetActiveNodes()

	if len(activeNodes) == 0 || (len(activeNodes) == 1 && activeNodes[0] == m.nodeID) {
		m.becomeLeader()
		return
	}

	// For simplicity, lowest node ID becomes leader
	minNodeID := m.nodeID
	for _, nodeID := range activeNodes {
		if nodeID < minNodeID {
			minNodeID = nodeID
		}
	}

	if minNodeID == m.nodeID {
		m.becomeLeader()
	} else {
		m.setLeader(minNodeID)
	}
}

// becomeLeader makes this node the cluster leader
func (m *Manager) becomeLeader() {
	m.leaderMutex.Lock()
	m.isLeader = true
	m.leaderID = m.nodeID
	m.leaderMutex.Unlock()

	m.nodesMutex.Lock()
	if selfNode, exists := m.nodes[m.nodeID]; exists {
		selfNode.IsLeader = true
	}
	m.nodesMutex.Unlock()

	m.logger.WithField("node_id", m.nodeID).Info("Became cluster leader")
}

// setLeader sets the cluster leader
func (m *Manager) setLeader(nodeID string) {
	m.leaderMutex.Lock()
	m.isLeader = (nodeID == m.nodeID)
	m.leaderID = nodeID
	m.leaderMutex.Unlock()

	m.nodesMutex.Lock()
	for _, node := range m.nodes {
		node.IsLeader = (node.ID == nodeID)
	}
	m.nodesMutex.Unlock()

	m.logger.WithField("leader_id", nodeID).Info("Leader set")
}

// IsLeader returns true if this node is the cluster leader
func (m *Manager) IsLeader() bool {
	m.leaderMutex.RLock()
	defer m.leaderMutex.RUnlock()
	return m.isLeader
}

// GetLeaderID returns the current leader node ID
func (m *Manager) GetLeaderID() string {
	m.leaderMutex.RLock()
	defer m.leaderMutex.RUnlock()
	return m.leaderID
}

// GetNodeID returns this node's ID
func (m *Manager) GetNodeID() string {
	return m.nodeID
}

// GetActiveNodes returns a list of active node IDs
func (m *Manager) GetActiveNodes() []string {
	m.nodesMutex.RLock()
	defer m.nodesMutex.RUnlock()

	var activeNodes []string
	for _, node := range m.nodes {
		if node.Status == "active" {
			activeNodes = append(activeNodes, node.ID)
		}
	}

	return activeNodes
}

// GetNodes returns all nodes in the cluster
func (m *Manager) GetNodes() map[string]*Node {
	m.nodesMutex.RLock()
	defer m.nodesMutex.RUnlock()

	nodes := make(map[string]*Node)
	for id, node := range m.nodes {
		// Create a copy to avoid race conditions
		nodeCopy := *node
		nodes[id] = &nodeCopy
	}

	return nodes
}

// IsLeaderForFile determines if this node should lead sync for a specific file
func (m *Manager) IsLeaderForFile(filePath string) bool {
	if !m.IsLeader() {
		return false
	}

	// Use consistent hashing to determine which node should handle this file
	activeNodes := m.GetActiveNodes()
	if len(activeNodes) == 0 {
		return false
	}

	// Simple hash-based assignment
	hash := fnv.New32a()
	hash.Write([]byte(filePath))
	nodeIndex := int(hash.Sum32()) % len(activeNodes)

	return activeNodes[nodeIndex] == m.nodeID
}

// AddNode adds a new node to the cluster
func (m *Manager) AddNode(node *Node) error {
	m.nodesMutex.Lock()
	defer m.nodesMutex.Unlock()

	if _, exists := m.nodes[node.ID]; exists {
		return fmt.Errorf("node %s already exists", node.ID)
	}

	m.nodes[node.ID] = node
	m.logger.WithField("node_id", node.ID).Info("Added node to cluster")

	return nil
}

// RemoveNode removes a node from the cluster
func (m *Manager) RemoveNode(nodeID string) error {
	m.nodesMutex.Lock()
	defer m.nodesMutex.Unlock()

	if _, exists := m.nodes[nodeID]; !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}

	delete(m.nodes, nodeID)
	m.logger.WithField("node_id", nodeID).Info("Removed node from cluster")

	// If the removed node was the leader, start election
	if nodeID == m.GetLeaderID() {
		go m.startLeaderElection()
	}

	return nil
}

// UpdateNodeStatus updates the status of a node
func (m *Manager) UpdateNodeStatus(nodeID, status string) error {
	m.nodesMutex.Lock()
	defer m.nodesMutex.Unlock()

	node, exists := m.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}

	oldStatus := node.Status
	node.Status = status
	node.LastSeen = time.Now()

	m.logger.WithFields(logrus.Fields{
		"node_id":    nodeID,
		"old_status": oldStatus,
		"new_status": status,
	}).Info("Updated node status")

	return nil
}

// Health returns the health status of the cluster manager
func (m *Manager) Health() map[string]interface{} {
	m.runningMu.RLock()
	running := m.running
	m.runningMu.RUnlock()

	nodes := m.GetNodes()
	var activeCount, failedCount int
	for _, node := range nodes {
		switch node.Status {
		case "active":
			activeCount++
		case "failed":
			failedCount++
		}
	}

	return map[string]interface{}{
		"running":      running,
		"mode":         m.config.Mode,
		"node_id":      m.nodeID,
		"is_leader":    m.IsLeader(),
		"leader_id":    m.GetLeaderID(),
		"total_nodes":  len(nodes),
		"active_nodes": activeCount,
		"failed_nodes": failedCount,
		"last_check":   time.Now().UTC(),
	}
}
