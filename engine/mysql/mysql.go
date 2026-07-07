// Package mysql is fab's MySQL engine. Same shape as the postgres
// engine: testcontainers-go brings up `mysql:8`, fab seeds it via
// /docker-entrypoint-initdb.d/, returns connection creds. We honor
// only `sql` seed steps — MySQL's init dir also accepts .sh and .gz,
// but profile authors who need either can shell out via a wrapper.
package mysql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

const (
	Engine       = "mysql"
	defaultImage = "mysql:8.4"
)

// New returns a MySQL engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   defaultImage,
		DefaultPort:    3306,
		SupportedSeeds: []string{profile.SeedTypeSQL},
		Description:    "MySQL relational database, seeded via /docker-entrypoint-initdb.d/.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	db := engine.OrDefault(p.Defaults.Database, "fab")
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	image := engine.OrDefault(p.Image, defaultImage)

	stageDir, scripts, err := stageInitScripts(p)
	if err != nil {
		return nil, fmt.Errorf("mysql engine: stage init scripts: %w", err)
	}
	defer os.RemoveAll(stageDir)

	customizers := []testcontainers.ContainerCustomizer{
		tcmysql.WithDatabase(db),
		tcmysql.WithUsername(user),
		tcmysql.WithPassword(pass),
	}
	if len(scripts) > 0 {
		customizers = append(customizers, tcmysql.WithScripts(scripts...))
	}
	// MySQL's first-boot is notoriously slow (the privilege tables get
	// initialized on cold image), so default well above postgres.
	t := engine.ParseTimeout(p.Healthcheck.Timeout, 180*time.Second)
	customizers = append(customizers, withLogWait(t))

	container, err := tcmysql.Run(ctx, image, customizers...)
	if err != nil {
		if container != nil {
			_ = container.Terminate(context.Background())
		}
		return nil, fmt.Errorf("mysql engine: container start: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("mysql engine: host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("mysql engine: port: %w", err)
	}
	port := mappedPort.Int()

	// MySQL DSNs aren't standardized the way postgres URLs are; the
	// go driver expects `user:pass@tcp(host:port)/db`. Most clients
	// also accept `mysql://user:pass@host:port/db`, so emit that —
	// it's the closer match to how every other fab URL reads.
	connURL := fmt.Sprintf("mysql://%s:%s@%s:%d/%s", user, pass, host, port, db)

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
	return engine.DockerRM(ctx, containerID)
}

func stageInitScripts(p *profile.Profile) (string, []string, error) {
	dir, err := os.MkdirTemp("", "fab-mysql-init-*")
	if err != nil {
		return "", nil, err
	}
	var out []string
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeSQL {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("mysql engine: seed step %d type=%q not supported (sql only)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("mysql engine: read seed %q: %w", step.File, err)
		}
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

// withLogWait mirrors the mysql module's default readiness strategy
// but lets the profile bump the timeout. MySQL writes two distinct
// "ready for connections" lines on first boot (init phase, then
// final start) — wait for the second to dodge a race where seeds
// run between the two and the client connects mid-init.
func withLogWait(t time.Duration) testcontainers.ContainerCustomizer {
	logStrat := tcwait.ForLog("ready for connections").
		WithOccurrence(2).
		WithStartupTimeout(t)
	portStrat := tcwait.ForListeningPort("3306/tcp").WithStartupTimeout(t)
	return testcontainers.WithWaitStrategy(tcwait.ForAll(logStrat, portStrat).WithDeadline(t))
}
