package realtime

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
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
}

type subscription struct {
	channels map[string]struct{}
}

func NewHub() *Hub {
	return &Hub{conns: make(map[string]map[*websocket.Conn]subscription)}
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
		if err := c.WriteMessage(websocket.TextMessage, payload); err != nil {
			slog.Debug("ws write", "channel", key, "err", err)
		}
	}
}
