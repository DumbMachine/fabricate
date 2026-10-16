package console

import (
	"fmt"
	"net/url"
	"strings"
)

// Config is how this console process is hosted. Sandboxes started from the
// console follow it. A start request cannot choose a different mode.
type Config struct {
	// Proxy starts every sandbox with the transparent proxy.
	Proxy bool
	// PublicURL is the base clients use to reach this console. Empty means
	// derive that base from the incoming request.
	PublicURL string
}

// Deployment is the hosting setup the console reports to its UI.
type Deployment struct {
	Proxy     bool   `json:"proxy"`
	PublicURL string `json:"publicUrl,omitempty"`
}

// Configure stores the hosting setup. Call it before serving.
func (m *Manager) Configure(config Config) error {
	publicURL, err := NormalizePublicURL(config.PublicURL)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.proxy = config.Proxy
	m.publicURL = publicURL
	m.mu.Unlock()
	return nil
}

func (m *Manager) Deployment() Deployment {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Deployment{Proxy: m.proxy, PublicURL: m.publicURL}
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
