# Protocol

Contract between this API and the web app. The web app mirrors it in `src/realtime/protocol.ts`
(WebSocket) and `src/features/room/roomApi.ts` (REST); the Go side is `internal/httpapi` and
`internal/protocol`. JSON keys are camelCase, message and error identifiers are English.

Everything below is implemented: REST, the WebSocket connection, the lobby, the round flow, the
voting, the impostor's guess, the result and the end of the match.

## REST

Implemented (slice 1).

| Endpoint | Body | Success |
|---|---|---|
| `POST /api/room` | `{ "playerName": string, "totalRounds": 3 \| 5 \| 8 \| 10 \| 12 }` | `201 { "roomCode", "playerId", "token" }` |
| `POST /api/room/{code}/join` | `{ "playerName": string }` | `201 { "roomCode", "playerId", "token" }` |
| `PUT /api/room/{code}/me/avatar` | the image itself (JPEG or PNG) | `204` |
| `DELETE /api/room/{code}/me/avatar` | | `204` |
| `GET /api/room/{code}/players/{id}/avatar` | | `200` with the JPEG |
| `GET /healthz` | | `200 { "status": "ok" }` |
| `GET /readyz` | | `200 { "status": "ok" }` or `503 { "status": "unavailable" }` |

Request rules:

- `POST` bodies must be sent as `Content-Type: application/json` (`415 unsupported_media_type`
  otherwise): another site's page cannot post them without a CORS preflight.
- The body is at most 4 KiB and holds exactly one JSON value. Unknown fields are rejected.
- `playerName`: 1 to 20 printable characters after trimming; inner whitespace is collapsed, the
  name is composed (Unicode NFC, one spelling per name), invisible or blank characters (Hangul
  fillers, the blank braille pattern, other default-ignorable code points) and more than two
  combining marks on a character are refused, and the first letter is upper-cased (`"ana"`
  becomes `"Ana"`). When the name is taken in the room
  the server appends a number (`"Ana 2"`); the response does not echo the final name (the
  snapshot does).
- `{code}` is case insensitive and trimmed (`abcde` joins `ABCDE`). Codes have 5 characters
  from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`; anything else is `room_not_found`.
- The creator becomes the host. A player can join while the latest match is in the lobby or
  between rounds (latest round in `result`). A finished or ended-early match refuses new players
  until "play again" opens a new match.
- The room is full with 20 players holding a seat: a player holds one once they opened the
  WebSocket at least once, or during the first 2 minutes after joining. Joins that never open the
  room cannot keep it full.
- Responses are JSON with `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` (the
  create and join responses carry the token).
- `token` is returned once. The client keeps it on the device; the server stores only its
  SHA-256 hash.

### Player photo

Optional. The web app crops the picked photo (camera or gallery) to a 768x768 square JPEG
(about 100 KB) and sends it right after creating or joining the room; in the lobby the player can
change or remove it.

- `PUT` and `DELETE` identify the player with `Authorization: Bearer <token>` (the session token).
  A missing or wrong token, a malformed code or an unknown room all answer `401 unauthorized`:
  these endpoints never tell whether a room exists. The token is checked before the image is
  decoded.
  The `PUT` body is the image itself, at most 500 KiB, JPEG or PNG, at most 768 pixels on each
  side and, for a progressive JPEG, at most 32 scans (decoding cost grows with them). The server
  decodes at most two photos at a time and re-encodes them as JPEG, which drops every metadata
  segment (EXIF can carry the GPS position).
- Rule: a player without a photo can send one at any moment (someone who joins between rounds
  has no lobby); changing or removing it is only allowed while the match is in the lobby
  (`avatar_locked`). The change goes through the room actor, so everyone gets a new snapshot.
- Photos never travel inside the snapshot: `players[].avatarUrl` is the path of the `GET` with
  the photo's version (`?v=<unix ms>`), or `null`. That exact URL is served with
  `Cache-Control: private, max-age=31536000, immutable`; any other version, or none, with
  `private, no-cache`.
- The `GET` needs no token (image elements cannot send one); the URL holds the player's random
  id, which only the players of the room receive. It answers `avatar_not_found` for a player
  without a photo, an unknown player or an expired room.
- The photo is deleted with the player, and so with the room (retention rules).

```bash
curl -s -X PUT localhost:8080/api/room/K7M2P/me/avatar \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: image/jpeg' --data-binary @me.jpg
```

### Errors

Errors are `{ "code": string }`:

| Status | `code` | When |
|---|---|---|
| 400 | `invalid_request` | Malformed JSON, wrong types, unknown fields, more than one value |
| 400 | `invalid_name` | Empty, too long or non-printable player name |
| 400 | `invalid_rounds` | `totalRounds` not in 3, 5, 8, 10, 12 |
| 400 | `invalid_avatar` | The photo is not a JPEG or PNG, or is bigger than 768x768 |
| 401 | `unauthorized` | Photo endpoints: missing or unknown session token, or unknown room |
| 404 | `avatar_not_found` | The player has no photo, or the player or room does not exist |
| 404 | `room_not_found` | Join: unknown, malformed or expired room code |
| 404 | `not_found` | Unknown route or method |
| 409 | `room_full` | The room already has 20 players |
| 409 | `match_in_progress` | A round is being played |
| 409 | `match_finished` | The match is over and no new one was started |
| 409 | `avatar_locked` | Changing or removing a photo outside the lobby |
| 413 | `request_too_large` | JSON body over 4 KiB, or photo over 500 KiB |
| 415 | `unsupported_media_type` | A `POST` body not sent as `application/json` |
| 429 | `rate_limited` | Too many requests from this client (see below); `Retry-After` in seconds |
| 500 | `internal_error` | Anything unexpected (logged with the request id) |

### Rate limits

Per client address: `RemoteAddr`, or, when it is one of `TRUSTED_PROXIES`, the last address of
`X-Forwarded-For` that is not a trusted proxy. IPv6 clients are grouped by their /64.

| Budget | Routes | Burst, then |
|---|---|---|
| Rooms | `POST /api/room` | 5, then 1 every 30 s |
| Requests | join, photo `PUT`/`DELETE`, the WebSocket handshake | 60, then 1 per second |
| Misses | join and photo `PUT`/`DELETE` answered `404` or `401` | 20, then 1 every 6 s |

Guessing room codes is almost only misses, while a group joining a real room from one Wi-Fi has
none: the room code (5 characters, 2^25 values) is the only capability to join, and these budgets
are what keeps it from being guessed.

Example:

```bash
curl -s -X POST localhost:8080/api/room -H 'Content-Type: application/json' \
  -d '{"playerName":"Ana","totalRounds":5}'
# {"roomCode":"K7M2P","playerId":"…","token":"…"}
curl -s -X POST localhost:8080/api/room/K7M2P/join -H 'Content-Type: application/json' \
  -d '{"playerName":"Bia"}'
```

## WebSocket

### Connection

`GET /ws/room/{code}?token=...`, where `token` is the one returned by create/join.

1. The `Origin` header must be one of `ALLOWED_ORIGINS` (exact origin, case-insensitive, trailing
   slash ignored). Otherwise the server answers `403 {"code":"forbidden_origin"}` and does not
   upgrade.
2. The connection is upgraded, then the player is authenticated (within 10 seconds). A bad code
   or token closes the socket with `4401` either way, so the handshake never tells whether a room
   exists (the upgrade happens first so the browser sees the code).
3. The token travels in the query string because browsers cannot set headers on the handshake.
   The API never logs it (the access log has only the path); a reverse proxy in front of it must
   not log the query string of `/ws/` either.
4. The first message is always a `room_snapshot`. Another one follows every change.

Close codes:

| Code | Meaning | Client |
|---|---|---|
| `4401` | Unknown or missing token, malformed code or unknown room | Final: forget the session, offer to join again |
| `4403` | The player left the room or was removed (host, or offline 3 minutes) | Final: forget the session, offer to join again |
| `4404` | The room expired (or was deleted) while connected | Final: the room is gone |
| `1001` | Server shutting down | Reconnect |
| `1008` | The connection sent messages too fast, or the player opened a fifth connection (the oldest is closed) | Reconnect |
| `1011` | The room failed on the server; reconnecting loads it again | Reconnect |
| `1013` | The connection could not keep up with its messages (errors and pongs) | Reconnect |
| `1009` | Message over 4 KiB | Reconnect (and stop sending it) |

Limits: messages are text frames of at most 4 KiB, at most 30 in a burst and then 10 per second
per connection. A connection that sends nothing for 60 seconds is closed; the web app sends
`ping` every 25 seconds. A player may have up to 4 connections (tabs, a reconnect racing a stale
socket); they count as one connected player, and a new one beyond that closes the oldest.
Snapshots carry the whole state, so a newer one replaces an unsent older one: bursts of changes
never fill a connection's buffer.

Every message is `{ "type": string, "payload"?: object }`. Unknown fields, unknown types, a payload
on a message that takes none, or binary frames are answered with
`{"type":"error","payload":{"code":"invalid_message"}}`; the connection stays open.

### Server to client

| `type` | Payload |
|---|---|
| `room_snapshot` | The full state for that player (below). Sent first on every connection and after every change. |
| `error` | `{ "code": string }`: a command was rejected. Nothing changed. |
| `pong` | none: the answer to `ping`. |

Snapshot:

```json
{
  "roomCode": "K7M2P",
  "match": { "status": "in_progress", "totalRounds": 5, "roundNumber": 1 },
  "players": [
    {
      "id": "…",
      "name": "Ana",
      "isHost": true,
      "isConnected": true,
      "score": 0,
      "avatarUrl": "/api/room/K7M2P/players/…/avatar?v=1795608000000",
      "hasLeft": false
    }
  ],
  "round": {
    "number": 1,
    "phase": "questions",
    "themeOptions": [],
    "themeName": "Comidas",
    "totalTurns": 3,
    "confirmedIds": [],
    "phaseDeadlineAt": null,
    "votedIds": [],
    "guessOptions": [],
    "turns": [
      { "position": 0, "askerId": "…", "answererId": "…", "question": "Doce ou salgado?", "deadlineAt": null },
      { "position": 1, "askerId": "…", "answererId": "…", "question": "Quente ou frio?", "deadlineAt": "2026-11-25T12:03:00Z" }
    ],
    "result": null
  },
  "me": { "playerId": "…", "role": "insider", "secret": "Pizza", "voteTargetId": null }
}
```

- `match.status`: `lobby`, `in_progress`, `finished`, `ended_early`. `roundNumber` is `0` before
  the first round, and `round` is `null`.
- `players` are ordered by join time; `score` is the sum of the current match.
- `round.phase`: `theme_selection`, `reveal`, `questions`, `discussion`, `voting`, `guess`
  (only when the impostor was found), `result`.
- `themeOptions`: the whole catalog, ordered by name, only in `theme_selection`.
- `themeName`: the chosen theme, from `reveal` on (`null` before).
- `totalTurns`: size of the turn ring, one turn per player of the round (`0` before the theme
  is chosen). The last turn has position `totalTurns - 1`.
- `confirmedIds`: who tapped "Entendi", only in `reveal`, in player order (not ring order: the
  ring is only shown turn by turn). Confirming again changes nothing and sends nothing.
- `phaseDeadlineAt`: the end of the discussion, of the voting (5 minutes) or of the guess (60
  seconds); `null` in the other phases.
- `votedIds`: who already voted, only in `voting` (who they voted for stays secret).
- `guessOptions`: `[{ "id", "label" }]`, up to 8 possible secrets with the right one among them,
  only in `guess` and only in the impostor's own snapshot (in a players theme they leave the
  impostor out, so anyone else would learn who the impostor is). In a words theme the wrong
  options come from the words no other round of the room used, like the secret.
- `turns`: empty until the questions start; during `questions` the turns up to the current one
  (the last element), and only that one carries `deadlineAt`; from `discussion` on the whole
  ring, without deadlines.
- `result` (only in `result`):
  `{ "impostorId", "impostorFound", "secret", "votes": [{ "voterId", "targetId" }],
  "guess": { "label", "correct" } | null, "points": [{ "playerId", "points" }] }`. `votes` lists
  every player of the round in ring order (`targetId` null is an abstention); `guess` is null
  when the impostor escaped, and its `label` is null when time ran out; `points` are the points
  of this round (players with none are left out). `players[].score` already includes them.
- `me.voteTargetId`: the player's own vote, from `voting` on.
- `me.role`: `insider` or `impostor` for the players of the round from `reveal` on; `null` in the
  lobby, in `theme_selection`, and for players who sit the round out (offline when it started).
  `me.secret` is the word (or, in "Pessoas da sala", the subject's name) for insiders only, in
  every phase from `reveal` on: the web app's "Segure para lembrar" reads it. It is never sent to
  the impostor or to players outside the round (`room/snapshot_test.go` walks a round to prove
  it).
- Lists are always arrays, never `null`.
- Security rules: `me.secret` is never sent to the impostor; nobody learns who the impostor is
  before the `result` phase; `turns` never contains a future question; `round.result` is only
  set in the `result` phase.

### Client to server

| `type` | Payload | Who | Status |
|---|---|---|---|
| `ping` | none | anyone | implemented |
| `start_match` | none | host, with 3+ connected players, in the lobby | implemented |
| `select_theme` | `{ "themeId": number \| "random" }` | host, in `theme_selection`, 3+ connected | implemented |
| `confirm_reveal` | none | a player of the round, in `reveal` | implemented |
| `start_questions` | none | host, in `reveal` | implemented |
| `next_turn` | none | answerer of the current turn, or the host | implemented |
| `extend_turn` | none | answerer of the current turn, or the host | implemented |
| `extend_discussion` | none | host, in `discussion` | implemented |
| `start_voting` | none | host, in `discussion` | implemented |
| `vote` | `{ "targetId": string }` | a player of the round, once, in `voting` | implemented |
| `guess` | `{ "optionId": string }` | the found impostor, in `guess` | implemented |
| `next_round` | none | host, in `result` | implemented |
| `end_match` | none | host, while the match is in progress | implemented |
| `play_again` | `{ "totalRounds": 3 \| 5 \| 8 \| 10 \| 12 }` | host, after the match ended | implemented |
| `leave_room` | none | anyone, at any moment | implemented |
| `remove_player` | `{ "playerId": string }` | host, another player still in the room | implemented |

Round flow:

1. `start_match` sets the match `in_progress` and creates round 1 in `theme_selection`.
2. `select_theme` draws the impostor and the secret, builds the turn ring with the connected
   players and moves to `reveal`. `"random"` tries the themes in random order until one can
   feed the round.
3. `confirm_reveal` adds the player to `confirmedIds`. When every connected player of the round
   confirmed (or the host sends `start_questions`), the round moves to `questions` and the first
   turn starts with a deadline of `turn_seconds` (180 s by default).
4. `next_turn` ends the current turn and starts the next one; `extend_turn` adds 1 minute to the
   current deadline. A turn that reaches its deadline ends by itself. After the last turn the
   round moves to `discussion` with `phaseDeadlineAt` 3 minutes ahead.
5. `extend_discussion` adds 1 minute to `phaseDeadlineAt`. At `phaseDeadlineAt`, or when the host
   sends `start_voting`, the round moves to `voting` with a 5-minute `phaseDeadlineAt`.
6. `vote` can be sent again to change it while the voting is open (the same target again is
   ignored). The voting closes when every connected player of the round voted, or at
   `phaseDeadlineAt`; the others count as abstentions. If the impostor got strictly more votes
   than anyone, the round moves to `guess` with a 60-second `phaseDeadlineAt`; otherwise straight
   to `result`.
7. `guess` (or the deadline, which counts as a wrong guess) moves the round to `result` with the
   points: +1 to each player who voted for the impostor, +2 to an impostor who escaped, +1 to a
   found impostor who guessed the secret.
8. `next_round` opens the next round in `theme_selection`, or after the last round sets the
   match `finished`. `end_match` sets it `ended_early` at any moment. Both show the podium.
9. `play_again` opens a new match in `lobby` with the chosen number of rounds, the same players
   and the scores at zero.
10. Leaving: `leave_room` (the player), `remove_player` (the host), or 3 minutes offline remove a
    player from the room. They are marked (`players.left_at`), not deleted: they stay in
    `players` with `"hasLeft": true`, so the names of the round still resolve (the app hides them
    from the lobby, the scoreboard and the podium). Their seat and name are free, their token stops
    working, and their open connections close with `4403`. A host who leaves hands the role to the
    oldest connected player at once. In a round in progress a removed player counts as offline
    (abstention, the turn advances by its deadline).

Error codes:

| `code` | When |
|---|---|
| `invalid_message` | The message does not follow the protocol |
| `not_host` | Only the host can do that |
| `not_enough_players` | `start_match` or `select_theme` with fewer than 3 connected players |
| `invalid_phase` | The action is not allowed now (for example `start_match` twice) |
| `not_your_turn` | `next_turn` or `extend_turn` from someone who is neither the answerer nor the host |
| `not_participant` | `confirm_reveal` or `vote` from a player who sits the round out |
| `theme_unavailable` | Unknown theme, fewer than 4 words left unused in the room (words theme), or fewer questions than players |
| `invalid_vote` | A vote for oneself or for someone outside the round |
| `not_impostor` | `guess` from anyone but the found impostor |
| `invalid_guess` | `guess` with an option that is not in `guessOptions` |
| `invalid_rounds` | `play_again` with a number of rounds other than 3, 5, 8, 10 or 12 |
| `invalid_player` | `remove_player` naming the host themselves, or nobody still in the room |
| `internal_error` | Something failed on the server (logged) |

### Presence and host

- `players[].isConnected` is true while the player has at least one open connection.
- When the last connection of the room closes, `rooms.empty_since` is set; every connection
  clears it (retrying a clear that failed). An actor that stops while idle marks the room empty
  first, and at startup every room still marked occupied is marked empty (no WebSocket survives a
  restart). The janitor deletes rooms empty for an hour.
- After the host ends a match early (`end_match`), its round is frozen: no command, presence
  change or timer of that match runs, and its scores are final.
- When the host has no connection for 30 seconds, the oldest connected player becomes the host.

Still to define: leaving a room.
