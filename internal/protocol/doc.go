// Package protocol defines the wire format shared with the web app: the JSON messages exchanged
// over the room WebSocket and the snapshot sent to each player.
//
// The contract is documented in docs/protocol.md and mirrored by src/realtime/protocol.ts in
// the web repository. Every message is {"type": string, "payload"?: object}; JSON keys are
// camelCase.
package protocol
