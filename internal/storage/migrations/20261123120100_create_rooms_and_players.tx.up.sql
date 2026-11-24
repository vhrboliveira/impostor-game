-- Rooms and the players inside them. Everything else hangs from rooms with ON DELETE CASCADE,
-- so deleting an expired room removes its whole game.

CREATE TABLE rooms (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- created_at + ROOM_TTL, set by the application.
    expires_at  timestamptz NOT NULL,
    -- Set when the last player leaves, cleared when someone returns.
    empty_since timestamptz,
    CONSTRAINT rooms_code_key UNIQUE (code)
);
--bun:split
CREATE INDEX rooms_expires_at_idx ON rooms (expires_at);
--bun:split
CREATE INDEX rooms_empty_since_idx ON rooms (empty_since) WHERE empty_since IS NOT NULL;
--bun:split
CREATE TABLE players (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id      uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    name         text NOT NULL,
    is_host      boolean NOT NULL DEFAULT false,
    -- SHA-256 of the session token; the token itself is never stored.
    token_hash   text NOT NULL,
    joined_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT players_room_name_key UNIQUE (room_id, name)
);
--bun:split
-- At most one host per room.
CREATE UNIQUE INDEX players_one_host_per_room_idx ON players (room_id) WHERE is_host;
