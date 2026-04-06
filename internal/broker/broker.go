// Package broker implements a fan-out hub for Server-Sent Events clients.
// Broadcast sends a JSON payload to every registered client channel using
// non-blocking sends; a client that cannot keep up is silently dropped.
package broker

import "sync"

const channelBuf = 8

// Broker fans serialised JSON bytes out to any number of SSE clients.
type Broker struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

// New returns an initialised Broker.
func New() *Broker {
	return &Broker{clients: make(map[chan []byte]struct{})}
}

// Register allocates a new buffered channel for one SSE client and returns it.
func (b *Broker) Register() <-chan []byte {
	ch := make(chan []byte, channelBuf)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unregister removes the channel from the hub and closes it so the SSE handler
// goroutine exits its range loop.
func (b *Broker) Unregister(ch <-chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for c := range b.clients {
		if c == ch {
			delete(b.clients, c)
			close(c)
			return
		}
	}
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
			}
		}
		b.mu.Unlock()
	}
}
