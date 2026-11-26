-- Rounds caught in the guess move to the result, which the old constraint allows.
UPDATE rounds SET phase = 'result' WHERE phase = 'guess';
--bun:split
ALTER TABLE rounds DROP CONSTRAINT rounds_phase_check;
--bun:split
ALTER TABLE rounds ADD CONSTRAINT rounds_phase_check
    CHECK (phase IN ('theme_selection', 'reveal', 'questions', 'discussion', 'voting', 'result'));
