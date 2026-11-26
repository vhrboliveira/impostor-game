// Package realtime is the WebSocket transport: it validates the origin, upgrades HTTP
// connections, authenticates the player through package room and runs the read and write loops
// of each connection. It contains no game logic.
//
// Close codes: 4401 (unauthorized) and 4404 (room not found or expired) are final for the
// client; 1001 (server shutting down) and 1013 (connection too slow) mean "reconnect".
package realtime
