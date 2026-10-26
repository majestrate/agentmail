// Command milter speaks the Sendmail milter protocol Postfix dials for
// inbound SMTP. Recipients are checked against the users and
// distribution_lists tables. The process is plaintext. The pod sidecar
// authenticates Postfix with mutual TLS.
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
	"agentmail/internal/milter"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log.SetFlags(0)
	if os.Getuid() == 0 {
		log.Fatal("refusing to run the milter as root")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	domains, err := milter.ParseDomains(os.Getenv("MAIL_DOMAIN"))
	if err != nil {
		log.Fatal(err)
	}
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
	policy, err := milter.NewPolicy(domains, milter.NewStore(pool))
	if err != nil {
		log.Fatal(err)
	}

	listenAddr := getenv("MILTER_LISTEN", ":50053")
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("milter listening on %s", listenAddr)
	if err := milter.Serve(ctx, ln, policy); err != nil {
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
	cfg.AfterConnect = milter.Prepare
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
