package milter

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

const (
	stmtUserAddress = "user_address"
	stmtListAddress = "list_address"
	lookupTimeout   = 5 * time.Second
)

// Prepare prepares the embedded recipient queries on conn.
func Prepare(ctx context.Context, conn *pgx.Conn) error {
	for _, spec := range []struct {
		name string
		file string
	}{
		{stmtUserAddress, "sql/user_address.sql"},
		{stmtListAddress, "sql/list_address.sql"},
	} {
		body, err := sqlFiles.ReadFile(spec.file)
		if err != nil {
			return err
		}
		sqlText := strings.TrimSpace(string(body))
		if sqlText == "" {
			return fmt.Errorf("sql file %s is empty", spec.file)
		}
		if _, err := conn.Prepare(ctx, spec.name, sqlText); err != nil {
			return fmt.Errorf("prepare %s: %w", spec.name, err)
		}
	}
	return nil
}

// Store reads users and distribution lists from Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a store that uses the prepared recipient queries.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// RecipientExists reports whether address is a user or a distribution list.
func (s *Store) RecipientExists(ctx context.Context, address string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	found, err := s.exists(ctx, stmtUserAddress, address)
	if err != nil || found {
		return found, err
	}
	return s.exists(ctx, stmtListAddress, address)
}

func (s *Store) exists(ctx context.Context, stmt, address string) (bool, error) {
	var stored string
	err := s.pool.QueryRow(ctx, stmt, address).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
