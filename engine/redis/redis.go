// Package redis is fab's Redis engine. Unlike postgres/mysql/mongo
// there's no first-boot script mechanism on the official redis
// image, so seeding happens out-of-band: we spin the container,
// wait for the port, then pipe each seed file into `redis-cli`
// inside the container via `docker exec`. Files are RESP/CLI
// commands, one per line (the format `redis-cli < file.txt` consumes).
package redis

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

const (
	Engine       = "redis"
	defaultImage = "redis:7.4-alpine"
)

// New returns a Redis engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   defaultImage,
		DefaultPort:    6379,
		SupportedSeeds: []string{profile.SeedTypeRedisCLI},
		Description:    "Redis key/value store with auth on. Seeds are RESP/CLI command lines piped into redis-cli.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	// Redis has no concept of a "username" pre-ACL (Redis 6+ added
	// ACL users; the default user is "default"). Profiles can pin a
	// password; if blank, we generate one and configure the container
	// with `--requirepass`.
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	user := engine.OrDefault(p.Defaults.Username, "default")
	// Redis' "database" is a 0-15 numeric index, not a name. Stash
	// the requested index on Creds.Database as a string so the env
	// output can surface it.
	dbIdx := engine.OrDefault(p.Defaults.Database, "0")
	image := engine.OrDefault(p.Image, defaultImage)

	// The redis module's WithConfigFile is the supported way to
	// bake settings in. We synthesize a tiny redis.conf with
	// `requirepass` on a tempdir and hand it the path; defaults for
	// every other knob come from the redis binary itself.
	confDir, confPath, err := writeConfig(pass)
	if err != nil {
		return nil, fmt.Errorf("redis engine: write config: %w", err)
	}
	defer os.RemoveAll(confDir)

	t := engine.ParseTimeout(p.Healthcheck.Timeout, 60*time.Second)
	customizers := []testcontainers.ContainerCustomizer{
		tcredis.WithConfigFile(confPath),
		withLogWait(t),
	}

	container, err := tcredis.Run(ctx, image, customizers...)
	if err != nil {
		if container != nil {
			_ = container.Terminate(context.Background())
		}
		return nil, fmt.Errorf("redis engine: container start: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("redis engine: host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("redis engine: port: %w", err)
	}
	port := mappedPort.Int()

	// Redis URL conventions: `redis://:password@host:port/db` (user
	// is empty for the default user). url.UserPassword handles the
	// "no username, just password" case via an explicit empty user.
	u := &url.URL{
		Scheme: "redis",
		User:   url.UserPassword("", pass),
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/" + dbIdx,
	}

	containerID := container.GetContainerID()

	if err := runSeedSteps(ctx, containerID, pass, dbIdx, p); err != nil {
		// Tear down so a partial seed doesn't leave a confusing
		// container alive in state.
		_ = container.Terminate(ctx)
		return nil, err
	}

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: containerID,
		Creds: engine.Creds{
			Engine:   Engine,
			Host:     host,
			Port:     port,
			Username: user,
			Password: pass,
			Database: dbIdx,
			URL:      u.String(),
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	return engine.DockerRM(ctx, containerID)
}

// runSeedSteps shells each redis-cli seed into the container via
// `docker exec -i`. Stdin of redis-cli accepts inline commands the
// same way the interactive shell does — one per line. We pass -a
// because requirepass is on; --no-auth-warning suppresses the
// "Using a password" notice on stderr so a clean profile load
// doesn't print noise.
func runSeedSteps(ctx context.Context, containerID, pass, dbIdx string, p *profile.Profile) error {
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeRedisCLI {
			return fmt.Errorf("redis engine: seed step %d type=%q not supported (redis-cli only)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			return fmt.Errorf("redis engine: read seed %q: %w", step.File, err)
		}
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", containerID,
			"redis-cli", "-a", pass, "--no-auth-warning", "-n", dbIdx)
		cmd.Stdin = bytes.NewReader(data)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("redis engine: seed %q via redis-cli: %w: %s", step.File, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// writeConfig synthesizes the minimum redis.conf the engine needs:
// just `requirepass`. Everything else inherits the binary defaults,
// which matches what `docker run redis:7-alpine` gets you out of
// the box. Returns the tempdir for cleanup plus the path to pass
// to WithConfigFile.
func writeConfig(pass string) (string, string, error) {
	dir, err := os.MkdirTemp("", "fab-redis-*")
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, "redis.conf")
	body := fmt.Sprintf("requirepass %s\n", pass)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	return dir, path, nil
}

func withLogWait(t time.Duration) testcontainers.ContainerCustomizer {
	logStrat := tcwait.ForLog("Ready to accept connections").WithStartupTimeout(t)
	portStrat := tcwait.ForListeningPort("6379/tcp").WithStartupTimeout(t)
	return testcontainers.WithWaitStrategy(logStrat, portStrat)
}
