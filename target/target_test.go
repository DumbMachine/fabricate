package target

import (
	"testing"

	"github.com/dumbmachine/fabricate/engine"
)

func TestNormalizeDefaultsDocker(t *testing.T) {
	opts, err := Normalize(Options{})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if opts.Target != Docker || opts.Endpoint != EndpointLocal {
		t.Fatalf("opts = %+v, want docker/local", opts)
	}
}

func TestNormalizeKubernetesDefaultsClusterEndpoint(t *testing.T) {
	opts, err := Normalize(Options{Target: Kubernetes, KubeContext: "demo"})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if opts.Target != Kubernetes || opts.Endpoint != EndpointCluster || opts.KubeContext != "demo" {
		t.Fatalf("opts = %+v, want kubernetes/cluster", opts)
	}
}

func TestNormalizeRejectsClusterEndpointForDocker(t *testing.T) {
	if _, err := Normalize(Options{Target: Docker, Endpoint: EndpointCluster}); err == nil {
		t.Fatalf("expected docker/cluster to be rejected")
	}
}

func TestApplyDockerEndpointLocalIP(t *testing.T) {
	inst := &engine.Instance{
		Creds: engine.Creds{
			Host: "localhost",
			Port: 15432,
			URL:  "postgresql://fab:secret@localhost:15432/fab?sslmode=disable",
		},
	}
	if err := ApplyDockerEndpoint(inst, EndpointLocalIP); err != nil {
		t.Fatalf("ApplyDockerEndpoint: %v", err)
	}
	if inst.Target != Docker {
		t.Fatalf("target = %q, want docker", inst.Target)
	}
	if inst.Creds.Host != "localip" {
		t.Fatalf("host = %q, want localip", inst.Creds.Host)
	}
	if inst.Creds.URL != "postgresql://fab:secret@localip:15432/fab?sslmode=disable" {
		t.Fatalf("url = %q", inst.Creds.URL)
	}
}
