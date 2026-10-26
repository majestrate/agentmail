// Command submission-auth speaks the Dovecot SASL protocol Postfix dials for
// submission. The password check uses the users table. The process is
// plaintext. The pod sidecar authenticates Postfix with mutual TLS.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"agentmail/internal/directory"
	"agentmail/internal/submissionauth"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log.SetFlags(0)
	if os.Getuid() == 0 {
		log.Fatal("refusing to run submission auth as root")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := dbConfig()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pingWithRetry(ctx, pool); err != nil {
		log.Fatal(err)
	}

	listenAddr := getenv("SUBMISSION_AUTH_LISTEN", ":50052")
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("submission auth listening on %s", listenAddr)
	if err := submissionauth.Serve(ctx, ln, submissionauth.NewStore(pool)); err != nil {
		log.Fatal(err)
	}
}

func pingWithRetry(ctx context.Context, pool *pgxpool.Pool) error {
	deadline := time.Now().Add(60 * time.Second)
	var err error
	for {
		err = pool.Ping(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		time.Sleep(time.Second)
	}
}

func dbConfig() (*pgxpool.Config, error) {
	host := firstEnv("DIRECTORY_PGSQL_HOST", "POSTFIX_PGSQL_HOST")
	portText := getenv("DIRECTORY_PGSQL_PORT", getenv("POSTFIX_PGSQL_PORT", "5432"))
	user := firstEnv("DIRECTORY_PGSQL_USER", "POSTFIX_PGSQL_USER")
	dbname := firstEnv("DIRECTORY_PGSQL_DBNAME", "POSTFIX_PGSQL_DBNAME")
	encoded := firstEnv("DIRECTORY_PGSQL_PASSWORD_B64", "POSTFIX_PGSQL_PASSWORD_B64")
	if host == "" || user == "" || dbname == "" || encoded == "" {
		return nil, fmt.Errorf("POSTFIX_PGSQL_HOST, POSTFIX_PGSQL_USER, POSTFIX_PGSQL_DBNAME, and POSTFIX_PGSQL_PASSWORD_B64 are required")
	}
	password, err := directory.DecodeBase64("POSTFIX_PGSQL_PASSWORD_B64", encoded)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("postgres port: %w", err)
	}
	sslmode := getenv("PGSSLMODE", "prefer")
	cfg, err := pgxpool.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s", host, port, user, dbname, sslmode))
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Password = password
	cfg.AfterConnect = submissionauth.Prepare
	return cfg, nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
