# 3. One actor goroutine per room

Status: accepted

## Context
A room receives concurrent events: players' messages, joins, disconnections and timers. The
game state must stay consistent without scattering locks.

## Decision
Each active room is owned by a single goroutine that processes commands from a channel in order.
Hot state is kept in memory and written through to PostgreSQL. After a restart a room is reloaded
from the database on its first connection and its timers are re-armed from stored deadlines.

## Consequences
No locks around room state and deterministic ordering, which makes the room logic easy to test.
Scaling beyond one instance needs sticky routing or a cross-instance channel.
