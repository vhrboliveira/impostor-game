// Package realtime is the WebSocket transport: it upgrades HTTP connections, runs the read and
// write loops, validates the origin and translates frames into package room commands. It
// contains no game logic.
//
// It is implemented in the WebSocket step (see docs/architecture.md).
package realtime
