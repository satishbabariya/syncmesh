package p2p

import (
	"context"
	"sync"
)

// MockPubSubService provides a mock implementation of pubsub functionality
type MockPubSubService struct {
	topics map[string]*MockTopic
	mutex  sync.RWMutex
}

// MockTopic represents a mock pubsub topic
type MockTopic struct {
	name     string
	subs     []*MockSubscription
	mutex    sync.RWMutex
	messages chan []byte
}

// MockSubscription represents a mock pubsub subscription
type MockSubscription struct {
	topic   *MockTopic
	ctx     context.Context
	cancel  context.CancelFunc
	channel chan []byte
}

// NewMockPubSubService creates a new mock pubsub service
func NewMockPubSubService() *MockPubSubService {
	return &MockPubSubService{
		topics: make(map[string]*MockTopic),
	}
}

// Join joins a topic
func (m *MockPubSubService) Join(topicName string) (*MockTopic, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	topic, exists := m.topics[topicName]
	if !exists {
		topic = &MockTopic{
			name:     topicName,
			subs:     make([]*MockSubscription, 0),
			messages: make(chan []byte, 100),
		}
		m.topics[topicName] = topic
	}

	return topic, nil
}

// Publish publishes a message to a topic
func (t *MockTopic) Publish(ctx context.Context, data []byte) error {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	// Send to all subscribers
	for _, sub := range t.subs {
		select {
		case sub.channel <- data:
		default:
			// Channel is full, skip this subscriber
		}
	}

	return nil
}

// Subscribe subscribes to a topic
func (t *MockTopic) Subscribe() (*MockSubscription, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sub := &MockSubscription{
		topic:   t,
		ctx:     ctx,
		cancel:  cancel,
		channel: make(chan []byte, 10),
	}

	t.subs = append(t.subs, sub)
	return sub, nil
}

// Next gets the next message from the subscription
func (s *MockSubscription) Next(ctx context.Context) (*MockMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case data := <-s.channel:
		return &MockMessage{
			Data:         data,
			ReceivedFrom: "mock-peer-id",
		}, nil
	}
}

// Cancel cancels the subscription
func (s *MockSubscription) Cancel() {
	s.cancel()
}

// Close closes the topic
func (t *MockTopic) Close() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	for _, sub := range t.subs {
		sub.Cancel()
	}
}

// MockMessage represents a mock pubsub message
type MockMessage struct {
	Data         []byte
	ReceivedFrom string
}
