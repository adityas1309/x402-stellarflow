package api

import (
	"sync"
	"time"
)

// FeedEvent is one item pushed to operator dashboards over SSE.
// Mirrors the most useful subset of an agent_calls row plus a few derived
// fields for display.
type FeedEvent struct {
	ID            int64     `json:"id"`
	OrgID         int64     `json:"org_id"`
	PayerAddress  string    `json:"payer_address"`
	Endpoint      string    `json:"endpoint"`
	PriceUSDC     float64   `json:"price_usdc"`
	Facilitator   string    `json:"facilitator"`
	Status        string    `json:"status"`
	ClientID      string    `json:"client_id,omitempty"`
	Reasoning     string    `json:"reasoning,omitempty"`
	TxHash        string    `json:"tx_hash,omitempty"`
	DurationMs    int       `json:"duration_ms,omitempty"`
	ResponseSize  int       `json:"response_size,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// FeedHub is a tiny in-process pub/sub: middleware publishes FeedEvents,
// SSE handlers subscribe and forward to connected clients. No persistence —
// the feed is "what's happening right now". Historical data is read from
// agent_calls via a separate REST endpoint (handler_feed.go uses both).
type FeedHub struct {
	mu          sync.RWMutex
	subscribers map[chan FeedEvent]struct{}
}

func NewFeedHub() *FeedHub {
	return &FeedHub{
		subscribers: make(map[chan FeedEvent]struct{}),
	}
}

// Subscribe returns a buffered channel that receives every event published
// after the call. Buffer size 16 — slow consumers drop events on overflow.
// The unsubscribe func MUST be called to release the channel.
func (h *FeedHub) Subscribe() (<-chan FeedEvent, func()) {
	ch := make(chan FeedEvent, 16)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
		close(ch)
	}
}

// Publish fans out an event to all current subscribers. Non-blocking: if a
// subscriber's buffer is full, the event is dropped for that subscriber only.
// Safe to call from any goroutine.
func (h *FeedHub) Publish(ev FeedEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- ev:
		default:
			// drop — slow consumer
		}
	}
}

// SubscriberCount returns the number of currently connected SSE clients.
// Useful for diagnostics and the operator dashboard health badge.
func (h *FeedHub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
