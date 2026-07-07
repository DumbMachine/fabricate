// Command fab-httpmock-server is the stateful HTTP-API mock fab's httpmock
// engine runs in a container. One binary hosts many services (a registry);
// MOCK_SERVICE selects which one this container serves. Each service is backed
// by a fresh per-container SQLite database seeded from the mounted fixture, so
// writes (creates, updates, custom actions) take effect and subsequent reads
// reflect them — the mock behaves like the real API.
//
// Env:
//
//	MOCK_SERVICE  required — which registered service to serve (e.g. google-play)
//	SEED_FILE     fixture path (default /seed.json)
//	DB_PATH       sqlite file (default /tmp/mock.db; recreated fresh each boot)
//	PORT          listen port (default 8080)
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dumbmachine/fabricate/mockd/mock"
	"github.com/dumbmachine/fabricate/mockd/services/cloudflare"
	"github.com/dumbmachine/fabricate/mockd/services/fly"
	"github.com/dumbmachine/fabricate/mockd/services/gmail"
	"github.com/dumbmachine/fabricate/mockd/services/googleplay"
	"github.com/dumbmachine/fabricate/mockd/services/linear"
	"github.com/dumbmachine/fabricate/mockd/services/railway"
	"github.com/dumbmachine/fabricate/mockd/services/render"
	"github.com/dumbmachine/fabricate/mockd/services/supabase"
	"github.com/dumbmachine/fabricate/mockd/services/vercel"
)

// registry maps a MOCK_SERVICE slug to its factory. Add a service by importing
// its package and adding an entry — plus the same slug in
// engine/httpmock's KnownServices, which is the CLI's discovery copy
// of this map (the two builds can't import each other).
var registry = map[string]func() *mock.Service{
	"google-play": googleplay.New,
	"gmail":       gmail.New,
	"linear":      linear.New,
	"cloudflare":  cloudflare.New,
	"vercel":      vercel.New,
	"supabase":    supabase.New,
	"render":      render.New,
	"fly":         fly.New,
	"railway":     railway.New,
}

func main() {
	name := os.Getenv("MOCK_SERVICE")
	if name == "" {
		log.Fatalf("fab-httpmock: MOCK_SERVICE is required (have: %s)", strings.Join(serviceNames(), ", "))
	}
	factory, ok := registry[name]
	if !ok {
		log.Fatalf("fab-httpmock: unknown MOCK_SERVICE %q (have: %s)", name, strings.Join(serviceNames(), ", "))
	}
	seedPath := envOr("SEED_FILE", "/seed.json")
	dbPath := envOr("DB_PATH", "/tmp/mock.db")
	port := envOr("PORT", "8080")

	// Fresh DB per boot: drop any stale file so a container restart re-seeds
	// cleanly rather than doubling rows.
	_ = os.Remove(dbPath)

	var fixture []byte
	if b, err := os.ReadFile(seedPath); err == nil {
		fixture = b
	} else if !os.IsNotExist(err) {
		log.Fatalf("fab-httpmock: read seed %q: %v", seedPath, err)
	}

	svc := factory()
	if err := svc.Init(dbPath, fixture); err != nil {
		log.Fatalf("fab-httpmock: init %s: %v", name, err)
	}
	log.Printf("fab-httpmock: serving %q on :%s (seed=%s, db=%s)", name, port, seedPath, dbPath)
	if err := http.ListenAndServe(":"+port, svc); err != nil {
		log.Fatalf("fab-httpmock: listen: %v", err)
	}
}

func serviceNames() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
