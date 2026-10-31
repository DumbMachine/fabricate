package console

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dumbmachine/fabricate/environment"
)

// Config is how this console process is hosted. Sandboxes started from the
// console follow it. A start request cannot choose a different mode.
type Config struct {
	// Proxy starts every sandbox with the transparent proxy.
	Proxy bool
	// PublicURL is the base clients use to reach this console. Empty means
	// derive that base from the incoming request.
	PublicURL string
	// StateDir enables mounted storage; empty preserves disposable mode.
	StateDir              string
	MaxSandboxes          int
	MaxServicesPerSandbox int
}

// Deployment is the hosting setup the console reports to its UI.
type Deployment struct {
	Proxy                 bool   `json:"proxy"`
	PublicURL             string `json:"publicUrl,omitempty"`
	Durable               bool   `json:"durable"`
	MaxSandboxes          int    `json:"maxSandboxes"`
	MaxServicesPerSandbox int    `json:"maxServicesPerSandbox"`
}

// Configure stores the hosting setup. Call it before serving.
func (m *Manager) Configure(config Config) error {
	publicURL, err := NormalizePublicURL(config.PublicURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.storage != nil || len(m.sessions) > 0 || m.inflight > 0 || m.closing {
		return fmt.Errorf("configure before starting sandboxes")
	}
	if config.MaxSandboxes < 0 || config.MaxSandboxes > 4096 {
		return fmt.Errorf("max sandboxes must be 1 to 4096")
	}
	if config.MaxServicesPerSandbox < 0 || config.MaxServicesPerSandbox > 256 {
		return fmt.Errorf("max services per sandbox must be 1 to 256")
	}
	if config.MaxServicesPerSandbox > 0 {
		m.serviceLimit = config.MaxServicesPerSandbox
	}
	if config.MaxSandboxes > 0 {
		m.capacity = config.MaxSandboxes
	}
	m.proxy = config.Proxy
	m.publicURL = publicURL
	if strings.TrimSpace(config.StateDir) == "" {
		if m.sweepStop == nil {
			m.startSweeper()
		}
		return nil
	}
	if config.Proxy {
		return fmt.Errorf("mounted storage requires direct service URLs; transparent proxy connections are process-scoped")
	}
	if publicURL == "" {
		return fmt.Errorf("mounted storage requires a stable public URL")
	}
	store, err := openStorage(config.StateDir, publicURL)
	if err != nil {
		return err
	}
	m.storage = store
	if err = m.recoverStorage(context.Background()); err != nil {
		for _, session := range m.sessions {
			ctx, cancel := environment.CloseTimeout()
			_ = session.runtime.Close(ctx)
			cancel()
		}
		m.sessions = map[string]*runningSession{}
		m.storage = nil
		return errors.Join(err, store.close())
	}
	if m.sweepStop == nil {
		m.startSweeper()
	}
	return nil
}

func (m *Manager) Deployment() Deployment {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Deployment{Proxy: m.proxy, PublicURL: m.publicURL, Durable: m.storage != nil, MaxSandboxes: m.capacity, MaxServicesPerSandbox: m.serviceLimit}
}

func (m *Manager) proxyEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proxy
}

func (m *Manager) configuredBase() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publicURL
}

// NormalizePublicURL accepts an empty string. A non-empty value must be an
// absolute http or https URL with a host, and without user info, a query, or
// a fragment. A path prefix is kept so a console mounted under one can
// advertise it.
func NormalizePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("public URL must be an absolute http or https URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("public URL must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("public URL must not include user info")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("public URL must not include a query or fragment")
	}
	parsed.Scheme = scheme
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}
