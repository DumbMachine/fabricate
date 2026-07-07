// Package kubernetes is fab's Kubernetes engine. Brings up a single-
// node k3s cluster in a privileged container and returns the full
// admin kubeconfig (server URL + CA cert + client cert/key) inline
// on Creds.Kubeconfig.
//
// Why k3s and not kind: k3s runs as one rootless-ish container and
// streams its kubeconfig out of /etc/rancher/k3s/k3s.yaml. kind runs
// docker-in-docker which needs different host privileges and adds a
// few extra seconds to startup. For per-test fixtures, k3s is the
// lower-friction choice.
//
// Seeds: v1 doesn't accept any seed types — k3s starts with the
// default namespaces and built-in addons (coredns, metrics-server,
// local-path provisioner). When we want pre-loaded manifests in a
// later slice, we'll add a "manifest" seed that funnels into the
// upstream k3s.WithManifest option.
package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"

	tck3s "github.com/testcontainers/testcontainers-go/modules/k3s"
)

const (
	Engine       = "kubernetes"
	defaultImage = "docker.io/rancher/k3s:v1.31.2-k3s1"

	// k3s listens on 6443 inside the container for the kube API; the
	// testcontainers module maps it to a random host port.
	defaultPort = 6443
)

// New returns a Kubernetes engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   defaultImage,
		DefaultPort:    defaultPort,
		SupportedSeeds: nil, // no seed types in v1
		Description:    "Single-node k3s cluster. Returns the admin kubeconfig (server URL + CA cert + client cert/key) inline on creds.kubeconfig.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	if len(p.Seed) > 0 {
		return nil, fmt.Errorf("kubernetes engine: seed steps not supported in v1 (profile %q has %d)", p.Name, len(p.Seed))
	}

	image := engine.OrDefault(p.Image, defaultImage)

	// k3s startup pulls the rancher image (~150MB) and waits for the
	// node-controller-sync log line. On a cold machine this can take
	// 90s; on a warm one it's ~10s. We give it a generous deadline
	// without overriding the caller's ctx.
	startCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	c, err := tck3s.Run(startCtx, image)
	if err != nil {
		return nil, fmt.Errorf("kubernetes engine: k3s run: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("kubernetes engine: host: %w", err)
	}
	port, err := c.MappedPort(ctx, "6443/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("kubernetes engine: mapped port: %w", err)
	}

	kubeconfig, err := c.GetKubeConfig(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("kubernetes engine: get kubeconfig: %w", err)
	}

	containerID := c.GetContainerID()
	// Persist the kubeconfig to disk so callers (and `fab creds -o env`)
	// have a real path to hand to kubectl/client-go. Same pattern as the
	// ssh engine's writeKeyFile.
	if _, err := writeKubeconfigFile(containerID, string(kubeconfig)); err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("kubernetes engine: persist kubeconfig: %w", err)
	}

	url := fmt.Sprintf("https://%s:%d", host, port.Int())

	inst := &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: containerID,
		Creds: engine.Creds{
			Engine:     Engine,
			Host:       host,
			Port:       port.Int(),
			URL:        url,
			Kubeconfig: string(kubeconfig),
			// Username surfaces what auth principal the kubeconfig
			// embeds — k3s's default user is named "default" in the
			// admin context. Cosmetic only.
			Username: defaultAdminUser(string(kubeconfig)),
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return inst, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	// DockerRM first: if it fails (daemon hiccup, container in use)
	// the cluster is still alive and the operator needs the kubeconfig
	// to drain it. Removing the file only after a successful rm avoids
	// orphaning the user from a running cluster they can no longer
	// reach. SSH's engine has the same ordering risk but its
	// equivalent (private key on disk) is mirrored into Creds.PrivateKey
	// so it's not strictly lost; kubeconfig only lives on disk, so
	// ordering matters more here.
	if err := engine.DockerRM(ctx, containerID); err != nil {
		return err
	}
	_ = os.Remove(KubeconfigPath(containerID))
	return nil
}

// KubeconfigPath returns the on-disk path where the admin kubeconfig
// for a given container is stored. Mirrors ssh.KeyPath so the cmd layer
// has a stable, fab-controlled path to hand to `kubectl --kubeconfig=...`.
func KubeconfigPath(containerID string) string {
	return filepath.Join(kubeconfigsDir(), containerID)
}

func kubeconfigsDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "fab", "kubeconfigs")
	}
	return filepath.Join(cfg, "fab", "kubeconfigs")
}

func writeKubeconfigFile(containerID, body string) (string, error) {
	dir := kubeconfigsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, containerID)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// defaultAdminUser scans kubeconfig YAML for the first "user:" name
// the contexts entry references. Best-effort — empty on parse failure
// since Username is purely cosmetic here. Avoids pulling in a YAML
// dep just for one display field.
func defaultAdminUser(kc string) string {
	const marker = "user: "
	for _, line := range strings.Split(kc, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, marker); idx >= 0 {
			return strings.TrimSpace(line[idx+len(marker):])
		}
	}
	return ""
}
