package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
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

	// Only bootstrap node starts as leader, others wait for leader election
	if m.config.Bootstrap {
		m.becomeLeader()
		m.logger.Info("Bootstrap node starting as initial leader")
	} else {
		m.logger.Info("Non-bootstrap node waiting for leader election")
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

		// Real HTTP request to join cluster
		success := m.sendJoinRequest(addr)
		if success {
			// Get cluster status from leader to discover other nodes
			m.discoverClusterNodes(addr)
			m.logger.WithField("leader_address", addr).Info("Successfully joined cluster")
			return
		} else {
			m.logger.WithField("address", addr).Warn("Failed to join cluster via this address")
		}
	}

	m.logger.Error("Failed to join cluster via any provided addresses")
}

// sendJoinRequest sends a real HTTP request to join the cluster
func (m *Manager) sendJoinRequest(leaderAddr string) bool {
	// Extract hostname for HTTP request
	httpAddr := strings.Replace(leaderAddr, ":8082", ":8080", 1)

	joinRequest := map[string]interface{}{
		"node_id": m.nodeID,
		"address": m.config.AdvertiseAddr,
		"version": "1.0.0",
		"metadata": map[string]string{
			"role": "sync-node",
		},
	}

	payload, err := json.Marshal(joinRequest)
	if err != nil {
		m.logger.WithError(err).Error("Failed to marshal join request")
		return false
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://%s/api/v1/cluster/join", httpAddr),
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		m.logger.WithError(err).Error("Failed to send join request")
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		m.logger.Info("Join request accepted by leader")
		return true
	} else {
		m.logger.WithField("status", resp.StatusCode).Error("Join request rejected")
		return false
	}
}

// discoverClusterNodes gets the current cluster state from leader
func (m *Manager) discoverClusterNodes(leaderAddr string) {
	httpAddr := strings.Replace(leaderAddr, ":8082", ":8080", 1)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/api/v1/cluster/status", httpAddr))
	if err != nil {
		m.logger.WithError(err).Error("Failed to get cluster status")
		return
	}
	defer resp.Body.Close()

	var clusterStatus map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&clusterStatus); err != nil {
		m.logger.WithError(err).Error("Failed to decode cluster status")
		return
	}

	// Update leader information
	if leaderID, ok := clusterStatus["leader_id"].(string); ok {
		m.setLeader(leaderID)
	}

	m.logger.WithField("cluster_status", clusterStatus).Info("Discovered cluster state")
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

// sendHeartbeat sends real heartbeat to all nodes
func (m *Manager) sendHeartbeat() {
	m.nodesMutex.RLock()
	nodes := make([]*Node, 0, len(m.nodes))
	for _, node := range m.nodes {
		if node.ID != m.nodeID {
			nodes = append(nodes, node)
		}
	}
	m.nodesMutex.RUnlock()

	// Send real HTTP heartbeats to all nodes
	for _, node := range nodes {
		go m.sendHeartbeatToNode(node)
	}

	// Update self last seen
	m.nodesMutex.Lock()
	if selfNode, exists := m.nodes[m.nodeID]; exists {
		selfNode.LastSeen = time.Now()
	}
	m.nodesMutex.Unlock()
}

// sendHeartbeatToNode sends a real HTTP heartbeat to a specific node
func (m *Manager) sendHeartbeatToNode(node *Node) {
	// Convert Raft address to HTTP address
	httpAddr := strings.Replace(node.Address, ":8082", ":8080", 1)

	heartbeatData := map[string]interface{}{
		"leader_id": m.nodeID,
		"term":      time.Now().Unix(),
		"timestamp": time.Now().Unix(),
	}

	payload, err := json.Marshal(heartbeatData)
	if err != nil {
		m.logger.WithError(err).WithField("target_node", node.ID).Error("Failed to marshal heartbeat")
		return
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(
		fmt.Sprintf("http://%s/api/v1/cluster/heartbeat", httpAddr),
		"application/json",
		strings.NewReader(string(payload)),
	)

	if err != nil {
		m.logger.WithError(err).WithField("target_node", node.ID).Debug("Failed to send heartbeat")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Heartbeat successful - update node's last seen time
		m.nodesMutex.Lock()
		if existingNode, exists := m.nodes[node.ID]; exists {
			existingNode.LastSeen = time.Now()
			existingNode.Status = "active"
		}
		m.nodesMutex.Unlock()

		m.logger.WithField("target_node", node.ID).Debug("Heartbeat successful")
	} else {
		m.logger.WithField("target_node", node.ID).WithField("status", resp.StatusCode).Debug("Heartbeat failed")
	}
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
