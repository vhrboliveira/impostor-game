-- Game catalog: themes, the words of "words" themes and the questions read aloud.

CREATE TABLE themes (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    CONSTRAINT themes_slug_key UNIQUE (slug),
    -- Target of the composite foreign keys that tie words and questions to the theme kind.
    CONSTRAINT themes_id_kind_key UNIQUE (id, kind),
    CONSTRAINT themes_kind_check CHECK (kind IN ('words', 'players'))
);
--bun:split
CREATE TABLE words (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    theme_id bigint NOT NULL,
    -- Always 'words': together with the foreign key it forbids words in "players" themes.
    kind     text NOT NULL DEFAULT 'words',
    text     text NOT NULL,
    CONSTRAINT words_theme_text_key UNIQUE (theme_id, text),
    -- Target of rounds_theme_word_fkey, so a round's word always belongs to its theme.
    CONSTRAINT words_theme_id_id_key UNIQUE (theme_id, id),
    CONSTRAINT words_kind_check CHECK (kind = 'words'),
    CONSTRAINT words_theme_kind_fkey FOREIGN KEY (theme_id, kind)
        REFERENCES themes (id, kind) ON DELETE CASCADE
);
--bun:split
CREATE TABLE questions (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind     text NOT NULL,
    -- NULL means a generic question that fits every theme of the same kind.
    theme_id bigint,
    text     text NOT NULL,
    CONSTRAINT questions_kind_check CHECK (kind IN ('words', 'players')),
    -- MATCH SIMPLE: not enforced while theme_id is NULL. Requires PostgreSQL 15+ for the unique below.
    CONSTRAINT questions_theme_kind_fkey FOREIGN KEY (theme_id, kind)
        REFERENCES themes (id, kind) ON DELETE CASCADE,
    CONSTRAINT questions_kind_theme_text_key UNIQUE NULLS NOT DISTINCT (kind, theme_id, text)
);
