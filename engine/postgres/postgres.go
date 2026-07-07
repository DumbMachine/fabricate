// Package postgres is fab's Postgres engine: testcontainers-go
// brings up a postgres container, fab seeds it via init scripts +
// CREATE EXTENSION, returns connection creds.
//
// Seeding is done via the postgres image's first-boot mechanism:
// every file dropped into /docker-entrypoint-initdb.d/ is run in
// lexicographic order against the default database, by the
// superuser, before the container is considered healthy. We
// prepend a synthesized 00-extensions.sql for the profile's
// `extensions` list and copy each profile seed file with a numeric
// prefix that preserves authoring order.
package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

const (
	// Engine is the slug profiles set in `engine:` and the key the
	// cmd-layer registry maps to this package.
	Engine = "postgres"

	defaultImage = "postgres:16-alpine"
)

// New returns a Postgres engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   defaultImage,
		DefaultPort:    5432,
		SupportedSeeds: []string{profile.SeedTypeSQL},
		Description:    "Postgres relational database, seeded via /docker-entrypoint-initdb.d/.",
	}
}

func (e eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	db := engine.OrDefault(p.Defaults.Database, "fab")
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	image := engine.OrDefault(p.Image, defaultImage)

	// testcontainers' WithInitScripts expects host paths, so we copy
	// out of the (possibly embedded) profile FS into a tempdir.
	stageDir, scripts, err := stageInitScripts(p)
	if err != nil {
		return nil, fmt.Errorf("postgres engine: stage init scripts: %w", err)
	}
	defer os.RemoveAll(stageDir)

	customizers := []testcontainers.ContainerCustomizer{
		tcpostgres.WithDatabase(db),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(pass),
	}
	if len(scripts) > 0 {
		customizers = append(customizers, tcpostgres.WithInitScripts(scripts...))
	}
	if t := engine.ParseTimeout(p.Healthcheck.Timeout, 90*time.Second); t > 0 {
		customizers = append(customizers, withLogWait(t))
	}

	container, err := tcpostgres.Run(ctx, image, customizers...)
	if err != nil {
		// testcontainers returns a non-nil container even on wait
		// failures so the caller can clean up. Ryuk is disabled in
		// fab, so without this terminate we'd leak a stopped
		// container into `docker ps -a` on every aborted create.
		if container != nil {
			_ = container.Terminate(context.Background())
		}
		return nil, fmt.Errorf("postgres engine: container start: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("postgres engine: host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("postgres engine: port: %w", err)
	}
	port := mappedPort.Int()

	connURL := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		user, pass, host, port, db)

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: container.GetContainerID(),
		Creds: engine.Creds{
			Engine:   Engine,
			Host:     host,
			Port:     port,
			Username: user,
			Password: pass,
			Database: db,
			URL:      connURL,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	// docker CLI is assumed to point at the same daemon testcontainers
	// used during Create. Holds for the single-daemon local setup we
	// target; document the assumption if we ever support remote
	// DOCKER_HOST.
	return engine.DockerRM(ctx, containerID)
}

// stageInitScripts materializes the profile's extensions + seed
// files into a tempdir, numbered so /docker-entrypoint-initdb.d/
// runs them in the intended order.
func stageInitScripts(p *profile.Profile) (string, []string, error) {
	dir, err := os.MkdirTemp("", "fab-init-*")
	if err != nil {
		return "", nil, err
	}
	var out []string

	if len(p.Extensions) > 0 {
		var b strings.Builder
		for _, ext := range p.Extensions {
			fmt.Fprintf(&b, "CREATE EXTENSION IF NOT EXISTS %q;\n", ext)
		}
		path := filepath.Join(dir, "00-fab-extensions.sql")
		if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
			os.RemoveAll(dir)
			return "", nil, err
		}
		out = append(out, path)
	}

	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeSQL {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("postgres engine: seed step %d type=%q not supported (sql only)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("postgres engine: read seed %q: %w", step.File, err)
		}
		if prefix := seedEnvSQL(p.Env); prefix != "" {
			data = append([]byte(prefix), data...)
		}
		// Numeric prefix preserves profile-authored ordering while
		// falling after 00-fab-extensions.sql in lex order, which is
		// all /docker-entrypoint-initdb.d/ honors.
		name := fmt.Sprintf("%02d-%s", i+10, filepath.Base(step.File))
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			os.RemoveAll(dir)
			return "", nil, err
		}
		out = append(out, path)
	}
	return dir, out, nil
}

func seedEnvSQL(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("-- fab profile env injected by the postgres engine.\n")
	for _, key := range keys {
		setting := "fab.seed." + normalizeSeedEnvKey(key)
		fmt.Fprintf(
			&b,
			"SELECT set_config('%s', '%s', false);\n",
			escapeSQLLiteral(setting),
			escapeSQLLiteral(env[key]),
		)
	}
	b.WriteString("\n")
	return b.String()
}

func normalizeSeedEnvKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	var b strings.Builder
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func escapeSQLLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// withLogWait mirrors the postgres module's BasicWaitStrategies
// (log probe twice + port listener), with a profile-tunable
// timeout. The port check is load-bearing on macOS where Docker
// Desktop's port proxy is asynchronous.
func withLogWait(t time.Duration) testcontainers.ContainerCustomizer {
	logStrat := tcwait.ForLog("database system is ready to accept connections").
		WithOccurrence(2).
		WithStartupTimeout(t)
	portStrat := tcwait.ForListeningPort("5432/tcp").WithStartupTimeout(t)
	return testcontainers.WithWaitStrategy(logStrat, portStrat)
}
