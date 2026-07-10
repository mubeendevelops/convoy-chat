-- A wholly separate authority level from room_members.role's 'admin' CHECK
-- value (per-room, see CLAUDE.md) — named is_system_admin, not is_admin, to
-- avoid confusing the two. No index: admin counts are small enough that a
-- full scan is fine, consistent with not adding speculative indices.
ALTER TABLE users ADD COLUMN is_system_admin BOOLEAN NOT NULL DEFAULT false;
