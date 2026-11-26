// Package ws fans out live frames and alerts to browser WebSocket clients.
package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 25 * time.Second
	sendBuffer = 256
)

var (
	clientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vg_ws_clients", Help: "Connected WebSocket clients"})
	droppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vg_ws_dropped_messages_total", Help: "Messages dropped for slow clients"}, []string{"type"})
)

func init() { prometheus.MustRegister(clientsGauge, droppedTotal) }

// Envelope is the server->client message shape.
type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Client is one connected browser.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	UserID string
	mu     sync.RWMutex
	subs   map[string]struct{}
}

// Hub tracks clients and their camera subscriptions.
type Hub struct {
	mu       sync.RWMutex
	clients  map[*Client]struct{}
	upgrader websocket.Upgrader
}

// NewHub builds a hub accepting the given browser origins ("*" allows any).
func NewHub(origins []string) *Hub {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return &Hub{
		clients: map[*Client]struct{}{},
		upgrader: websocket.Upgrader{
			ReadBufferSize: 1024, WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				return origin == "" || allowed["*"] || allowed[origin]
			},
		},
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Serve upgrades the request and runs the client's pumps (blocks until disconnect).
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, userID string) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // upgrader already wrote the HTTP error
	}
	c := &Client{hub: h, conn: conn, send: make(chan []byte, sendBuffer), UserID: userID, subs: map[string]struct{}{}}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	clientsGauge.Inc()
	go c.writePump()
	c.readPump()
}

func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
		clientsGauge.Dec()
	}
	h.mu.Unlock()
}

// Broadcast sends a typed message to every client.
func (h *Hub) Broadcast(msgType string, data any) {
	raw, err := json.Marshal(Envelope{Type: msgType, Data: data})
	if err != nil {
		slog.Error("marshal broadcast", "err", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.enqueue(msgType, raw)
	}
}

// PublishFrame sends a raw frame JSON to subscribers of cameraID only.
func (h *Hub) PublishFrame(cameraID string, frame []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var msg []byte
	for c := range h.clients {
		if !c.subscribed(cameraID) {
			continue
		}
		if msg == nil {
			msg = append(append([]byte(`{"type":"frame","data":`), frame...), '}')
		}
		c.enqueue("frame", msg)
	}
}

func (c *Client) enqueue(msgType string, raw []byte) {
	select {
	case c.send <- raw:
	default:
		droppedTotal.WithLabelValues(msgType).Inc() // slow consumer: drop rather than block the hub
	}
}

func (c *Client) subscribed(cameraID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.subs[cameraID]
	return ok
}

type inbound struct {
	Type     string `json:"type"`
	CameraID string `json:"camera_id"`
}

func (c *Client) readPump() {
	defer func() {
		c.hub.remove(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(pongWait)) })
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		var m inbound
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "subscribe":
			if m.CameraID != "" {
				c.mu.Lock()
				if len(c.subs) < 64 {
					c.subs[m.CameraID] = struct{}{}
				}
				c.mu.Unlock()
			}
		case "unsubscribe":
			c.mu.Lock()
			delete(c.subs, m.CameraID)
			c.mu.Unlock()
		case "ping":
			c.enqueue("pong", []byte(`{"type":"pong"}`))
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
