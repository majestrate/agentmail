// Command relay is a synchronous SMTP proxy with two listeners.
// The VPN listener accepts recipients in MAIL_DOMAIN and dials internal
// Postfix. The mesh listener refuses those domains and dials the public MX.
// A message is accepted only after the next hop accepts it. Nothing is queued.
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

	"agentmail/internal/relay"
)

func main() {
	log.SetFlags(0)
	if os.Getuid() == 0 {
		log.Fatal("refusing to run the relay as root")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mesh, vpn, meshListen, vpnListen, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	meshLn, err := net.Listen("tcp", meshListen)
	if err != nil {
		log.Fatal(err)
	}
	vpnLn, err := net.Listen("tcp", vpnListen)
	if err != nil {
		meshLn.Close()
		log.Fatal(err)
	}
	log.Printf("relay mesh listening on %s, outbound upstream %s", meshListen, mesh.Upstream)
	log.Printf("relay vpn listening on %s, inbound upstream %s", vpnListen, vpn.Upstream)

	errCh := make(chan error, 2)
	go func() {
		errCh <- relay.Serve(ctx, meshLn, mesh)
	}()
	go func() {
		errCh <- relay.Serve(ctx, vpnLn, vpn)
	}()
	err = <-errCh
	stop()
	if err != nil {
		log.Fatal(err)
	}
	if err := <-errCh; err != nil {
		log.Fatal(err)
	}
}

func configFromEnv() (mesh, vpn relay.Config, meshListen, vpnListen string, err error) {
	domains, err := relay.ParseDomains(os.Getenv("MAIL_DOMAIN"))
	if err != nil {
		return relay.Config{}, relay.Config{}, "", "", err
	}
	inbound, err := relay.ParseUpstream("RELAY_INBOUND_UPSTREAM", os.Getenv("RELAY_INBOUND_UPSTREAM"), false)
	if err != nil {
		return relay.Config{}, relay.Config{}, "", "", err
	}
	outbound, err := relay.ParseUpstream("RELAY_OUTBOUND_UPSTREAM", os.Getenv("RELAY_OUTBOUND_UPSTREAM"), true)
	if err != nil {
		return relay.Config{}, relay.Config{}, "", "", err
	}
	allow, err := relay.ParseAllow(os.Getenv("RELAY_VPN_ALLOW"))
	if err != nil {
		return relay.Config{}, relay.Config{}, "", "", err
	}
	maxSize, err := parseMaxSize()
	if err != nil {
		return relay.Config{}, relay.Config{}, "", "", err
	}
	name, err := relayHostname()
	if err != nil {
		return relay.Config{}, relay.Config{}, "", "", err
	}
	base := relay.Config{
		Hostname: name,
		Domains:  domains,
		MaxSize:  maxSize,
		Timeout:  60 * time.Second,
	}
	mesh = base
	mesh.Outbound = true
	mesh.Upstream = outbound
	vpn = base
	vpn.Upstream = inbound
	vpn.Allow = allow
	meshListen = getenv("RELAY_MESH_LISTEN", ":50054")
	vpnListen = getenv("RELAY_VPN_LISTEN", ":50055")
	if meshListen == vpnListen {
		return relay.Config{}, relay.Config{}, "", "", fmt.Errorf("RELAY_MESH_LISTEN and RELAY_VPN_LISTEN must differ")
	}
	return mesh, vpn, meshListen, vpnListen, nil
}

func parseMaxSize() (int64, error) {
	raw := os.Getenv("RELAY_MAX_SIZE")
	if raw == "" {
		return 50 << 20, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("RELAY_MAX_SIZE must be a positive integer")
	}
	return n, nil
}

func relayHostname() (string, error) {
	if name := os.Getenv("RELAY_HOSTNAME"); name != "" {
		return name, nil
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "localhost", nil
	}
	return name, nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
