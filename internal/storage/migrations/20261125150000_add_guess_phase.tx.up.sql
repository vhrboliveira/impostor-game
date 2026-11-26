-- The impostor's guess is a phase of its own, between the voting and the result.
ALTER TABLE rounds DROP CONSTRAINT rounds_phase_check;
--bun:split
ALTER TABLE rounds ADD CONSTRAINT rounds_phase_check
    CHECK (phase IN ('theme_selection', 'reveal', 'questions', 'discussion', 'voting', 'guess', 'result'));
