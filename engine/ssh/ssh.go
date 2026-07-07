// Package ssh is fab's SSH engine. Spins up linuxserver/openssh-server,
// generates an ed25519 keypair, injects the public key as the
// container user's authorized_keys, and returns the private key
// inline on Creds (plus a copy written to ~/.config/fab/keys/<id>
// so callers can `ssh -i` directly).
//
// Seed steps of type `shell` are copied into the container and
// executed as root after sshd is healthy. Use them to provision
// extra users, drop fake logs, or install packages.
//
// Why linuxserver/openssh-server: it's the most ergonomic public
// sshd image — env-driven configuration (PUBLIC_KEY, USER_NAME),
// Alpine-base, and predictable port (2222). The "official" openssh
// would force us to build a custom image just to seed authorized
// keys.
package ssh

import (
	"context"
	"fmt"
	"net/url"
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
	Engine       = "ssh"
	defaultImage = "lscr.io/linuxserver/openssh-server:latest"

	// linuxserver/openssh-server listens on 2222 inside the
	// container. We map it to a random host port via testcontainers'
	// usual mechanism.
	defaultPort = "2222/tcp"
)

// New returns an SSH engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   defaultImage,
		DefaultPort:    2222,
		SupportedSeeds: []string{profile.SeedTypeShell},
		Description:    "Linuxserver OpenSSH server with a fab-generated ed25519 key. Seeds run as root inside the container after sshd is up.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	user := engine.OrDefault(p.Defaults.Username, "fab")
	image := engine.OrDefault(p.Image, defaultImage)

	pub, priv, err := generateKeypair()
	if err != nil {
		return nil, fmt.Errorf("ssh engine: generate keypair: %w", err)
	}

	stageDir, seedFiles, err := stageShellSeeds(p)
	if err != nil {
		return nil, fmt.Errorf("ssh engine: stage seeds: %w", err)
	}
	defer os.RemoveAll(stageDir)

	envVars := map[string]string{
		"PUBLIC_KEY":      pub,
		"USER_NAME":       user,
		"PASSWORD_ACCESS": "false",
		"SUDO_ACCESS":     "true",
		"PUID":            "1000",
		"PGID":            "1000",
	}
	for k, v := range p.Env {
		envVars[k] = v
	}

	files := make([]testcontainers.ContainerFile, 0, len(seedFiles))
	for _, f := range seedFiles {
		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      f.hostPath,
			ContainerFilePath: f.containerPath,
			FileMode:          0o755,
		})
	}

	t := engine.ParseTimeout(p.Healthcheck.Timeout, 180*time.Second)
	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{defaultPort},
		Env:          envVars,
		Files:        files,
		// linuxserver/openssh-server uses s6-overlay, which boots
		// in stages and routes service logs through s6's own log
		// pipes. The exact "Server listening on" line from sshd
		// often never reaches docker logs, so gating on it (the way
		// we used to) made the wait spuriously time out even when
		// the port was already accepting connections. Port-listening
		// is the only check that actually maps to "fab can hand a
		// caller working creds." Bumped default startup to 180s
		// because the first cold pull of this image pushes past 90s
		// on slow links.
		WaitingFor: tcwait.ForAll(
			tcwait.ForListeningPort(defaultPort),
			tcwait.ForLog("[ls.io-init] done."),
		).WithDeadline(t),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		// testcontainers returns a non-nil container even on
		// wait-strategy failures so the caller can clean up. Ryuk
		// is disabled by fab, so without this terminate we'd leak a
		// stopped-but-not-removed container into the user's
		// `docker ps -a` on every timeout.
		if container != nil {
			_ = container.Terminate(context.Background())
		}
		return nil, fmt.Errorf("ssh engine: container start: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("ssh engine: host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, defaultPort)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("ssh engine: port: %w", err)
	}
	port := mappedPort.Int()

	containerID := container.GetContainerID()

	// Run shell seeds as root inside the container. linuxserver image
	// boots a fairly slim Alpine — keep seeds POSIX-sh-compatible.
	for _, f := range seedFiles {
		out, execErr := dockerExec(ctx, containerID, "/bin/sh", f.containerPath)
		if execErr != nil {
			_ = container.Terminate(ctx)
			return nil, fmt.Errorf("ssh engine: seed %s: %w: %s", filepath.Base(f.containerPath), execErr, strings.TrimSpace(out))
		}
	}

	// Persist the key so callers can `ssh -i ~/.config/fab/keys/<id>`
	// without parsing JSON. The PEM also rides on Creds so `fab
	// creds -o json` is self-contained.
	if _, err := writeKeyFile(containerID, priv); err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("ssh engine: persist key: %w", err)
	}

	u := &url.URL{
		Scheme: "ssh",
		User:   url.User(user),
		Host:   fmt.Sprintf("%s:%d", host, port),
	}

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: containerID,
		Creds: engine.Creds{
			Engine:     Engine,
			Host:       host,
			Port:       port,
			Username:   user,
			URL:        u.String(),
			PrivateKey: priv,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// KeyPath returns the on-disk path where the private key for a
// given container is stored.
func KeyPath(containerID string) string {
	return filepath.Join(keysDir(), containerID)
}

func keysDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "fab", "keys")
	}
	return filepath.Join(cfg, "fab", "keys")
}

func writeKeyFile(containerID, pem string) (string, error) {
	dir := keysDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, containerID)
	if err := os.WriteFile(path, []byte(pem), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	// Best-effort key removal: state cleanup is the cmd-layer's job,
	// but the key file is engine-specific so we own its lifecycle.
	_ = os.Remove(KeyPath(containerID))
	return engine.DockerRM(ctx, containerID)
}

type stagedSeed struct {
	hostPath      string
	containerPath string
}

func stageShellSeeds(p *profile.Profile) (string, []stagedSeed, error) {
	if len(p.Seed) == 0 {
		return "", nil, nil
	}
	dir, err := os.MkdirTemp("", "fab-ssh-*")
	if err != nil {
		return "", nil, err
	}
	out := make([]stagedSeed, 0, len(p.Seed))
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeShell {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("seed step %d type=%q not supported (shell only)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			os.RemoveAll(dir)
			return "", nil, fmt.Errorf("read seed %q: %w", step.File, err)
		}
		base := filepath.Base(step.File)
		hostPath := filepath.Join(dir, base)
		if err := os.WriteFile(hostPath, data, 0o755); err != nil {
			os.RemoveAll(dir)
			return "", nil, err
		}
		out = append(out, stagedSeed{
			hostPath:      hostPath,
			containerPath: fmt.Sprintf("/fab-seed/%02d-%s", i, base),
		})
	}
	return dir, out, nil
}
