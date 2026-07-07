package websocket

// Client → server event type tags. Only the room-membership events are handled
// in the connection-layer phase; message.send / typing / presence and their
// tags arrive with the message-routing step, alongside their handlers.
const (
	eventRoomJoin  = "room.join"
	eventRoomLeave = "room.leave"
)

// Server → client event type tags.
const (
	eventError = "error"
)

// inboundEnvelope is the minimal shape needed to route a client frame this
// phase: the discriminating type plus the room it targets. Fields specific to
// message.send / typing / etc. are decoded by their own handlers next phase.
type inboundEnvelope struct {
	Type   string `json:"type"`
	RoomID string `json:"room_id"`
}

// errorEvent is the server's {"type":"error","code","message"} envelope, per
// the WebSocket contract in CLAUDE.md.
type errorEvent struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
