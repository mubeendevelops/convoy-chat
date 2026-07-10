// Mirrors internal/models/*.go and internal/websocket/events.go. Kept in
// sync by hand — there's no codegen from the Go structs, so a backend field
// rename/addition needs the matching edit here (see CLAUDE.md's WebSocket
// event contract section for the wire shapes and their doc'd deviations from
// ConvoyChat_Complete_Context.md, e.g. room.leave/message.reaction/error/
// is_typing).

// ---- String enums ----

export type RoomType = "direct" | "group" | "channel";
export type MemberRole = "admin" | "member" | "guest";
export type MessageType = "text" | "image" | "file" | "system";
export type PresenceStatus = "online" | "away" | "offline";

// ---- Core entities ----

export interface User {
  id: string;
  username: string;
  email: string;
  avatar_url?: string;
  bio?: string;
  created_at: string;
  updated_at: string;
}

// The safe, minimal view of a user embedded in other payloads (room
// members, message authors, presence events) — never carries email.
export interface UserSummary {
  id: string;
  username: string;
  avatar_url?: string;
}

export interface Room {
  id: string;
  name?: string;
  type: RoomType;
  creator_id: string;
  description?: string;
  avatar_url?: string;
  is_archived: boolean;
  // Only meaningful for type "channel" — a public channel is listed by
  // GET /rooms/public and self-joinable via POST .../join.
  is_public: boolean;
  created_at: string;
  updated_at: string;
}

// GET /rooms/public row: a public, non-archived channel the caller isn't a
// member of yet, with its active member count — a purpose-built shape, not
// the full Room.
export interface PublicChannel {
  id: string;
  name?: string;
  description?: string;
  avatar_url?: string;
  created_at: string;
  member_count: number;
}

// Raw room_members row, e.g. the POST .../invite response.
export interface RoomMember {
  id: string;
  room_id: string;
  user_id: string;
  role: MemberRole;
  joined_at: string;
  left_at?: string;
}

// Member row joined with the user's public summary, for member-list
// responses.
export interface RoomMemberWithUser {
  user: UserSummary;
  role: MemberRole;
  joined_at: string;
}

// GET /rooms/{room_id} response: room fields plus embedded members.
export interface RoomDetail extends Room {
  members: RoomMemberWithUser[];
}

// Reactions grouped by emoji ("👍 3", not three separate rows). user_ids is
// ordered by when each user reacted.
export interface MessageReactionSummary {
  emoji: string;
  count: number;
  user_ids: string[];
}

// REST message shape (history fetch, REST send response). content is null
// when the message has been soft-deleted; the original text is retained in
// Postgres but never served back through this shape. read_by/reactions
// default to [] server-side, never null.
export interface MessageWithAuthor {
  id: string;
  room_id: string;
  user: UserSummary;
  content: string | null;
  message_type: MessageType;
  edited_at?: string;
  deleted_at?: string;
  created_at: string;
  updated_at: string;
  read_by: string[];
  reactions: MessageReactionSummary[];
}

// user_presence row — durable "last seen" snapshot. Redis (not modeled here;
// see internal/store/presence.go) is the source of truth for live status.
export interface UserPresence {
  user_id: string;
  status: PresenceStatus;
  last_seen_at?: string;
}

// ---- REST request/response bodies (internal/handlers/*.go) ----

// Returned by signup/login/refresh alike — all three issue a fresh
// access+refresh pair (Phase 3: refresh tokens). refresh_token is opaque to
// the client (a random value, not a JWT) — it's only ever sent back verbatim
// to POST /auth/refresh or POST /auth/logout, never decoded client-side.
export interface AuthResponse {
  token: string;
  refresh_token: string;
  user: User;
}

export interface SignupRequest {
  username: string;
  email: string;
  password: string;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RefreshRequest {
  refresh_token: string;
}

export interface LogoutRequest {
  refresh_token: string;
}

export interface LogoutResponse {
  status: "logged_out";
}

// POST /rooms discriminates on `type`; the backend rejects any other value
// (e.g. "group" is schema-supported but not creatable in v1).
export type CreateRoomRequest =
  | { type: "channel"; name: string; description?: string; is_public?: boolean }
  | { type: "direct"; peer_user_id: string };

export interface InviteMemberRequest {
  user_id: string;
}

export interface LeaveRoomResponse {
  status: "left";
}

export interface SendMessageRequest {
  content: string;
  message_type?: MessageType;
}

export interface DeleteMessageResponse {
  status: "deleted";
}

export interface EditMessageRequest {
  content: string;
}

// PATCH /messages/{id} response — a minimal, purpose-built shape rather than
// the full MessageWithAuthor (see internal/handlers/messages.go): content
// and edited_at are the only fields an edit changes.
export interface EditMessageResponse {
  id: string;
  room_id: string;
  content: string;
  edited_at: string;
}

export interface ToggleReactionRequest {
  emoji: string;
}

export interface ToggleReactionResponse {
  status: "added" | "removed";
  emoji: string;
}

// {"error":{"code","message"}} — the one JSON error shape every REST
// endpoint uses (internal/httpx/response.go).
export type ApiErrorCode =
  | "invalid_input"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "internal_error";

export interface ApiErrorBody {
  error: {
    code: ApiErrorCode;
    message: string;
  };
}

// ---- WebSocket events (internal/websocket/events.go) ----
// Discriminated unions on `type`, matching CLAUDE.md's WebSocket event
// contract exactly — switch on `.type` to narrow.

export type ClientEvent =
  | { type: "room.join"; room_id: string }
  | { type: "room.leave"; room_id: string }
  | {
      type: "message.send";
      room_id: string;
      content: string;
      message_type?: MessageType;
      // Optional client-generated nonce (a deviation beyond the context file,
      // like room.leave/is_typing) echoed back in message.new so the sender
      // can match its optimistic bubble to the broadcast, which carries the
      // real DB id, not this nonce.
      client_id?: string;
    }
  | { type: "typing.start"; room_id: string }
  | { type: "typing.stop"; room_id: string }
  | { type: "message.read"; message_id: string }
  | { type: "presence.update"; status: PresenceStatus };

// message.new's payload is deliberately not MessageWithAuthor: it omits
// message_type/updated_at and read_by is always [] (a message can't have
// been read by anyone at the instant it's broadcast) — see CLAUDE.md.
// client_id is present only on the echo of a message.send that carried a
// nonce (absent otherwise, via the backend's omitempty).
export interface WsMessage {
  id: string;
  room_id: string;
  user: UserSummary;
  content: string | null;
  created_at: string;
  read_by: string[];
  client_id?: string;
}

// Inbound-dispatch error codes (internal/websocket/handlers.go +
// dispatch()) — a distinct set from ApiErrorCode: no unauthorized/conflict
// over the socket (auth is checked pre-upgrade), plus WS-only "unsupported".
export type WsErrorCode =
  | "invalid_input"
  | "forbidden"
  | "not_found"
  | "internal_error"
  | "unsupported";

export type ServerEvent =
  | { type: "message.new"; message: WsMessage }
  | {
      type: "user.joined";
      user: { id: string; username: string };
      room_id: string;
    }
  | { type: "user.left"; user_id: string; room_id: string }
  | {
      type: "user.typing";
      user_id: string;
      room_id: string;
      is_typing: boolean;
    }
  | {
      type: "user.status_changed";
      user_id: string;
      status: PresenceStatus;
      last_seen_at: string;
    }
  | { type: "message.read_by"; message_id: string; read_by_user_id: string }
  | {
      type: "message.reaction";
      message_id: string;
      user_id: string;
      emoji: string;
      action: "added" | "removed";
    }
  | { type: "message.edited"; id: string; room_id: string; content: string; edited_at: string }
  | { type: "error"; code: WsErrorCode; message: string };
