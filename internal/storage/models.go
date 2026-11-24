package storage

import "github.com/uptrace/bun"

// The models below are used to query and write rows. They do not define the schema: the SQL
// migrations do. Models for the game state tables are added together with their repositories.

// Theme is a row of the themes table.
type Theme struct {
	bun.BaseModel `bun:"table:themes,alias:t"`

	ID   int64  `bun:"id,pk,autoincrement"`
	Slug string `bun:"slug,notnull"`
	Name string `bun:"name,notnull"`
	Kind string `bun:"kind,notnull"`
}

// Word is a row of the words table. The kind column is omitted on purpose: it defaults to
// 'words' in the database.
type Word struct {
	bun.BaseModel `bun:"table:words,alias:w"`

	ID      int64  `bun:"id,pk,autoincrement"`
	ThemeID int64  `bun:"theme_id,notnull"`
	Text    string `bun:"text,notnull"`
}

// Question is a row of the questions table. A nil ThemeID is a generic question.
type Question struct {
	bun.BaseModel `bun:"table:questions,alias:q"`

	ID      int64  `bun:"id,pk,autoincrement"`
	Kind    string `bun:"kind,notnull"`
	ThemeID *int64 `bun:"theme_id"`
	Text    string `bun:"text,notnull"`
}
