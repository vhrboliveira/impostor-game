-- Photos over the old limit cannot stay; their players fall back to the initials.
DELETE FROM player_avatars WHERE octet_length(image) > 153600;
--bun:split
ALTER TABLE player_avatars DROP CONSTRAINT player_avatars_image_size_check;
--bun:split
ALTER TABLE player_avatars ADD CONSTRAINT player_avatars_image_size_check
    CHECK (octet_length(image) BETWEEN 1 AND 153600);
