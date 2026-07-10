-- Opaque refresh tokens: only the SHA-256 hash of the token is stored, never
-- the plaintext (which the client holds and presents on /auth/refresh).
-- family_id groups every token issued from one login through its rotation
-- chain, so a stolen-and-replayed old token can revoke the whole chain at
-- once rather than just itself.
CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    family_id  UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens (family_id);

-- Speeds up "revoke every session for a user" (not exposed by any v1
-- endpoint yet, same schema-ready spirit as is_archived/group/guest).
CREATE INDEX idx_refresh_tokens_user_active ON refresh_tokens (user_id) WHERE revoked_at IS NULL;
