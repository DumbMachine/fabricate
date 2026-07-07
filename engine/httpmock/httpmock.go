// Package httpmock is fab's stateful HTTP-API mock engine. It runs a single Go
// server (mockd/ at the repo root) that hosts many mock services backed by
// per-container SQLite; env.MOCK_SERVICE picks which one this container serves,
// and the profile's seed is a JSON fixture loaded into that service's tables at
// boot. Because state is real, writes (creates, updates, custom actions like
// Play's reviews:reply) take effect and later reads reflect them — the mock
// behaves like the actual API. Returns a base URL + a bearer token (the mock
// doesn't validate auth; the token exists so clients that insist on auth have one).
//
// Adding a service is a Go service package (schema + Express-style handlers,
// composing a generic CRUD baseline) + a profile (env.MOCK_SERVICE + a fixture)
// — no engine changes. We don't use vercel-labs/emulate: its
// catalog is fixed (no Android Publisher / Play Reporting) and it can't ingest a
// spec; this gives us full control per service.
//
// The image (fabricate/httpmock:local) is local-only today: build once with
// `make images`.
package httpmock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/testcontainers/testcontainers-go"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

const (
	// Engine is the slug profiles set in `engine:` and the key the cmd-layer
	// registry maps to this package.
	Engine = "httpmock"

	// defaultImage is the local dev tag built by `make images`.
	// $FAB_HTTPMOCK_IMAGE overrides it (see imageDefault) so a
	// published registry copy can be swapped in without a code change;
	// once a GHCR release exists this constant flips to the pinned tag
	// (docs/releasing.md).
	defaultImage = "fabricate/httpmock:local"
	defaultPort  = "8080/tcp"

	// defaultToken is the bearer Creds.Password falls back to when a profile
	// leaves defaults.password unset. The mock never checks it.
	defaultToken = "fab-mock-token"
)

// KnownServices lists the MOCK_SERVICE slugs the httpmock image serves.
// It MUST mirror the registry in mockd/main.go (the registry itself is
// compiled into the separately-built image, so the CLI keeps this copy
// for discovery: `fab profiles init <service>`, schema hints, errors).
// Each service documents its fixture shape in its package comment
// under mockd/services/<slug>/.
var KnownServices = []string{
	"cloudflare", "fly", "gmail", "google-play", "linear",
	"railway", "render", "supabase", "vercel",
}

// New returns an httpmock engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

// imageDefault resolves the image used when a profile doesn't pin one:
// $FAB_HTTPMOCK_IMAGE if set, else the local dev tag. Precedence
// overall is --image flag > profile image: > $FAB_HTTPMOCK_IMAGE >
// default.
func imageDefault() string {
	return engine.OrDefault(os.Getenv("FAB_HTTPMOCK_IMAGE"), defaultImage)
}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   imageDefault(),
		DefaultPort:    8080,
		SupportedSeeds: []string{profile.SeedTypeMockFixture},
		Description:    "Stateful HTTP-API mock. Hosts SQLite-backed mock services (env.MOCK_SERVICE picks one: " + strings.Join(KnownServices, ", ") + "), seeded from a JSON fixture; writes take effect. Returns base URL + bearer token.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	image := engine.OrDefault(p.Image, imageDefault())

	seedHostPath, cleanup, err := stageSeed(p)
	if err != nil {
		return nil, fmt.Errorf("httpmock engine: stage seed: %w", err)
	}
	defer cleanup()

	token := engine.OrDefault(p.Defaults.Password, defaultToken)
	t := engine.ParseTimeout(p.Healthcheck.Timeout, 60*time.Second)

	// The profile selects which mock service this container serves via
	// env.MOCK_SERVICE; the profile's env is passed through to the container.
	env := map[string]string{"SEED_FILE": "/seed.json", "PORT": "8080"}
	for k, v := range p.Env {
		env[k] = v
	}
	if env["MOCK_SERVICE"] == "" {
		return nil, fmt.Errorf("httpmock engine: profile %q must set env.MOCK_SERVICE (which mock service to serve)", p.Name)
	}

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{defaultPort},
		Env:          env,
		Files: []testcontainers.ContainerFile{
			{HostFilePath: seedHostPath, ContainerFilePath: "/seed.json", FileMode: 0o644},
		},
		// /healthz over HTTP is the readiness probe. We deliberately do NOT use
		// ForListeningPort: on the shell-less scratch image its internal port
		// check execs /bin/sh and logs "Shell not found in container". ForHTTP
		// dials the mapped port externally, so it already proves the server is
		// listening AND serving — no shell required.
		WaitingFor: tcwait.ForHTTP("/healthz").WithPort(defaultPort).WithStartupTimeout(t),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		return nil, fmt.Errorf("httpmock engine: container start: %w (image=%s — did you run `make images`?)", err, image)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("httpmock engine: host: %w", err)
	}
	mapped, err := c.MappedPort(ctx, defaultPort)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("httpmock engine: port: %w", err)
	}
	port := mapped.Int()

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: c.GetContainerID(),
		Creds: engine.Creds{
			Engine:   Engine,
			Host:     host,
			Port:     port,
			URL:      fmt.Sprintf("http://%s:%d", host, port),
			Password: token, // overloaded as the bearer, mirrors the github engine
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	return engine.DockerRM(ctx, containerID)
}

// stageSeed reads the single mock-fixture seed step and writes it to a temp file
// for bind-mounting at /seed.json. Returns the host path and a cleanup func.
func stageSeed(p *profile.Profile) (string, func(), error) {
	var step *profile.SeedStep
	for i := range p.Seed {
		s := p.Seed[i]
		if s.Type != profile.SeedTypeMockFixture {
			return "", nil, fmt.Errorf("seed step %d type=%q not supported (want %q)", i, s.Type, profile.SeedTypeMockFixture)
		}
		if step != nil {
			return "", nil, fmt.Errorf("only one %q seed step allowed", profile.SeedTypeMockFixture)
		}
		step = &s
	}
	if step == nil {
		return "", nil, fmt.Errorf("profile must declare exactly one %q seed step", profile.SeedTypeMockFixture)
	}
	data, err := p.ReadSeed(*step)
	if err != nil {
		return "", nil, fmt.Errorf("read seed %q: %w", step.File, err)
	}
	dir, err := os.MkdirTemp("", "fab-httpmock-*")
	if err != nil {
		return "", nil, err
	}
	hostPath := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(hostPath, data, 0o644); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return hostPath, func() { os.RemoveAll(dir) }, nil
}
