CREATE TABLE IF NOT EXISTS profile_links (
    id BIGSERIAL PRIMARY KEY,
    invite_token TEXT NOT NULL UNIQUE,
    inviter_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    invitee_user_id BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    CHECK (invitee_user_id IS NULL OR inviter_user_id <> invitee_user_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_links_active_pair
    ON profile_links (
        LEAST(inviter_user_id, invitee_user_id),
        GREATEST(inviter_user_id, invitee_user_id)
    )
    WHERE status = 'active' AND invitee_user_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS profile_link_aliases (
    id BIGSERIAL PRIMARY KEY,
    link_id BIGINT NOT NULL REFERENCES profile_links(id) ON DELETE RESTRICT,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    target_user_id BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    alias TEXT NOT NULL,
    normalized_alias TEXT NOT NULL,
    source TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id, normalized_alias),
    UNIQUE (link_id, owner_user_id, normalized_alias)
);

CREATE INDEX IF NOT EXISTS idx_profile_link_aliases_owner
    ON profile_link_aliases (owner_user_id, target_user_id);
