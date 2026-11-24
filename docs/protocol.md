# Protocol (draft)

Contract between this API and the web app. The web app mirrors it in `src/realtime/protocol.ts`.
It is finalized in the WebSocket step. JSON keys are camelCase, message and error identifiers
are English.

## REST

| Endpoint | Body | Response |
|---|---|---|
| `POST /api/room` | `{ "playerName": string, "totalRounds": 3 \| 5 \| 8 \| 10 \| 12 }` | `{ "roomCode", "playerId", "token" }` |
| `POST /api/room/{code}/join` | `{ "playerName": string }` | `{ "roomCode", "playerId", "token" }` |
| `GET /healthz` | | `{ "status": "ok" }` |
| `GET /readyz` | | `{ "status": "ok" }` or `503 { "status": "unavailable" }` |

Errors: `{ "code": "room_not_found" }` with an HTTP status. Codes seen by the web app so far:
`room_not_found`, `room_full`, `match_in_progress`, `invalid_name`, `internal_error`, `not_found`.

## WebSocket

`GET /ws/room/{code}?token=...`. The `Origin` header must be in `ALLOWED_ORIGINS`. Application
close codes `4401` (unauthorized) and `4404` (room not found) are final: the client does not retry.

Every message is `{ "type": string, "payload"?: object }`.

### Server to client

| `type` | Payload |
|---|---|
| `room_snapshot` | The full state for that player (below). Sent first on every connection and after every change. |
| `error` | `{ "code": string }` |
| `pong` | none |

Snapshot:

```json
{
  "roomCode": "ABCDE",
  "match": { "status": "in_progress", "totalRounds": 5, "roundNumber": 2 },
  "players": [{ "id": "...", "name": "Ana", "isHost": true, "isConnected": true, "score": 3 }],
  "round": {
    "number": 2,
    "phase": "questions",
    "themeOptions": [],
    "turns": [{ "position": 0, "askerId": "...", "answererId": "...", "question": "...", "deadlineAt": "2026-11-23T12:00:00Z" }],
    "result": null
  },
  "me": { "playerId": "...", "role": "insider", "secret": "Pizza", "voteTargetId": null }
}
```

Rules: `turns` stops at the current turn (never future questions); `me.secret` is `null` for the
impostor; `round.result` is only set in the `result` phase; `themeOptions` is only filled in
`theme_selection`.

### Client to server

| `type` | Payload | Who |
|---|---|---|
| `ping` | none | anyone |
| `start_match` | none | host |
| `select_theme` | `{ "themeId": number \| "random" }` | host |
| `next_turn` | none | answerer or host |
| `extend_turn` | none | answerer or host |
| `vote` | `{ "targetId": string }` | any player |
| `end_match` | none | host |

Still to define: the impostor's guess, "play again", discussion timer, leaving a room.
