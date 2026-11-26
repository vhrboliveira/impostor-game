ALTER TABLE rounds
    DROP CONSTRAINT rounds_subject_player_id_fkey,
    ADD CONSTRAINT rounds_subject_player_id_fkey
        FOREIGN KEY (subject_player_id) REFERENCES players (id);
--bun:split
ALTER TABLE rounds
    DROP CONSTRAINT rounds_impostor_player_id_fkey,
    ADD CONSTRAINT rounds_impostor_player_id_fkey
        FOREIGN KEY (impostor_player_id) REFERENCES players (id);
--bun:split
ALTER TABLE rounds
    DROP CONSTRAINT rounds_guess_player_id_fkey,
    ADD CONSTRAINT rounds_guess_player_id_fkey
        FOREIGN KEY (guess_player_id) REFERENCES players (id);
--bun:split
ALTER TABLE turns
    DROP CONSTRAINT turns_asker_id_fkey,
    ADD CONSTRAINT turns_asker_id_fkey
        FOREIGN KEY (asker_id) REFERENCES players (id);
--bun:split
ALTER TABLE turns
    DROP CONSTRAINT turns_answerer_id_fkey,
    ADD CONSTRAINT turns_answerer_id_fkey
        FOREIGN KEY (answerer_id) REFERENCES players (id);
--bun:split
ALTER TABLE votes
    DROP CONSTRAINT votes_voter_id_fkey,
    ADD CONSTRAINT votes_voter_id_fkey
        FOREIGN KEY (voter_id) REFERENCES players (id);
--bun:split
ALTER TABLE votes
    DROP CONSTRAINT votes_target_id_fkey,
    ADD CONSTRAINT votes_target_id_fkey
        FOREIGN KEY (target_id) REFERENCES players (id);
--bun:split
ALTER TABLE round_scores
    DROP CONSTRAINT round_scores_player_id_fkey,
    ADD CONSTRAINT round_scores_player_id_fkey
        FOREIGN KEY (player_id) REFERENCES players (id);
