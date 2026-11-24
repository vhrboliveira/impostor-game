-- Matches, rounds, turns, votes and scores.

CREATE TABLE matches (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id      uuid NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    -- "Play again" creates a new match (number + 1) in the same room.
    number       integer NOT NULL,
    status       text NOT NULL DEFAULT 'lobby',
    total_rounds integer NOT NULL,
    turn_seconds integer NOT NULL DEFAULT 180,
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    ended_at     timestamptz,
    CONSTRAINT matches_room_number_key UNIQUE (room_id, number),
    CONSTRAINT matches_number_check CHECK (number > 0),
    CONSTRAINT matches_status_check CHECK (status IN ('lobby', 'in_progress', 'finished', 'ended_early')),
    CONSTRAINT matches_total_rounds_check CHECK (total_rounds IN (3, 5, 8, 10, 12)),
    CONSTRAINT matches_turn_seconds_check CHECK (turn_seconds > 0)
);
--bun:split
CREATE TABLE rounds (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id           uuid NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    number             integer NOT NULL,
    phase              text NOT NULL DEFAULT 'theme_selection',
    phase_deadline_at  timestamptz,
    theme_id           bigint REFERENCES themes (id),
    -- True when the host used the "Sorteio" (random theme) option.
    theme_drawn        boolean NOT NULL DEFAULT false,
    -- Secret of a "words" theme.
    word_id            bigint,
    -- Secret of a "players" theme.
    subject_player_id  uuid REFERENCES players (id),
    impostor_player_id uuid REFERENCES players (id),
    -- Zero-based index of the turn in progress. Players only receive turns up to this one.
    current_turn       integer NOT NULL DEFAULT 0,
    impostor_found     boolean,
    -- Options shown to the impostor when guessing the secret (word ids or player ids).
    guess_options      jsonb,
    guess_word_id      bigint REFERENCES words (id),
    guess_player_id    uuid REFERENCES players (id),
    guess_correct      boolean,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rounds_match_number_key UNIQUE (match_id, number),
    CONSTRAINT rounds_number_check CHECK (number > 0),
    CONSTRAINT rounds_phase_check CHECK (phase IN ('theme_selection', 'reveal', 'questions', 'discussion', 'voting', 'result')),
    CONSTRAINT rounds_current_turn_check CHECK (current_turn >= 0),
    -- A round has a word or a player as its secret, never both.
    CONSTRAINT rounds_single_secret_check CHECK (NOT (word_id IS NOT NULL AND subject_player_id IS NOT NULL)),
    CONSTRAINT rounds_word_needs_theme_check CHECK (word_id IS NULL OR theme_id IS NOT NULL),
    CONSTRAINT rounds_single_guess_check CHECK (NOT (guess_word_id IS NOT NULL AND guess_player_id IS NOT NULL)),
    -- The word must belong to the round's theme (MATCH SIMPLE: skipped while word_id is NULL).
    CONSTRAINT rounds_theme_word_fkey FOREIGN KEY (theme_id, word_id)
        REFERENCES words (theme_id, id)
);
--bun:split
CREATE TABLE turns (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    round_id    uuid NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    -- Zero-based position in the turn ring.
    position    integer NOT NULL,
    asker_id    uuid NOT NULL REFERENCES players (id),
    answerer_id uuid NOT NULL REFERENCES players (id),
    question_id bigint NOT NULL REFERENCES questions (id),
    started_at  timestamptz,
    deadline_at timestamptz,
    ended_at    timestamptz,
    ended_by    text,
    CONSTRAINT turns_round_position_key UNIQUE (round_id, position),
    -- A question never repeats within a round.
    CONSTRAINT turns_round_question_key UNIQUE (round_id, question_id),
    CONSTRAINT turns_position_check CHECK (position >= 0),
    CONSTRAINT turns_distinct_players_check CHECK (asker_id <> answerer_id),
    CONSTRAINT turns_ended_by_check CHECK (ended_by IS NULL OR ended_by IN ('answerer', 'host', 'timeout'))
);
--bun:split
CREATE TABLE votes (
    round_id  uuid NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    voter_id  uuid NOT NULL REFERENCES players (id),
    -- NULL is an abstention.
    target_id uuid REFERENCES players (id),
    cast_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (round_id, voter_id),
    CONSTRAINT votes_no_self_vote_check CHECK (target_id IS NULL OR target_id <> voter_id)
);
--bun:split
CREATE TABLE round_scores (
    round_id  uuid NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    player_id uuid NOT NULL REFERENCES players (id),
    reason    text NOT NULL,
    points    integer NOT NULL,
    PRIMARY KEY (round_id, player_id, reason),
    CONSTRAINT round_scores_reason_check CHECK (reason IN ('correct_vote', 'impostor_escaped', 'impostor_guessed_word')),
    CONSTRAINT round_scores_points_check CHECK (points > 0)
);
