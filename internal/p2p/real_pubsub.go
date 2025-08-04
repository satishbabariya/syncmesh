package p2p

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multiaddr"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/sirupsen/logrus"
)

// RealPubSubService implements real P2P pubsub using go-libp2p
type RealPubSubService struct {
	host    host.Host
	pubsub  *pubsub.PubSub
	topic   *pubsub.Topic
	sub     *pubsub.Subscription
	logger  *logrus.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
	mu      sync.RWMutex

	// Callback for file sync messages
	fileSyncCallback func(data []byte, fromPeer string)

	// Discovery service
	discovery mdns.Service

	// Connected peers
	peers   map[peer.ID]peer.AddrInfo
	peersMu sync.RWMutex
}

// NewRealPubSubService creates a new real pubsub service
func NewRealPubSubService(ctx context.Context, logger *logrus.Logger) *RealPubSubService {
	return &RealPubSubService{
		logger: logger,
		ctx:    ctx,
		peers:  make(map[peer.ID]peer.AddrInfo),
	}
}

// SetFileSyncCallback sets the callback for file sync messages
func (r *RealPubSubService) SetFileSyncCallback(callback func(data []byte, fromPeer string)) {
	r.fileSyncCallback = callback
}

// Start initializes and starts the pubsub service
func (r *RealPubSubService) Start(p2pConfig *config.P2PConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("pubsub service already running")
	}

	// Create libp2p host
	host, err := r.createHost(p2pConfig)
	if err != nil {
		return fmt.Errorf("failed to create host: %w", err)
	}
	r.host = host

	// Create pubsub service
	ps, err := pubsub.NewGossipSub(r.ctx, r.host)
	if err != nil {
		return fmt.Errorf("failed to create pubsub: %w", err)
	}
	r.pubsub = ps

	// Join topic
	topic, err := ps.Join("syncmesh-files")
	if err != nil {
		return fmt.Errorf("failed to join topic: %w", err)
	}
	r.topic = topic

	// Subscribe to topic
	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("failed to subscribe to topic: %w", err)
	}
	r.sub = sub

	// Start discovery service
	r.startDiscovery()

	// Start message handling
	r.wg.Add(1)
	go r.handleMessages()

	// Start peer connection monitoring
	r.wg.Add(1)
	go r.monitorPeers()

	r.running = true
	r.logger.Info("Real pubsub service started successfully")
	return nil
}

// createHost creates a libp2p host
func (r *RealPubSubService) createHost(p2pConfig *config.P2PConfig) (host.Host, error) {
	// Create private key
	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.Ed25519, 2048, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Create multiaddr
	addr, err := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", p2pConfig.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to create multiaddr: %w", err)
	}

	// Create libp2p host with enhanced configuration for P2P networking
	host, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrs(addr),
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	return host, nil
}

// startDiscovery starts the mDNS discovery service
func (r *RealPubSubService) startDiscovery() {
	// Create mDNS discovery service
	r.discovery = mdns.NewMdnsService(r.host, "syncmesh-p2p", r)

	// Start discovery
	if err := r.discovery.Start(); err != nil {
		r.logger.WithError(err).Error("Failed to start discovery service")
		return
	}

	r.logger.Info("Discovery service started")
}

// HandlePeerFound implements the mdns.Notifee interface
func (r *RealPubSubService) HandlePeerFound(pi peer.AddrInfo) {
	// Don't connect to self
	if pi.ID == r.host.ID() {
		return
	}

	r.logger.WithField("peer", pi.ID.String()).Info("Peer discovered")

	// Connect to the peer
	if err := r.host.Connect(r.ctx, pi); err != nil {
		r.logger.WithError(err).WithField("peer", pi.ID.String()).Error("Failed to connect to peer")
		return
	}

	// Add to peers list
	r.peersMu.Lock()
	r.peers[pi.ID] = pi
	r.peersMu.Unlock()

	r.logger.WithField("peer", pi.ID.String()).Info("Connected to peer")
}

// monitorPeers monitors peer connections
func (r *RealPubSubService) monitorPeers() {
	defer r.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.peersMu.RLock()
			peerCount := len(r.peers)
			r.peersMu.RUnlock()

			r.logger.WithField("peer_count", peerCount).Debug("Peer monitoring tick")
		}
	}
}

// handleMessages handles incoming pubsub messages
func (r *RealPubSubService) handleMessages() {
	defer r.wg.Done()

	for {
		select {
		case <-r.ctx.Done():
			return
		default:
			msg, err := r.sub.Next(r.ctx)
			if err != nil {
				r.logger.WithError(err).Error("Failed to get next message")
				continue
			}

			// Skip messages from self
			if msg.ReceivedFrom == r.host.ID() {
				continue
			}

			r.logger.WithFields(logrus.Fields{
				"from_peer": msg.ReceivedFrom.String(),
				"data_size": len(msg.Data),
			}).Debug("Received pubsub message")

			// Call file sync callback if set
			if r.fileSyncCallback != nil {
				go r.fileSyncCallback(msg.Data, msg.ReceivedFrom.String())
			}
		}
	}
}

// Publish publishes a message to the pubsub topic
func (r *RealPubSubService) Publish(data []byte) error {
	if !r.running {
		return fmt.Errorf("pubsub service not running")
	}

	return r.topic.Publish(r.ctx, data)
}

// GetHostID returns the host ID
func (r *RealPubSubService) GetHostID() peer.ID {
	return r.host.ID()
}

// GetPeerCount returns the number of connected peers
func (r *RealPubSubService) GetPeerCount() int {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()
	return len(r.peers)
}

// GetPeers returns the list of connected peers
func (r *RealPubSubService) GetPeers() []peer.ID {
	r.peersMu.RLock()
	defer r.peersMu.RUnlock()

	peers := make([]peer.ID, 0, len(r.peers))
	for peerID := range r.peers {
		peers = append(peers, peerID)
	}
	return peers
}

// Stop stops the pubsub service
func (r *RealPubSubService) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	r.cancel()

	// Stop discovery
	if r.discovery != nil {
		r.discovery.Close()
	}

	// Close subscription
	if r.sub != nil {
		r.sub.Cancel()
	}

	// Close topic
	if r.topic != nil {
		r.topic.Close()
	}

	// Close host
	if r.host != nil {
		r.host.Close()
	}

	r.wg.Wait()
	r.running = false
	r.logger.Info("Real pubsub service stopped")
	return nil
}
