// Package ws is the realtime fan-out hub: activity events and per-run log
// streaming to browser clients.
package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/zypher-systems/kairos/internal/server/config"
)

// Message is the wire envelope for hub events.
type Message struct {
	Topic string `json:"topic"`
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

type client struct {
	conn      *websocket.Conn
	send      chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *client) signalClose() {
	c.closeOnce.Do(func() { close(c.closed) })
}

// Hub fans messages out to subscribed connections.
type Hub struct {
	mu      sync.RWMutex
	topics  map[string]map[*client]struct{}
	upgrade websocket.Upgrader
}

// SameHostOrigin accepts same-host Origins and requests with no Origin
// header (non-browser clients). Foreign hosts are rejected — matches the
// HTTP origin guard. publicURL is KAIROS_PUBLIC_URL (empty on the laptop).
func SameHostOrigin(r *http.Request, publicURL string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return config.OriginAllowed(origin, r.Host, publicURL)
}

// New builds a hub. publicURL is KAIROS_PUBLIC_URL for the origin check.
func New(publicURL string) *Hub {
	return &Hub{
		topics: make(map[string]map[*client]struct{}),
		upgrade: websocket.Upgrader{
			HandshakeTimeout: 5 * time.Second,
			CheckOrigin: func(r *http.Request) bool {
				return SameHostOrigin(r, publicURL)
			},
		},
	}
}

// Publish routes a message into a topic.
func (h *Hub) Publish(topic, event string, data any) {
	raw, err := json.Marshal(Message{Topic: topic, Event: event, Data: data})
	if err != nil {
		slog.Warn("ws marshal", "err", err)
		return
	}
	h.mu.RLock()
	subs := h.topics[topic]
	if len(subs) > 0 {
		for c := range subs {
			select {
			case c.send <- raw:
			default: // slow consumer: drop
				c.signalClose()
			}
		}
	}
	h.mu.RUnlock()
}

// ServeHTTP upgrades a request and subscribes to ?topics=a,b,c.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 256), closed: make(chan struct{})}

	raw := r.URL.Query().Get("topics")
	var topics []string
	if raw != "" {
		topics = splitComma(raw)
	}

	h.mu.Lock()
	for _, t := range topics {
		subs := h.topics[t]
		if subs == nil {
			subs = make(map[*client]struct{})
			h.topics[t] = subs
		}
		subs[c] = struct{}{}
	}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		for _, t := range topics {
			delete(h.topics[t], c)
			if len(h.topics[t]) == 0 {
				delete(h.topics, t)
			}
		}
		h.mu.Unlock()
		conn.Close()
	}()

	go func() {
		// read pump: discard inbound, detect close
		defer c.signalClose()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case raw, ok := <-c.send:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-c.closed:
			return
		case <-ping.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// Topic names used across the app.
const (
	TopicActivity = "activity"
)

// RunTopic names the per-run log stream topic.
func RunTopic(runID string) string { return "run:" + runID }
