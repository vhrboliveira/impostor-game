-- When the player first opened the room (WebSocket). Only players who connected, or who joined a
-- moment ago, hold a seat against the room's player limit: joins that never open the room cannot
-- keep it full.
ALTER TABLE players ADD COLUMN connected_at timestamptz;
