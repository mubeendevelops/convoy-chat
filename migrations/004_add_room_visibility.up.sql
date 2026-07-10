ALTER TABLE rooms ADD COLUMN is_public BOOLEAN NOT NULL DEFAULT true;

-- Speeds up the browse-public-channels query (WHERE type='channel' AND
-- is_public); partial since only public rows are ever selected by it.
CREATE INDEX idx_rooms_type_is_public ON rooms (type, is_public) WHERE is_public = true;
