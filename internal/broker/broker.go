// Package broker implements a fan-out hub for Server-Sent Events clients.
// Broadcast sends a JSON payload to every registered client channel using
// non-blocking sends; a client that cannot keep up is silently dropped.
package broker

import (
	"sync"
	"sync/atomic"
)

const (
	channelBuf = 8
	// maxClients is the maximum number of concurrent SSE connections.
	// Register returns false when this limit is reached.
	maxClients = 100
)

// Broker fans serialised JSON bytes out to any number of SSE clients.
type Broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
	count   atomic.Int32
}

// Client is a handle for a registered SSE client. The channel returned by
// Recv must be ranged or selected on until it is closed; Unregister must be
// called when the client disconnects.
type Client struct {
	b  *Broker
	ch chan []byte
}

// Recv returns the receive-only view of the client's channel.
func (c *Client) Recv() <-chan []byte { return c.ch }

// Unregister removes the client from the broker and closes its channel so
// any receiver exits its range/select loop. Safe to call multiple times.
func (c *Client) Unregister() {
	c.b.mu.Lock()
	defer c.b.mu.Unlock()
	if _, ok := c.b.clients[c.ch]; !ok {
		return
	}
	delete(c.b.clients, c.ch)
	close(c.ch)
	c.b.count.Add(-1)
}

// New returns an initialised Broker.
func New() *Broker {
	return &Broker{clients: make(map[chan []byte]struct{})}
}

// Register allocates a new buffered channel for one SSE client.
// Returns (client, true) on success, or (nil, false) if the connection cap
// has been reached.
func (b *Broker) Register() (*Client, bool) {
	if b.count.Add(1) > maxClients {
		b.count.Add(-1)
		return nil, false
	}
	ch := make(chan []byte, channelBuf)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return &Client{b: b, ch: ch}, true
}

// Broadcast delivers msg to every registered client. Slow clients that have
// filled their buffer are dropped (unregistered) rather than blocking the
// caller.
func (b *Broker) Broadcast(msg []byte) {
	b.mu.RLock()
	snapshot := make([]chan []byte, 0, len(b.clients))
	for c := range b.clients {
		snapshot = append(snapshot, c)
	}
	b.mu.RUnlock()

	var slow []chan []byte
	for _, c := range snapshot {
		select {
		case c <- msg:
		default:
			slow = append(slow, c)
		}
	}

	if len(slow) > 0 {
		b.mu.Lock()
		for _, c := range slow {
			if _, ok := b.clients[c]; ok {
				delete(b.clients, c)
				close(c)
				b.count.Add(-1)
			}
		}
		b.mu.Unlock()
	}
}
