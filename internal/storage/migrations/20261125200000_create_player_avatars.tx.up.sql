-- A player's optional photo: a JPEG re-encoded by the server, at most 512x512 pixels and
-- 150 KiB. It lives apart from players so the usual player queries never read the image, and it
-- goes away with the player (and so with the room).
CREATE TABLE player_avatars (
    player_id  uuid PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE,
    image      bytea NOT NULL,
    -- Changes with every new photo; the web app uses it to bust the browser cache.
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT player_avatars_image_size_check CHECK (octet_length(image) BETWEEN 1 AND 153600)
);
