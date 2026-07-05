-- rooms
CREATE INDEX idx_rooms_type ON rooms (type);
CREATE INDEX idx_rooms_creator_id ON rooms (creator_id);

-- room_members
CREATE INDEX idx_room_members_user_id ON room_members (user_id);
CREATE INDEX idx_room_members_room_id_joined_at ON room_members (room_id, joined_at);

-- messages
CREATE INDEX idx_messages_room_id_created_at ON messages (room_id, created_at DESC);
CREATE INDEX idx_messages_user_id ON messages (user_id);
CREATE INDEX idx_messages_created_at ON messages (created_at DESC);

-- message_reactions
CREATE INDEX idx_message_reactions_message_id ON message_reactions (message_id);

-- message_read_receipts
CREATE INDEX idx_message_read_receipts_user_id_read_at ON message_read_receipts (user_id, read_at DESC);

-- user_presence
CREATE INDEX idx_user_presence_status_last_seen_at ON user_presence (status, last_seen_at DESC);
