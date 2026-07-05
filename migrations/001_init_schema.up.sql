-- Users
CREATE TABLE users (
    id            UUID PRIMARY KEY,
    username      VARCHAR(255) UNIQUE NOT NULL,
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar_url    TEXT,
    bio           TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Chat rooms: direct messages, groups, and channels
CREATE TABLE rooms (
    id          UUID PRIMARY KEY,
    name        VARCHAR(255),
    type        VARCHAR(50) NOT NULL,
    creator_id  UUID NOT NULL REFERENCES users(id),
    description TEXT,
    avatar_url  TEXT,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Room membership with roles
CREATE TABLE room_members (
    id        UUID PRIMARY KEY,
    room_id   UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      VARCHAR(50) NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at   TIMESTAMPTZ,

    UNIQUE (room_id, user_id)
);

-- Messages: append-only, soft-deleted via deleted_at
CREATE TABLE messages (
    id           UUID PRIMARY KEY,
    room_id      UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id),
    content      TEXT NOT NULL,
    message_type VARCHAR(50) NOT NULL DEFAULT 'text',
    metadata     JSONB,
    deleted_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Emoji reactions on messages
CREATE TABLE message_reactions (
    id         UUID PRIMARY KEY,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji      VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (message_id, user_id, emoji)
);

-- Read receipts: who has read which message
CREATE TABLE message_read_receipts (
    id         UUID PRIMARY KEY,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (message_id, user_id)
);

-- Presence snapshot; Redis (TTL keys + heartbeat) is the source of truth for
-- "is this user online right now", this table mirrors last_seen_at for
-- durability and for showing "last seen" when a user is offline.
CREATE TABLE user_presence (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status          VARCHAR(50) NOT NULL DEFAULT 'offline',
    last_seen_at    TIMESTAMPTZ,
    current_room_id UUID REFERENCES rooms(id) ON DELETE SET NULL
);
