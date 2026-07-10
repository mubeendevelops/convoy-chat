-- A dedicated column, not a reuse of updated_at: updated_at already changes
-- on soft-delete (see SoftDeleteMessage), so it can't double as a reliable
-- "has this message's content ever been edited" signal for the frontend's
-- "(edited)" indicator. NULL means never edited.
ALTER TABLE messages ADD COLUMN edited_at TIMESTAMPTZ;
