// Package github is fab's GitHub REST API engine. It runs vercel-labs'
// `emulate` Node package inside a container, configured to serve the
// `github` service — a fully stateful, in-memory GitHub API on a random
// host port. Use it to drive agents / SDKs / integration tests against
// "real GitHub" without network or auth ceremony.
//
// What you get:
//   - HTTP server on a random host port (Creds.URL).
//   - One bearer token (Creds.Password) wired to a seeded user.
//   - Whatever orgs / repos / issues / labels / PRs the profile's
//     seed.yaml declares.
//
// The image (fabricate/emulate:local) is generic — service is picked
// via EMULATE_SERVICE env at container start — so when we add stripe /
// slack engines they reuse the same baked image.
//
// The image is local-only today: build it once with `make images`
// (top-level Makefile) before `fab create github -p ...`. The engine
// fails with a clear "image not cached locally" message if you forget.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/testcontainers/testcontainers-go"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"
)

const (
	// Engine is the slug profiles set in `engine:` and the key the
	// cmd-layer registry maps to this package.
	Engine = "github"

	// defaultImage is the local dev tag built by `make images`.
	// $FAB_EMULATE_IMAGE overrides it (see imageDefault) so a published
	// registry copy can be swapped in without a code change; once a
	// GHCR release exists this constant flips to the pinned tag
	// (docs/releasing.md).
	defaultImage = "fabricate/emulate:local"

	// emulate listens on 4000 inside the container; testcontainers
	// maps it to a random host port.
	defaultPort = "4000/tcp"

	// defaultToken is the bearer token Creds.Password falls back to
	// when a profile leaves defaults.password unset. Profile seeds in
	// this repo declare the same value under `tokens:`. If you author
	// a custom profile, keep the two in sync — the engine doesn't
	// parse the seed YAML to extract the token.
	defaultToken = "ghp_fab_dev"
)

// New returns a GitHub engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

// imageDefault resolves the image used when a profile doesn't pin one:
// $FAB_EMULATE_IMAGE if set, else the local dev tag. Precedence overall
// is --image flag > profile image: > $FAB_EMULATE_IMAGE > default.
func imageDefault() string {
	return engine.OrDefault(os.Getenv("FAB_EMULATE_IMAGE"), defaultImage)
}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   imageDefault(),
		DefaultPort:    4000,
		SupportedSeeds: []string{profile.SeedTypeEmulateConfig, profile.SeedTypeEmulatePostSeed},
		Description:    "Stateful GitHub REST API emulator (vercel-labs/emulate). Returns base URL + bearer token. emulate-config seeds users/orgs/repos/tokens at boot; emulate-postseed replays API calls (issues/labels/milestones/etc.) after the container is healthy.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	image := engine.OrDefault(p.Image, imageDefault())

	stageDir, seedHostPath, seedBase, postseed, err := stageSeed(p)
	if err != nil {
		return nil, fmt.Errorf("github engine: stage seed: %w", err)
	}
	defer os.RemoveAll(stageDir)

	token := engine.OrDefault(p.Defaults.Password, defaultToken)

	t := engine.ParseTimeout(p.Healthcheck.Timeout, 60*time.Second)
	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{defaultPort},
		Env: map[string]string{
			"EMULATE_SERVICE": Engine,
		},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      seedHostPath,
				ContainerFilePath: "/seed.yaml",
				FileMode:          0o644,
			},
		},
		WaitingFor: tcwait.ForAll(
			// Port-listen first: emulate binds before it's accepted a
			// request, but the bind is the moment it's safe to fan out
			// HTTP traffic at it.
			tcwait.ForListeningPort(defaultPort).WithStartupTimeout(t),
			// `/zen` is the simplest unauthenticated endpoint emulate
			// implements — a string body, 200 OK. Cheap readiness probe
			// that also proves the github service (not some other
			// EMULATE_SERVICE) is actually serving.
			tcwait.ForHTTP("/zen").WithPort(defaultPort).WithStartupTimeout(t),
		),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		return nil, fmt.Errorf("github engine: container start: %w (seed=%s, image=%s — did you run `make images`?)", err, seedBase, image)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("github engine: host: %w", err)
	}
	mapped, err := c.MappedPort(ctx, defaultPort)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("github engine: port: %w", err)
	}
	port := mapped.Int()
	url := fmt.Sprintf("http://%s:%d", host, port)

	// Replay any postseed steps in profile order, against the live
	// emulator. Order matters: labels before issues that reference
	// them, milestones before issues that reference them, etc.
	for i, raw := range postseed {
		if err := replayPostSeed(ctx, url, token, raw); err != nil {
			_ = c.Terminate(context.Background())
			return nil, fmt.Errorf("github engine: postseed step %d: %w", i, err)
		}
	}

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: c.GetContainerID(),
		Creds: engine.Creds{
			Engine: Engine,
			Host:   host,
			Port:   port,
			URL:    url,
			// Password is overloaded here as the bearer token. Mirrors
			// how prometheus + http-shaped engines lean on existing
			// Creds fields rather than widening the type. `fab creds
			// --env` will print it as FAB_PASSWORD; combine with
			// `Authorization: Bearer $FAB_PASSWORD` against $FAB_URL.
			Password: token,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	return engine.DockerRM(ctx, containerID)
}

// stageSeed splits profile seeds into the (single) emulate-config
// payload that gets bind-mounted at /seed.yaml and the (zero-or-more)
// emulate-postseed payloads the engine replays after the container is
// healthy. Returns (tempdir, hostPath, baseName, postseedPayloads).
// Caller defers RemoveAll(tempdir).
func stageSeed(p *profile.Profile) (string, string, string, [][]byte, error) {
	var seed *profile.SeedStep
	var postseedBytes [][]byte
	for i, s := range p.Seed {
		switch s.Type {
		case profile.SeedTypeEmulateConfig:
			if seed != nil {
				return "", "", "", nil, fmt.Errorf("only one emulate-config seed step allowed (got at least two)")
			}
			ss := s
			seed = &ss
		case profile.SeedTypeEmulatePostSeed:
			raw, err := p.ReadSeed(s)
			if err != nil {
				return "", "", "", nil, fmt.Errorf("read postseed %q: %w", s.File, err)
			}
			postseedBytes = append(postseedBytes, raw)
		default:
			return "", "", "", nil, fmt.Errorf("seed step %d type=%q not supported (emulate-config, emulate-postseed)", i, s.Type)
		}
	}
	if seed == nil {
		return "", "", "", nil, fmt.Errorf("profile must declare exactly one emulate-config seed step")
	}
	data, err := p.ReadSeed(*seed)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("read seed %q: %w", seed.File, err)
	}
	dir, err := os.MkdirTemp("", "fab-github-*")
	if err != nil {
		return "", "", "", nil, err
	}
	base := filepath.Base(seed.File)
	hostPath := filepath.Join(dir, base)
	if err := os.WriteFile(hostPath, data, 0o644); err != nil {
		os.RemoveAll(dir)
		return "", "", "", nil, err
	}
	return dir, hostPath, base, postseedBytes, nil
}

// postSeedDoc is the YAML shape of an emulate-postseed file. Each call
// describes one HTTP request to replay against the live emulator. The
// engine runs them in order and aborts on the first non-2xx response.
//
// We use a struct (not a free-form map) so a typo in `method:` or
// `path:` surfaces at parse time instead of silently no-op'ing.
type postSeedDoc struct {
	Calls []postSeedCall `yaml:"calls"`
}

type postSeedCall struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	// Body is whatever YAML the author wrote — we JSON-marshal it for
	// the request body. nil/absent = no body.
	Body any `yaml:"body,omitempty"`
}

// replayPostSeed reads one postseed YAML payload and replays each call
// against baseURL with `Authorization: Bearer <token>`. Aborts on the
// first non-2xx response so a broken postseed never silently leaves
// the fixture half-populated.
func replayPostSeed(ctx context.Context, baseURL, token string, raw []byte) error {
	var doc postSeedDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	if len(doc.Calls) == 0 {
		return fmt.Errorf("no `calls:` entries")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	base := strings.TrimRight(baseURL, "/")
	for i, call := range doc.Calls {
		if call.Method == "" || call.Path == "" {
			return fmt.Errorf("call %d: method + path required", i)
		}
		var body io.Reader
		if call.Body != nil {
			// yaml.Unmarshal produces map[string]any for objects; JSON
			// marshal handles that shape cleanly.
			jb, err := json.Marshal(call.Body)
			if err != nil {
				return fmt.Errorf("call %d (%s %s): encode body: %w", i, call.Method, call.Path, err)
			}
			body = bytes.NewReader(jb)
		}
		req, err := http.NewRequestWithContext(ctx, call.Method, base+call.Path, body)
		if err != nil {
			return fmt.Errorf("call %d (%s %s): build request: %w", i, call.Method, call.Path, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("call %d (%s %s): %w", i, call.Method, call.Path, err)
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("call %d (%s %s): status %d: %s",
				i, call.Method, call.Path, resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
	}
	return nil
}
