package realtime

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait bounds a single write; pongWait is the read deadline a pong must
	// renew; PingPeriod (exported for the handler's heartbeat goroutine) must be
	// < pongWait. Mirrors shared/services/notifications and the contract-management
	// hub so heartbeat timing is uniform across the platform.
	writeWait  = 10 * time.Second
	PongWait   = 60 * time.Second
	PingPeriod = (PongWait * 9) / 10
)

type WorkspacePush struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	Version int64           `json:"version"`
}

// Channel is the addressable target of a realtime broadcast. The hub
// fans messages out to every connection subscribed to the channel
// string. Use the helper constructors (UserChannel, WorkspaceChannel,
// ProjectChannel) so the encoded keys stay consistent across packages.
type Channel struct {
	Kind string
	ID   string
}

// String returns the canonical wire key, e.g. "user:abc" or
// "project:42:design". Used as the map key inside Hub and as the
// identifier on the Redis pub/sub bridge.
func (c Channel) String() string {
	if c.Kind == "" || c.ID == "" {
		return ""
	}
	return c.Kind + ":" + c.ID
}

func UserChannel(userID string) Channel       { return Channel{Kind: "user", ID: userID} }
func WorkspaceChannel(ownerID string) Channel { return Channel{Kind: "workspace", ID: ownerID} }
func ProjectChannel(ownerID, projectID string) Channel {
	return Channel{Kind: "project", ID: ownerID + ":" + projectID}
}

// ParseChannel inverts Channel.String. Returns an empty Channel for an
// unparsable input.
func ParseChannel(key string) Channel {
	i := strings.IndexByte(key, ':')
	if i <= 0 {
		return Channel{}
	}
	return Channel{Kind: key[:i], ID: key[i+1:]}
}

type Hub struct {
	mu    sync.RWMutex
	conns map[string]map[*websocket.Conn]subscription
	// writeMu holds one mutex per live connection. gorilla allows only one
	// concurrent writer per conn, so every write (snapshot, broadcast, ping)
	// must serialize through it — without this, two concurrent broadcasts (or a
	// broadcast racing the heartbeat ping) corrupt the frame stream.
	writeMu map[*websocket.Conn]*sync.Mutex
}

type subscription struct {
	channels map[string]struct{}
}

func NewHub() *Hub {
	return &Hub{
		conns:   make(map[string]map[*websocket.Conn]subscription),
		writeMu: make(map[*websocket.Conn]*sync.Mutex),
	}
}

// WriteText sends a text frame to conn under its per-connection write lock.
func (h *Hub) WriteText(conn *websocket.Conn, payload []byte) error {
	return h.writeFrame(conn, websocket.TextMessage, payload)
}

// Ping sends a websocket control ping to conn under its per-connection write
// lock. Used by the handler's heartbeat goroutine.
func (h *Hub) Ping(conn *websocket.Conn) error {
	return h.writeFrame(conn, websocket.PingMessage, nil)
}

func (h *Hub) writeFrame(conn *websocket.Conn, messageType int, payload []byte) error {
	h.mu.RLock()
	mu := h.writeMu[conn]
	h.mu.RUnlock()
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteMessage(messageType, payload)
}

// Register subscribes a connection to the user's primary channel. Kept
// for backwards compatibility with the existing call sites; equivalent
// to RegisterChannel(UserChannel(userID), conn) but also subscribes to
// the workspace channel for the same id (current behavior is that the
// owner sees workspace mutations).
func (h *Hub) Register(userID string, conn *websocket.Conn) {
	h.RegisterChannel(UserChannel(userID), conn)
	h.RegisterChannel(WorkspaceChannel(userID), conn)
}

// Unregister removes the connection from every channel it joined.
func (h *Hub) Unregister(userID string, conn *websocket.Conn) {
	h.UnregisterAll(conn)
}

// RegisterChannel subscribes a connection to one specific channel. A
// single connection can subscribe to multiple channels; the hub tracks
// the set so UnregisterAll cleans up cleanly on disconnect.
func (h *Hub) RegisterChannel(ch Channel, conn *websocket.Conn) {
	key := ch.String()
	if key == "" || conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	subs, ok := h.conns[key]
	if !ok {
		subs = map[*websocket.Conn]subscription{}
		h.conns[key] = subs
	}
	s, ok := subs[conn]
	if !ok {
		s = subscription{channels: map[string]struct{}{}}
	}
	s.channels[key] = struct{}{}
	subs[conn] = s
	if _, ok := h.writeMu[conn]; !ok {
		h.writeMu[conn] = &sync.Mutex{}
	}
}

// UnregisterChannel removes a connection from a single channel.
func (h *Hub) UnregisterChannel(ch Channel, conn *websocket.Conn) {
	key := ch.String()
	if key == "" || conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.conns[key]; ok {
		delete(subs, conn)
		if len(subs) == 0 {
			delete(h.conns, key)
		}
	}
}

// UnregisterAll drops the connection from every channel it was on.
// Call on websocket disconnect.
func (h *Hub) UnregisterAll(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, subs := range h.conns {
		if _, ok := subs[conn]; ok {
			delete(subs, conn)
			if len(subs) == 0 {
				delete(h.conns, key)
			}
		}
	}
	delete(h.writeMu, conn)
}

// Broadcast publishes a workspace push to every connection on the
// user's workspace channel. Legacy entrypoint — new callers should
// prefer BroadcastChannel for explicit channel addressing.
func (h *Hub) Broadcast(userID string, push WorkspacePush) {
	h.BroadcastChannel(WorkspaceChannel(userID), push)
}

// BroadcastChannel fans the push out to every connection on the given
// channel. Marshals once; writes are best-effort per connection.
func (h *Hub) BroadcastChannel(ch Channel, push WorkspacePush) {
	key := ch.String()
	if key == "" {
		return
	}
	payload, err := json.Marshal(push)
	if err != nil {
		return
	}
	h.mu.RLock()
	subs := h.conns[key]
	conns := make([]*websocket.Conn, 0, len(subs))
	for c := range subs {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		if err := h.WriteText(c, payload); err != nil {
			slog.Debug("ws write", "channel", key, "err", err)
		}
	}
}
