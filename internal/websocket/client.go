package websocket

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	gws "github.com/gorilla/websocket"
)

const (
	// writeWait is the time allowed to write a single frame to the peer.
	writeWait = 10 * time.Second
	// pongWait is how long we wait for a pong (or any read) before treating the
	// connection as dead.
	pongWait = 60 * time.Second
	// pingPeriod is how often we ping the peer. Must be < pongWait so a missed
	// pong is noticed before the read deadline lapses.
	pingPeriod = (pongWait * 9) / 10
	// maxMessageSize caps a single inbound frame. Sized to comfortably hold a
	// message.send envelope (content up to 10k UTF-8 chars) once that lands.
	maxMessageSize = 64 * 1024
	// sendBuffer is the per-client outbound queue depth. A client that lets it
	// fill (a slow consumer) is dropped by the Hub rather than stalling it.
	sendBuffer = 256
)

// Client is one WebSocket connection. Its two pumps (readPump, writePump) are
// the only goroutines that touch the socket; all shared state lives in the Hub.
//
// rooms is owned exclusively by the Hub goroutine — it is mutated only in
// addToRoom / removeFromRoom / removeClient, and the pumps never touch it.
type Client struct {
	hub    *Hub
	conn   *gws.Conn
	userID uuid.UUID
	logger *slog.Logger

	// send is the outbound queue. The Hub — and only the Hub — closes it, which
	// tells writePump to flush a close frame and exit.
	send chan []byte

	// rooms — Hub-goroutine-owned. See the type doc above.
	rooms map[uuid.UUID]struct{}
}

func newClient(hub *Hub, conn *gws.Conn, userID uuid.UUID, logger *slog.Logger) *Client {
	return &Client{
		hub:    hub,
		conn:   conn,
		userID: userID,
		logger: logger,
		send:   make(chan []byte, sendBuffer),
		rooms:  make(map[uuid.UUID]struct{}),
	}
}

// readPump reads frames and dispatches them. It runs in its own goroutine and
// owns all reads. On return — any read error, including a clean close or a
// lapsed read deadline — it unregisters the client and closes the connection.
// This is the single place that initiates teardown.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if gws.IsUnexpectedCloseError(err, gws.CloseGoingAway, gws.CloseNormalClosure) {
				c.logger.Warn("ws read error", "user_id", c.userID, "error", err)
			}
			return
		}
		c.dispatch(data)
	}
}

// dispatch handles a single inbound frame. This phase understands only the
// room-membership envelopes (room.join / room.leave); message.send, typing, and
// presence arrive with the message-routing step. Unknown or not-yet-supported
// types get an error envelope rather than a silent drop.
func (c *Client) dispatch(data []byte) {
	var env inboundEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.sendError("invalid_input", "message must be a JSON object with a type field")
		return
	}

	switch env.Type {
	case eventRoomJoin, eventRoomLeave:
		roomID, err := uuid.Parse(env.RoomID)
		if err != nil {
			c.sendError("invalid_input", "room_id must be a valid UUID")
			return
		}
		if env.Type == eventRoomJoin {
			c.hub.Join(c, roomID)
		} else {
			c.hub.Leave(c, roomID)
		}
	case "":
		c.sendError("invalid_input", "missing event type")
	default:
		c.sendError("unsupported", "event type not supported yet: "+env.Type)
	}
}

// writePump owns all writes to the connection: it drains the send queue and
// emits periodic pings for keepalive. It exits when the Hub closes send (a
// clean unregister) or any write fails, closing the connection on the way out.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the queue: send a clean close frame and stop.
				_ = c.conn.WriteMessage(gws.CloseMessage, gws.FormatCloseMessage(gws.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(gws.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(gws.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// sendError best-effort enqueues an error envelope. It never blocks the read
// loop: if the send queue is full the client is already being dropped, so the
// notice is simply discarded.
func (c *Client) sendError(code, message string) {
	payload, err := json.Marshal(errorEvent{Type: eventError, Code: code, Message: message})
	if err != nil {
		return
	}
	select {
	case c.send <- payload:
	default:
	}
}
