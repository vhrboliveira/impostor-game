-- Photos grew to 768x768 so the impostor's reveal stays sharp on high-density screens: the
-- stored JPEG may now take up to 500 KiB.
ALTER TABLE player_avatars DROP CONSTRAINT player_avatars_image_size_check;
--bun:split
ALTER TABLE player_avatars ADD CONSTRAINT player_avatars_image_size_check
    CHECK (octet_length(image) BETWEEN 1 AND 512000);
