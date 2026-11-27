-- A player who left the room (their own "Sair", removed by the host, or offline too long) is
-- marked, never deleted: deleting would cascade to the rounds in which they were the impostor or
-- the subject. Their name is free again for someone new.
ALTER TABLE players ADD COLUMN left_at timestamptz;
--bun:split
ALTER TABLE players DROP CONSTRAINT players_room_name_key;
--bun:split
CREATE UNIQUE INDEX players_room_name_key ON players (room_id, name) WHERE left_at IS NULL;
--bun:split
-- Whoever left is never the host.
ALTER TABLE players ADD CONSTRAINT players_left_not_host_check CHECK (left_at IS NULL OR NOT is_host);
