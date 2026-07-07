// Package prometheus is fab's Prometheus engine. There's no
// testcontainers-go module for prom, so we spin a generic container
// with prom/prometheus and bind-mount the profile's config + rule
// files. Two seed types are supported:
//
//	prom-config — replaces /etc/prometheus/prometheus.yml verbatim.
//	prom-rule   — mounted under /etc/prometheus/rules/.
//
// If the profile omits prom-config, fab synthesizes a minimal one
// that scrapes Prometheus itself and pulls in every prom-rule file
// the profile shipped. That makes "empty + a single rule file"
// profiles trivial to author.
package prometheus

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
	Engine       = "prometheus"
	defaultImage = "prom/prometheus:v2.55.1"

	configPath  = "/etc/prometheus/prometheus.yml"
	rulesDir    = "/etc/prometheus/rules"
	defaultPort = "9090/tcp"
)

// New returns a Prometheus engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:         Engine,
		DefaultImage: defaultImage,
		DefaultPort:  9090,
		SupportedSeeds: []string{
			profile.SeedTypePromConfig,
			profile.SeedTypePromRule,
		},
		Description: "Prometheus 2.x with bind-mounted config + rule files. Omit prom-config and fab synthesizes one.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	image := engine.OrDefault(p.Image, defaultImage)

	stageDir, configBytes, ruleFiles, err := stage(p)
	if err != nil {
		return nil, fmt.Errorf("prometheus engine: stage seeds: %w", err)
	}
	defer os.RemoveAll(stageDir)

	// Materialize the (possibly-synthesized) prometheus.yml to disk
	// so we can hand testcontainers a real host path. Mode 0644 —
	// the prometheus user inside the container reads it, doesn't
	// write it.
	hostConfig := filepath.Join(stageDir, "prometheus.yml")
	if err := os.WriteFile(hostConfig, configBytes, 0o644); err != nil {
		return nil, fmt.Errorf("prometheus engine: write config: %w", err)
	}

	files := []testcontainers.ContainerFile{
		{HostFilePath: hostConfig, ContainerFilePath: configPath, FileMode: 0o644},
	}
	for _, rf := range ruleFiles {
		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      rf.hostPath,
			ContainerFilePath: filepath.Join(rulesDir, rf.baseName),
			FileMode:          0o644,
		})
	}

	t := engine.ParseTimeout(p.Healthcheck.Timeout, 60*time.Second)
	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{defaultPort},
		Files:        files,
		// The default entrypoint accepts CLI overrides; pin
		// --config.file so a custom image doesn't drift.
		Cmd: []string{
			"--config.file=" + configPath,
			"--storage.tsdb.path=/prometheus",
			"--web.console.libraries=/usr/share/prometheus/console_libraries",
			"--web.console.templates=/usr/share/prometheus/consoles",
			"--web.enable-lifecycle",
		},
		WaitingFor: tcwait.ForAll(
			tcwait.ForListeningPort(defaultPort).WithStartupTimeout(t),
			tcwait.ForHTTP("/-/ready").WithPort(defaultPort).WithStartupTimeout(t),
		),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if container != nil {
			_ = container.Terminate(context.Background())
		}
		return nil, fmt.Errorf("prometheus engine: container start: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("prometheus engine: host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, defaultPort)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("prometheus engine: port: %w", err)
	}
	port := mappedPort.Int()

	connURL := fmt.Sprintf("http://%s:%d", host, port)

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: container.GetContainerID(),
		Creds: engine.Creds{
			Engine: Engine,
			Host:   host,
			Port:   port,
			URL:    connURL,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	return engine.DockerRM(ctx, containerID)
}

type stagedRule struct {
	hostPath string
	baseName string
}

// stage walks the profile's seed steps. It pulls out at most one
// prom-config (config bytes) and any number of prom-rule files
// (written to the stage dir, returned as paths). When no
// prom-config is provided, stage synthesizes a default that scrapes
// Prometheus itself and references every staged rule file.
func stage(p *profile.Profile) (string, []byte, []stagedRule, error) {
	dir, err := os.MkdirTemp("", "fab-prom-*")
	if err != nil {
		return "", nil, nil, err
	}

	var (
		configBytes []byte
		rules       []stagedRule
	)
	for i, step := range p.Seed {
		data, err := p.ReadSeed(step)
		if err != nil {
			os.RemoveAll(dir)
			return "", nil, nil, fmt.Errorf("read seed %q: %w", step.File, err)
		}
		switch step.Type {
		case profile.SeedTypePromConfig:
			if configBytes != nil {
				os.RemoveAll(dir)
				return "", nil, nil, fmt.Errorf("seed step %d: only one prom-config allowed", i)
			}
			configBytes = data
		case profile.SeedTypePromRule:
			base := filepath.Base(step.File)
			hostPath := filepath.Join(dir, base)
			if err := os.WriteFile(hostPath, data, 0o644); err != nil {
				os.RemoveAll(dir)
				return "", nil, nil, err
			}
			rules = append(rules, stagedRule{hostPath: hostPath, baseName: base})
		default:
			os.RemoveAll(dir)
			return "", nil, nil, fmt.Errorf("seed step %d type=%q not supported (prom-config, prom-rule)", i, step.Type)
		}
	}

	if configBytes == nil {
		configBytes = []byte(synthConfig(rules))
	}
	return dir, configBytes, rules, nil
}

// synthConfig returns a sensible default prometheus.yml: 15s scrape
// of Prometheus itself, plus a rule_files list pointing at whatever
// the profile shipped. Authors who want a richer setup ship their
// own prom-config and reference rules manually.
func synthConfig(rules []stagedRule) string {
	var b strings.Builder
	b.WriteString("global:\n")
	b.WriteString("  scrape_interval: 15s\n")
	b.WriteString("  evaluation_interval: 15s\n")
	b.WriteString("\nscrape_configs:\n")
	b.WriteString("  - job_name: prometheus\n")
	b.WriteString("    static_configs:\n")
	b.WriteString("      - targets: [localhost:9090]\n")
	if len(rules) > 0 {
		b.WriteString("\nrule_files:\n")
		for _, r := range rules {
			fmt.Fprintf(&b, "  - %s/%s\n", rulesDir, r.baseName)
		}
	}
	return b.String()
}
