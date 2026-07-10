DROP INDEX IF EXISTS idx_rooms_type_is_public;
ALTER TABLE rooms DROP COLUMN is_public;
