-- A kicked member is banned from rejoining a channel on their own: banned_at
-- is stamped alongside left_at when an admin removes them (store.BanMember).
-- Nullable, mirroring left_at's soft-state idiom — NULL means "not banned".
-- A plain self-leave (store.RemoveMember) never sets it, and an admin re-invite
-- (store.AddMember) clears it, so a ban is lifted by exactly one action.
-- No index: the ban check is always alongside the existing UNIQUE(room_id,
-- user_id) lookup, which the unique constraint already covers.
ALTER TABLE room_members ADD COLUMN banned_at TIMESTAMPTZ;
