package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multiaddr"
	"github.com/sirupsen/logrus"
)

// DiscoveryProtocol handles peer discovery using mDNS and DHT
type DiscoveryProtocol struct {
	node   *P2PNode
	mdns   mdns.Service
	dht    interface{} // Will be routing.ContentRouting in full implementation
	logger *logrus.Entry

	// Discovery state
	discoveredPeers map[peer.ID]*PeerState
	peersMutex      sync.RWMutex

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	runningMu sync.RWMutex
}

// NewDiscoveryProtocol creates a new discovery protocol
func NewDiscoveryProtocol(node *P2PNode) *DiscoveryProtocol {
	return &DiscoveryProtocol{
		node:            node,
		logger:          node.logger.WithField("component", "discovery"),
		discoveredPeers: make(map[peer.ID]*PeerState),
	}
}

// Initialize initializes the discovery protocol
func (d *DiscoveryProtocol) Initialize() error {
	d.logger.Info("Initializing discovery protocol")

	// Initialize mDNS if enabled
	if d.node.config.Discovery.MDNS {
		d.mdns = mdns.NewMdnsService(d.node.host, "syncmesh-p2p", d)
		d.logger.Info("mDNS discovery enabled")
	}

	// Initialize DHT if enabled
	if d.node.config.Discovery.DHT {
		// For now, we'll use a simple routing discovery
		// In a full implementation, you'd initialize the DHT here
		d.logger.Info("DHT discovery enabled")
	}

	return nil
}

// Start starts the discovery protocol
func (d *DiscoveryProtocol) Start(ctx context.Context) error {
	d.runningMu.Lock()
	if d.running {
		d.runningMu.Unlock()
		return fmt.Errorf("discovery protocol is already running")
	}
	d.running = true
	d.runningMu.Unlock()

	d.ctx, d.cancel = context.WithCancel(ctx)

	d.logger.Info("Starting discovery protocol")

	// Start mDNS if enabled
	if d.mdns != nil {
		if err := d.mdns.Start(); err != nil {
			return fmt.Errorf("failed to start mDNS: %w", err)
		}
	}

	// Start bootstrap peer discovery
	go d.discoverBootstrapPeers()

	// Start periodic peer discovery
	go d.periodicDiscovery()

	d.logger.Info("Discovery protocol started successfully")
	return nil
}

// Stop stops the discovery protocol
func (d *DiscoveryProtocol) Stop() {
	d.runningMu.Lock()
	if !d.running {
		d.runningMu.Unlock()
		return
	}
	d.running = false
	d.runningMu.Unlock()

	d.logger.Info("Stopping discovery protocol")

	if d.cancel != nil {
		d.cancel()
	}

	if d.mdns != nil {
		d.mdns.Close()
	}

	d.logger.Info("Discovery protocol stopped")
}

// HandlePeerFound is called by mDNS when a peer is discovered
func (d *DiscoveryProtocol) HandlePeerFound(pi peer.AddrInfo) {
	d.logger.WithFields(logrus.Fields{
		"peer_id": pi.ID.String(),
		"addrs":   pi.Addrs,
	}).Info("Peer discovered via mDNS")

	// Add peer to discovered peers
	d.AddPeer(pi)

	// Try to connect to the peer
	go d.connectToPeer(pi)
}

// discoverBootstrapPeers discovers bootstrap peers
func (d *DiscoveryProtocol) discoverBootstrapPeers() {
	if len(d.node.config.Discovery.BootstrapPeers) == 0 {
		return
	}

	d.logger.Info("Discovering bootstrap peers")

	for _, addr := range d.node.config.Discovery.BootstrapPeers {
		select {
		case <-d.ctx.Done():
			return
		default:
			// Parse multiaddr and connect to bootstrap peer
			// This is a simplified implementation
			d.logger.WithField("bootstrap_addr", addr).Debug("Attempting to connect to bootstrap peer")
		}
	}
}

// periodicDiscovery performs periodic peer discovery
func (d *DiscoveryProtocol) periodicDiscovery() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.performDiscovery()
		}
	}
}

// performDiscovery performs active peer discovery
func (d *DiscoveryProtocol) performDiscovery() {
	d.logger.Debug("Performing periodic peer discovery")

	// Get current peers
	d.peersMutex.RLock()
	peers := make([]peer.ID, 0, len(d.discoveredPeers))
	for peerID := range d.discoveredPeers {
		peers = append(peers, peerID)
	}
	d.peersMutex.RUnlock()

	// Try to discover new peers through existing peers
	for _, peerID := range peers {
		select {
		case <-d.ctx.Done():
			return
		default:
			// In a full implementation, you'd query peers for their known peers
			d.logger.WithField("peer_id", peerID.String()).Debug("Querying peer for known peers")
		}
	}
}

// connectToPeer attempts to connect to a discovered peer
func (d *DiscoveryProtocol) connectToPeer(pi peer.AddrInfo) {
	d.logger.WithField("peer_id", pi.ID.String()).Debug("Attempting to connect to peer")

	// Skip if it's our own peer ID
	if pi.ID == d.node.host.ID() {
		return
	}

	// Try to connect
	ctx, cancel := context.WithTimeout(d.ctx, 10*time.Second)
	defer cancel()

	if err := d.node.host.Connect(ctx, pi); err != nil {
		d.logger.WithError(err).WithField("peer_id", pi.ID.String()).Debug("Failed to connect to peer")
		return
	}

	d.logger.WithField("peer_id", pi.ID.String()).Info("Successfully connected to peer")

	// Update peer status
	d.updatePeerStatus(pi.ID, "active")
}

// AddPeer adds a peer to the discovered peers list
func (d *DiscoveryProtocol) AddPeer(pi peer.AddrInfo) {
	d.peersMutex.Lock()
	defer d.peersMutex.Unlock()

	// Convert addresses to multiaddr format
	addresses := make([]multiaddr.Multiaddr, len(pi.Addrs))
	for i, addr := range pi.Addrs {
		addresses[i] = addr
	}

	// Create peer state
	peerState := &PeerState{
		ID:        pi.ID,
		Addresses: addresses,
		Status:    "active",
		LastSeen:  time.Now(),
		Metadata: map[string]interface{}{
			"discovered": "true",
		},
	}

	d.discoveredPeers[pi.ID] = peerState
	d.logger.WithField("peer_id", pi.ID.String()).Debug("Peer added to discovery")
}

// RemovePeer removes a peer from the discovered peers list
func (d *DiscoveryProtocol) RemovePeer(peerID peer.ID) error {
	d.peersMutex.Lock()
	defer d.peersMutex.Unlock()

	delete(d.discoveredPeers, peerID)

	// Also remove from the main node's peer states
	d.node.stateMutex.Lock()
	delete(d.node.peerStates, peerID)
	delete(d.node.clusterState.Peers, peerID)
	d.node.stateMutex.Unlock()

	d.logger.WithField("peer_id", peerID.String()).Debug("Removed peer from discovery")
	return nil
}

// GetDiscoveredPeers returns all discovered peers
func (d *DiscoveryProtocol) GetDiscoveredPeers() map[peer.ID]*PeerState {
	d.peersMutex.RLock()
	defer d.peersMutex.RUnlock()

	peers := make(map[peer.ID]*PeerState)
	for id, peer := range d.discoveredPeers {
		peers[id] = peer
	}

	return peers
}

// updatePeerStatus updates the status of a peer
func (d *DiscoveryProtocol) updatePeerStatus(peerID peer.ID, status string) {
	d.peersMutex.Lock()
	defer d.peersMutex.Unlock()

	if peer, exists := d.discoveredPeers[peerID]; exists {
		peer.Status = status
		peer.LastSeen = time.Now()
	}
}

// addrInfoToStrings converts peer.AddrInfo addresses to strings
func (d *DiscoveryProtocol) addrInfoToStrings(addrs []multiaddr.Multiaddr) []string {
	strings := make([]string, len(addrs))
	for i, addr := range addrs {
		strings[i] = addr.String()
	}
	return strings
}
