// Package migrate applies the embedded goose migrations.
package migrate

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var files embed.FS

// Up applies every pending migration. It is safe to call on each start.
func Up(ctx context.Context, cfg *pgx.ConnConfig) error {
	goose.SetBaseFS(files)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	return goose.UpContext(ctx, db, "migrations")
}
