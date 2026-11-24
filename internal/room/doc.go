// Package room runs the live game of a room. Each active room is an actor: a single goroutine
// that processes commands in order (join, leave, player messages, timers), applies the rules
// from package game, persists the result through small storage interfaces defined here, and
// sends each player a snapshot that never contains secrets they must not see.
//
// It is implemented in the WebSocket step (see docs/architecture.md).
package room
