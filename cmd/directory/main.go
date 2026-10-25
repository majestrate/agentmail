//go:generate protoc -I ../../proto -I /usr/include --go_out=../.. --go_opt=module=agentmail --go-grpc_out=../.. --go-grpc_opt=module=agentmail ../../proto/agentmail/directory/v1/directory.proto

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

	directoryv1 "agentmail/gen/agentmail/directory/v1"
	"agentmail/internal/directory"
	"agentmail/internal/migrate"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	log.SetFlags(0)
	if os.Getuid() == 0 {
		log.Fatal("refusing to run the directory service as root")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := dbConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := migrateWithRetry(ctx, cfg.ConnConfig); err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		log.Print("migrations applied")
		return
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	listenAddr := getenv("DIRECTORY_LISTEN", ":50051")
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	server := grpc.NewServer()
	directoryv1.RegisterDirectoryServer(server, directory.NewServer(directory.NewStore(pool)))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	reflection.Register(server)

	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	log.Printf("directory listening on %s", listenAddr)
	if err := server.Serve(lis); err != nil {
		log.Fatal(err)
	}
}

func migrateWithRetry(ctx context.Context, cfg *pgx.ConnConfig) error {
	deadline := time.Now().Add(60 * time.Second)
	var err error
	for {
		err = migrate.Up(ctx, cfg)
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
		return nil, fmt.Errorf("DIRECTORY_PGSQL_HOST, DIRECTORY_PGSQL_USER, DIRECTORY_PGSQL_DBNAME, and DIRECTORY_PGSQL_PASSWORD_B64 are required")
	}
	password, err := directory.DecodeBase64("DIRECTORY_PGSQL_PASSWORD_B64", encoded)
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
	cfg.AfterConnect = directory.PrepareQueries
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
