package environment

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const APIVersion = "fabricate.dev/v1alpha1"

type Spec struct {
	APIVersion string                 `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                 `yaml:"kind" json:"kind"`
	Metadata   Metadata               `yaml:"metadata" json:"metadata"`
	Services   map[string]ServiceSpec `yaml:"services" json:"services"`
	Proxy      ProxySpec              `yaml:"proxy,omitempty" json:"proxy,omitempty"`
}

type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

type ServiceSpec struct {
	Resource string `yaml:"resource" json:"resource"`
	Scenario string `yaml:"scenario" json:"scenario"`
}

type ProxySpec struct {
	Hosts        map[string]string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
	Passthrough  []string          `yaml:"passthrough,omitempty" json:"passthrough,omitempty"`
	UnknownHosts string            `yaml:"unknown_hosts,omitempty" json:"unknown_hosts,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Load(path string) (Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("environment: read %s: %w", path, err)
	}
	return Parse(raw)
}

func Parse(raw []byte) (Spec, error) {
	var spec Spec
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("environment: decode: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Spec{}, fmt.Errorf("environment: multiple YAML documents are not allowed")
		}
		return Spec{}, fmt.Errorf("environment: trailing data: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func (s Spec) Validate() error {
	if s.APIVersion != APIVersion {
		return fmt.Errorf("environment: apiVersion must be %q", APIVersion)
	}
	if s.Kind != "Environment" {
		return fmt.Errorf("environment: kind must be %q", "Environment")
	}
	if !namePattern.MatchString(s.Metadata.Name) {
		return fmt.Errorf("environment: metadata.name must match %s", namePattern)
	}
	if len(s.Services) == 0 {
		return fmt.Errorf("environment: at least one service is required")
	}
	for name, service := range s.Services {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("environment: service name %q must match %s", name, namePattern)
		}
		if strings.TrimSpace(service.Resource) == "" || strings.TrimSpace(service.Scenario) == "" {
			return fmt.Errorf("environment: service %q requires resource and scenario", name)
		}
	}
	for route, service := range s.Proxy.Hosts {
		if strings.TrimSpace(route) == "" {
			return fmt.Errorf("environment: proxy host route cannot be empty")
		}
		if _, ok := s.Services[service]; !ok {
			return fmt.Errorf("environment: proxy route %q names unknown service %q", route, service)
		}
	}
	for _, host := range s.Proxy.Passthrough {
		if strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/:") {
			return fmt.Errorf("environment: proxy passthrough host %q must be a bare hostname", host)
		}
	}
	if policy := strings.TrimSpace(s.Proxy.UnknownHosts); policy != "" && policy != "reject" && policy != "passthrough" {
		return fmt.Errorf("environment: proxy unknown_hosts must be \"reject\" or \"passthrough\"")
	}
	return nil
}

func (s ProxySpec) RejectUnknownHosts() bool {
	return strings.TrimSpace(s.UnknownHosts) == "reject"
}
