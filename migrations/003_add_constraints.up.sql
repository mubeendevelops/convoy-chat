ALTER TABLE rooms
    ADD CONSTRAINT chk_rooms_type CHECK (type IN ('direct', 'group', 'channel'));

ALTER TABLE room_members
    ADD CONSTRAINT chk_room_members_role CHECK (role IN ('admin', 'member', 'guest'));

ALTER TABLE messages
    ADD CONSTRAINT chk_messages_message_type CHECK (message_type IN ('text', 'image', 'file', 'system'));

ALTER TABLE user_presence
    ADD CONSTRAINT chk_user_presence_status CHECK (status IN ('online', 'away', 'offline'));
