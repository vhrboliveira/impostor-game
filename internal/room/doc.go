// Package room holds the application logic of a room.
//
// This package currently implements the room lifecycle over REST: creating a room and joining
// one (Service). It defines the small Store interface it needs; package storage implements it,
// so room never imports the database layer.
//
// The live game (one actor goroutine per active room that processes commands in order, applies
// the rules from package game and sends each player a snapshot) is added in the WebSocket step
// (see docs/architecture.md).
package room
