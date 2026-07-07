package target

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/dumbmachine/fabricate/engine"
)

const (
	Docker     = "docker"
	Kubernetes = "kubernetes"

	EndpointLocal   = "local"
	EndpointLocalIP = "localip"
	EndpointCluster = "cluster"
)

// Options describes where fab should create a fixture and which endpoint
// shape callers should receive.
type Options struct {
	Target      string
	Endpoint    string
	KubeContext string
	Kubeconfig  string
	Namespace   string
}

// Normalize fills defaults and rejects target/endpoint combinations that
// would create unreachable resource configs.
func Normalize(opts Options) (Options, error) {
	opts.Target = strings.ToLower(strings.TrimSpace(opts.Target))
	opts.Endpoint = strings.ToLower(strings.TrimSpace(opts.Endpoint))
	opts.KubeContext = strings.TrimSpace(opts.KubeContext)
	opts.Kubeconfig = strings.TrimSpace(opts.Kubeconfig)
	opts.Namespace = strings.TrimSpace(opts.Namespace)

	if opts.Target == "" {
		opts.Target = Docker
	}
	switch opts.Target {
	case Docker:
		if opts.Endpoint == "" {
			opts.Endpoint = EndpointLocal
		}
		if opts.Endpoint != EndpointLocal && opts.Endpoint != EndpointLocalIP {
			return Options{}, fmt.Errorf("target %q supports --endpoint %q or %q, got %q", Docker, EndpointLocal, EndpointLocalIP, opts.Endpoint)
		}
		if opts.KubeContext != "" || opts.Kubeconfig != "" || opts.Namespace != "" {
			return Options{}, fmt.Errorf("kubernetes options require --target %s", Kubernetes)
		}
	case Kubernetes:
		if opts.Endpoint == "" {
			opts.Endpoint = EndpointCluster
		}
		if opts.Endpoint != EndpointCluster {
			return Options{}, fmt.Errorf("target %q supports --endpoint %q, got %q", Kubernetes, EndpointCluster, opts.Endpoint)
		}
	default:
		return Options{}, fmt.Errorf("unknown fab target %q (want %s or %s)", opts.Target, Docker, Kubernetes)
	}
	return opts, nil
}

// ApplyDockerEndpoint mutates Docker-returned creds to the requested
// caller-facing host. localip is useful when the consumer of the creds runs
// in a cluster that can resolve localip back to the developer machine.
func ApplyDockerEndpoint(inst *engine.Instance, endpoint string) error {
	if inst == nil {
		return nil
	}
	inst.Target = Docker
	switch endpoint {
	case "", EndpointLocal:
		return nil
	case EndpointLocalIP:
		rewriteHost(inst, EndpointLocalIP)
		return nil
	default:
		return fmt.Errorf("docker endpoint %q is not supported", endpoint)
	}
}

func rewriteHost(inst *engine.Instance, host string) {
	inst.Creds.Host = host
	if inst.Creds.Port <= 0 || inst.Creds.URL == "" {
		return
	}
	u, err := url.Parse(inst.Creds.URL)
	if err != nil || u.Scheme == "" {
		return
	}
	u.Host = net.JoinHostPort(host, fmt.Sprintf("%d", inst.Creds.Port))
	inst.Creds.URL = u.String()
}
