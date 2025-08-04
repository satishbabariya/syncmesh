package p2p

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"
)

// PubSubService defines the interface for pubsub functionality
type PubSubService interface {
	Publish(topic string, data []byte) error
	Subscribe(topic string) (<-chan []byte, error)
	Unsubscribe(topic string) error
}

// DHTService defines the interface for DHT functionality
type DHTService interface {
	FindPeer(ctx context.Context, peerID peer.ID) (peer.AddrInfo, error)
	FindPeersConnectedToPeer(ctx context.Context, peerID peer.ID) (<-chan peer.AddrInfo, error)
	GetClosestPeers(ctx context.Context, key string) (<-chan peer.ID, error)
}

// Protocol defines the base interface for all P2P protocols
type Protocol interface {
	Initialize() error
	Start(ctx context.Context) error
	Stop() error
}
