ALTER TABLE players DROP CONSTRAINT players_left_not_host_check;
--bun:split
DROP INDEX players_room_name_key;
--bun:split
-- Players who left would clash with the names taken after them.
DELETE FROM players WHERE left_at IS NOT NULL;
--bun:split
ALTER TABLE players ADD CONSTRAINT players_room_name_key UNIQUE (room_id, name);
--bun:split
ALTER TABLE players DROP COLUMN left_at;
