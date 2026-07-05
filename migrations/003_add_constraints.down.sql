ALTER TABLE rooms DROP CONSTRAINT IF EXISTS chk_rooms_type;
ALTER TABLE room_members DROP CONSTRAINT IF EXISTS chk_room_members_role;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS chk_messages_message_type;
ALTER TABLE user_presence DROP CONSTRAINT IF EXISTS chk_user_presence_status;
