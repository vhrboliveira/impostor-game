package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/vhrboliveira/impostor-game/internal/game"
	"github.com/vhrboliveira/impostor-game/internal/room"
)

// Rooms implements the persistence the room service needs.
var _ room.Store = (*Rooms)(nil)

// CreateRoom creates the room, its first match (in the lobby) and the host in one transaction.
// It returns room.ErrCodeTaken, leaving no rows behind, when the code is already in use.
//
// empty_since starts at the creation time: nobody is connected yet, so a room nobody ever opens
// expires like any empty room. The WebSocket layer clears it when the first player connects.
func (r *Rooms) CreateRoom(ctx context.Context, params room.CreateRoomParams) (room.Player, error) {
	var player room.Player

	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		emptySince := params.Now
		newRoom := &Room{
			Code:       params.Code,
			CreatedAt:  params.Now,
			ExpiresAt:  params.ExpiresAt,
			EmptySince: &emptySince,
		}
		// With ON CONFLICT DO NOTHING no row comes back. Scan reports that as sql.ErrNoRows
		// (Exec would succeed silently and leave the id empty).
		err := tx.NewInsert().Model(newRoom).
			On("CONFLICT (code) DO NOTHING").
			Returning("id").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return room.ErrCodeTaken
		}
		if err != nil {
			return fmt.Errorf("insert room: %w", err)
		}

		match := &Match{
			RoomID:      newRoom.ID,
			Number:      1,
			Status:      string(game.MatchLobby),
			TotalRounds: params.TotalRounds,
			TurnSeconds: params.TurnSeconds,
			CreatedAt:   params.Now,
		}
		if _, err := tx.NewInsert().Model(match).Exec(ctx); err != nil {
			return fmt.Errorf("insert first match: %w", err)
		}

		host := &Player{
			RoomID:     newRoom.ID,
			Name:       params.HostName,
			IsHost:     true,
			TokenHash:  params.TokenHash,
			JoinedAt:   params.Now,
			LastSeenAt: params.Now,
		}
		if _, err := tx.NewInsert().Model(host).Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert host: %w", err)
		}

		player = room.Player{ID: host.ID, RoomID: newRoom.ID, Name: host.Name, IsHost: true}
		return nil
	})
	if err != nil {
		return room.Player{}, err
	}
	return player, nil
}

// AddPlayer adds a player to a room in one transaction. The room row is locked (FOR UPDATE), so
// concurrent joins to the same room run one after the other and the player limit holds.
//
// A player can join while the latest match is in the lobby, and between rounds (the latest round
// is in its result phase). It returns room.ErrRoomNotFound (unknown or expired code),
// room.ErrMatchInProgress, room.ErrMatchFinished (finished or ended early: players join again
// after "play again" opens a new match), room.ErrRoomFull or room.ErrNameTaken.
func (r *Rooms) AddPlayer(ctx context.Context, params room.AddPlayerParams) (room.Player, error) {
	var player room.Player

	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var locked Room
		err := tx.NewSelect().Model(&locked).
			Column("id").
			Where("code = ?", params.Code).
			Where("expires_at > ?", params.Now).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return room.ErrRoomNotFound
		}
		if err != nil {
			return fmt.Errorf("lock room: %w", err)
		}

		if err := latestMatchAcceptsPlayers(ctx, tx, locked.ID); err != nil {
			return err
		}

		// A seat is held by a player who opened the room at least once, or who joined a moment ago
		// (game.SeatHoldTime): joins that never connect cannot keep the room full.
		count, err := tx.NewSelect().Model((*Player)(nil)).
			Where("room_id = ?", locked.ID).
			Where("left_at IS NULL").
			WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
				return q.Where("connected_at IS NOT NULL").WhereOr("joined_at > ?", params.Now.Add(-game.SeatHoldTime))
			}).
			Count(ctx)
		if err != nil {
			return fmt.Errorf("count players: %w", err)
		}
		if count >= int64(params.MaxPlayers) {
			return room.ErrRoomFull
		}

		joined := &Player{
			RoomID:     locked.ID,
			Name:       params.Name,
			TokenHash:  params.TokenHash,
			JoinedAt:   params.Now,
			LastSeenAt: params.Now,
		}
		err = tx.NewInsert().Model(joined).
			On("CONFLICT (room_id, name) WHERE left_at IS NULL DO NOTHING").
			Returning("id").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return room.ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("insert player: %w", err)
		}

		player = room.Player{ID: joined.ID, RoomID: locked.ID, Name: joined.Name}
		return nil
	})
	if err != nil {
		return room.Player{}, err
	}
	return player, nil
}

// latestMatchAcceptsPlayers returns nil when new players may join the room now: the latest match
// is in the lobby, or it is between rounds (its latest round is in the result phase). Otherwise
// it returns room.ErrMatchInProgress or room.ErrMatchFinished.
func latestMatchAcceptsPlayers(ctx context.Context, tx bun.Tx, roomID string) error {
	var match Match
	err := tx.NewSelect().Model(&match).
		Column("id", "status").
		Where("room_id = ?", roomID).
		Order("number DESC").
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// Every room is created with its first match, so this only happens if that invariant
		// was broken by hand. Nothing is running, so joining is harmless.
		return nil
	}
	if err != nil {
		return fmt.Errorf("read latest match: %w", err)
	}

	switch game.MatchStatus(match.Status) {
	case game.MatchLobby:
		return nil
	case game.MatchInProgress:
		var round Round
		err := tx.NewSelect().Model(&round).
			Column("phase").
			Where("match_id = ?", match.ID).
			Order("number DESC").
			Limit(1).
			Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return room.ErrMatchInProgress // started, first round not created yet
		case err != nil:
			return fmt.Errorf("read latest round: %w", err)
		case game.Phase(round.Phase) == game.PhaseResult:
			return nil
		default:
			return room.ErrMatchInProgress
		}
	default: // finished, ended_early
		return room.ErrMatchFinished
	}
}
