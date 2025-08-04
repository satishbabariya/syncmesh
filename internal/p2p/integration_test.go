package p2p

import (
	"context"
	"testing"
	"time"

	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestP2PNodeCreation(t *testing.T) {
	// Create P2P config
	p2pConfig := &config.P2PConfig{
		Enabled: true,
		Host:    "127.0.0.1",
		Port:    4001,
		Discovery: config.DiscoveryConfig{
			MDNS: true,
			DHT:  false,
		},
		PubSub: config.PubSubConfig{
			FileSyncTopic: "syncmesh-file-sync",
			ClusterTopic:  "syncmesh-cluster",
		},
		FileSync: config.FileSyncConfig{
			ConflictResolution: "timestamp",
			Compression:        true,
			ChunkSize:          1048576,
			MaxFileSize:        1073741824,
		},
		Cluster: config.P2PClusterConfig{
			HeartbeatInterval: 30 * time.Second,
			ElectionTimeout:   5 * time.Second,
			ConsensusTimeout:  10 * time.Second,
		},
	}

	// Create P2P node
	node, err := NewP2PNode(p2pConfig)
	assert.NoError(t, err)
	assert.NotNil(t, node)

	// Test node properties
	assert.NotEmpty(t, node.GetNodeID())
	assert.NotNil(t, node.host)
	assert.NotNil(t, node.pubsub)

	// Test starting the node
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = node.Start(ctx)
	assert.NoError(t, err)

	// Test node is running
	status := node.GetClusterStatus()
	assert.NotNil(t, status)
	assert.Equal(t, node.GetNodeID(), status["node_id"])

	// Test stopping the node
	node.Stop()
}

func TestFileSyncProtocol(t *testing.T) {
	// Create P2P config
	p2pConfig := &config.P2PConfig{
		Enabled: true,
		Host:    "127.0.0.1",
		Port:    4002,
		PubSub: config.PubSubConfig{
			FileSyncTopic: "syncmesh-file-sync",
		},
		FileSync: config.FileSyncConfig{
			ConflictResolution: "timestamp",
		},
	}

	// Create P2P node
	node, err := NewP2PNode(p2pConfig)
	assert.NoError(t, err)

	// Test file sync protocol
	fileSync := NewFileSyncProtocol(node)
	assert.NotNil(t, fileSync)

	// Test initialization
	err = fileSync.Initialize()
	assert.NoError(t, err)

	// Test starting
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = fileSync.Start(ctx)
	assert.NoError(t, err)

	// Test stopping
	fileSync.Stop()
}

func TestClusterProtocol(t *testing.T) {
	// Create P2P config
	p2pConfig := &config.P2PConfig{
		Enabled: true,
		Host:    "127.0.0.1",
		Port:    4003,
		PubSub: config.PubSubConfig{
			ClusterTopic: "syncmesh-cluster",
		},
		Cluster: config.P2PClusterConfig{
			HeartbeatInterval: 30 * time.Second,
			ElectionTimeout:   5 * time.Second,
		},
	}

	// Create P2P node
	node, err := NewP2PNode(p2pConfig)
	assert.NoError(t, err)

	// Test cluster protocol
	clusterSync := NewClusterProtocol(node)
	assert.NotNil(t, clusterSync)

	// Test initialization
	err = clusterSync.Initialize()
	assert.NoError(t, err)

	// Test starting
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = clusterSync.Start(ctx)
	assert.NoError(t, err)

	// Test cluster state
	state := clusterSync.GetClusterState()
	assert.NotNil(t, state)

	// Test stopping
	clusterSync.Stop()
}
