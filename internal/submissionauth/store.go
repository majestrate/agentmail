// Package submissionauth checks submission passwords against the users table.
package submissionauth

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	"agentmail/internal/directory"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

const stmtUserPassword = "user_password"

// ErrDenied is a wrong username or password. Any other error is temporary.
var ErrDenied = errors.New("submission auth denied")

// dummyHash keeps a missing user on the same bcrypt cost as a real check.
var (
	dummyOnce sync.Once
	dummyHash string
)

func burnDummy(password string) {
	dummyOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword([]byte("submission-auth-dummy"), bcrypt.DefaultCost)
		if err != nil {
			panic(err)
		}
		dummyHash = "{BLF-CRYPT}" + string(hash)
	})
	_, _ = directory.VerifyPassword(dummyHash, password)
}

// Prepare prepares the embedded password query on conn.
func Prepare(ctx context.Context, conn *pgx.Conn) error {
	body, err := sqlFiles.ReadFile("sql/user_password.sql")
	if err != nil {
		return err
	}
	sqlText := strings.TrimSpace(string(body))
	if sqlText == "" {
		return fmt.Errorf("sql file user_password.sql is empty")
	}
	if _, err := conn.Prepare(ctx, stmtUserPassword, sqlText); err != nil {
		return fmt.Errorf("prepare %s: %w", stmtUserPassword, err)
	}
	return nil
}

// Store reads password hashes from Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a store that uses prepared statement user_password.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Authenticate returns the canonical mailbox address when the password matches.
func (s *Store) Authenticate(ctx context.Context, username, password string) (string, error) {
	address, err := directory.CanonicalAddress(username)
	if err != nil || password == "" {
		burnDummy(password)
		return "", ErrDenied
	}
	var stored string
	err = s.pool.QueryRow(ctx, stmtUserPassword, address).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		burnDummy(password)
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	ok, err := directory.VerifyPassword(stored, password)
	if err != nil || !ok {
		return "", ErrDenied
	}
	return address, nil
}
