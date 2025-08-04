package p2p

import (
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// FileEvent represents a file system event in the P2P network
type FileEvent struct {
	Path      string
	Operation string // create, modify, delete
	Timestamp time.Time
	Size      int64
	Checksum  string
	FromPeer  peer.ID
	Version   uint64
	Metadata  map[string]string
}

// FileOperation represents a file operation to be processed
type FileOperation struct {
	Event       FileEvent
	Priority    int
	RetryCount  int
	LastAttempt time.Time
}

// FileWorker represents a worker for processing file operations
type FileWorker struct {
	ID       int
	queue    chan FileOperation
	stopChan chan struct{}
}

// ConsensusState represents the consensus state for cluster coordination
type ConsensusState int

const (
	FOLLOWER ConsensusState = iota
	CANDIDATE
	LEADER
)

// P2PConsensus represents P2P-based consensus (replaces Raft)
type P2PConsensus struct {
	State         ConsensusState
	Term          uint64
	Leader        peer.ID
	Votes         map[peer.ID]bool
	ElectionTimer *time.Timer
}

// ClusterMessage represents a cluster coordination message
type ClusterMessage struct {
	Type      string            `json:"type"`
	PeerID    string            `json:"peer_id"`
	Metadata  map[string]string `json:"metadata"`
	Timestamp int64             `json:"timestamp"`
}

// FileSyncMessage represents a file synchronization message
type FileSyncMessage struct {
	Type      string            `json:"type"`
	FilePath  string            `json:"file_path"`
	FileData  []byte            `json:"file_data,omitempty"`
	Checksum  string            `json:"checksum"`
	Size      int64             `json:"size"`
	Timestamp int64             `json:"timestamp"`
	FromPeer  string            `json:"from_peer"`
	Version   uint64            `json:"version"`
	Metadata  map[string]string `json:"metadata"`
}
