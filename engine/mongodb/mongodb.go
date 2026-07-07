// Package mongodb is fab's MongoDB engine. testcontainers-go starts
// `mongo:7`; fab copies the profile's .js seed scripts into the
// image's first-boot dir (/docker-entrypoint-initdb.d/) — the
// entrypoint runs them in lex order against the admin database via
// `mongosh`. We don't shell out to `mongoimport` ourselves; the
// official mongo image also picks up .sh scripts there, so a
// profile that needs collection-import semantics can include a
// wrapper shell script.
package mongodb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/testcontainers/testcontainers-go"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
)

const (
	Engine       = "mongodb"
	defaultImage = "mongo:7"
)

// New returns a MongoDB engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:         Engine,
		DefaultImage: defaultImage,
		DefaultPort:  27017,
		SupportedSeeds: []string{
			profile.SeedTypeJS,
			profile.SeedTypeMongoImport,
			profile.SeedTypeShell,
		},
		Description: "MongoDB document store, seeded via /docker-entrypoint-initdb.d/ (.js for mongosh, .sh for shell).",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	// Mongo treats the "database" we connect to as just a default
	// auth source; collections are addressed by name. Profile authors
	// who care about the default db set it; otherwise fall back.
	db := engine.OrDefault(p.Defaults.Database, "fab")
	image := engine.OrDefault(p.Image, defaultImage)

	stageDir, mounts, err := stageInitScripts(p)
	if err != nil {
		return nil, fmt.Errorf("mongodb engine: stage init scripts: %w", err)
	}
	defer os.RemoveAll(stageDir)

	customizers := []testcontainers.ContainerCustomizer{
		tcmongo.WithUsername(user),
		tcmongo.WithPassword(pass),
	}
	if len(mounts) > 0 {
		// One CustomizeRequest call carries the full list of bind
		// mounts into the container request — there's no WithFiles
		// in v0.34.
		customizers = append(customizers, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{Files: mounts},
		}))
	}
	t := engine.ParseTimeout(p.Healthcheck.Timeout, 120*time.Second)
	customizers = append(customizers, withLogWait(t))

	container, err := tcmongo.Run(ctx, image, customizers...)
	if err != nil {
		if container != nil {
			_ = container.Terminate(context.Background())
		}
		return nil, fmt.Errorf("mongodb engine: container start: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("mongodb engine: host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("mongodb engine: port: %w", err)
	}
	port := mappedPort.Int()

	// User/pass go through url.User so a `@` in either is escaped
	// before it lands in the host position.
	u := &url.URL{
		Scheme: "mongodb",
		User:   url.UserPassword(user, pass),
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/" + db,
	}
	// Auth source defaults to the connection db, but the mongo image
	// creates the root user in `admin`. Pin authSource so the
	// connection URL works on first try.
	q := u.Query()
	q.Set("authSource", "admin")
	u.RawQuery = q.Encode()

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
			URL:      u.String(),
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	return engine.DockerRM(ctx, containerID)
}

// stageInitScripts writes each .js / .sh seed into a tempdir and
// returns ContainerFile mounts pointing at
// /docker-entrypoint-initdb.d/. The mongo image runs *.js with
// `mongosh` and *.sh as plain shell; we infer the on-container
// filename from the profile-supplied type.
func stageInitScripts(p *profile.Profile) (string, []testcontainers.ContainerFile, error) {
	if len(p.Seed) == 0 {
		return "", nil, nil
	}
	dir, err := os.MkdirTemp("", "fab-mongo-init-*")
	if err != nil {
		return "", nil, err
	}
	var files []testcontainers.ContainerFile
	for i, step := range p.Seed {
		var ext string
		switch step.Type {
		case profile.SeedTypeJS:
			ext = "js"
		case profile.SeedTypeMongoImport, profile.SeedTypeShell:
			// mongoimport is conventional inside a wrapper shell
			// script; we let the profile drop a .sh that calls
			// mongoimport with files staged alongside.
			ext = "sh"
		default:
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("mongodb engine: seed step %d type=%q not supported (js, mongoimport, shell)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("mongodb engine: read seed %q: %w", step.File, err)
		}
		if step.Type == profile.SeedTypeJS {
			if prefix, err := seedEnvJS(p.Env); err != nil {
				os.RemoveAll(dir)
				return "", nil, err
			} else if prefix != "" {
				data = append([]byte(prefix), data...)
			}
		}
		name := fmt.Sprintf("%02d-%s.%s", i+10, sanitizeFilename(filepath.Base(step.File)), ext)
		hostPath := filepath.Join(dir, name)
		if err := os.WriteFile(hostPath, data, 0o644); err != nil {
			os.RemoveAll(dir)
			return "", nil, err
		}
		containerPath := "/docker-entrypoint-initdb.d/" + name
		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      hostPath,
			ContainerFilePath: containerPath,
			FileMode:          0o644,
		})
	}
	return dir, files, nil
}

func seedEnvJS(env map[string]string) (string, error) {
	ordered := make(map[string]string, len(env))
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ordered[key] = env[key]
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("mongodb engine: encode profile env: %w", err)
	}
	return fmt.Sprintf(`// fab profile env injected by the mongodb engine.
const FAB_ENV = Object.freeze(%s);
function fabEnvInt(key, fallback) {
  const raw = FAB_ENV[key];
  if (raw === undefined || raw === null || String(raw).trim() === '') return fallback;
  const n = Number.parseInt(String(raw), 10);
  if (!Number.isFinite(n)) throw new Error('Invalid integer FAB_ENV ' + key + '=' + raw);
  return n;
}

`, raw), nil
}

// sanitizeFilename strips any extension from the seed file basename
// so we get a clean numbered prefix + a consistent extension based
// on seed type. Avoids `00-events.js.js`.
func sanitizeFilename(name string) string {
	if ext := filepath.Ext(name); ext != "" {
		return name[:len(name)-len(ext)]
	}
	return name
}

func withLogWait(t time.Duration) testcontainers.ContainerCustomizer {
	logStrat := tcwait.ForLog("Waiting for connections").WithStartupTimeout(t)
	portStrat := tcwait.ForListeningPort("27017/tcp").WithStartupTimeout(t)
	return testcontainers.WithWaitStrategy(logStrat, portStrat)
}
