// Package engine defines the lifecycle interface every database
// backend (postgres, mysql, mongo, ...) implements. Engines turn a
// loaded Profile + a desired instance Name into a running container
// and a set of Creds.
package engine

import (
	"context"
	"os"

	"github.com/dumbmachine/fabricate/profile"
)

// Disable testcontainers' Ryuk reaper. Ryuk's job is to kill
// containers when the spawning test process exits — exactly the
// wrong behavior for fab, where the CLI is short-lived but the
// container must outlive it. The env var is consulted by
// testcontainers-go during its lazy provider init, so setting it
// here (in the engine package's init, which runs before any
// engine.Create call thanks to import order) wins the race.
// Honors a pre-set value so users can opt back in for debugging.
func init() {
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
}

// Creds is everything a caller needs to actually talk to the
// instance fab just started. URL is the canonical connection string
// for the engine (postgres://, mysql://, mongodb://, redis://,
// http://, ssh://, https:// for k8s API server). Database is
// engine-specific and may be empty (Redis uses the numeric DB index,
// Prometheus has none, SSH has none). PrivateKey is populated by the
// ssh engine — it's the PEM bytes the caller needs to authenticate;
// we ship it inline rather than as a path so `creds -o env` can hand
// it to the next process without a side-channel file.
//
// Kubeconfig is populated by the kubernetes engine: the full YAML
// (server URL + CA cert + admin cred) that lets kubectl or client-go
// talk to the cluster. Shipped inline for the same reason as
// PrivateKey.
type Creds struct {
	Engine     string            `json:"engine"`
	Host       string            `json:"host"`
	Port       int               `json:"port"`
	Username   string            `json:"username,omitempty"`
	Password   string            `json:"password,omitempty"`
	Database   string            `json:"database,omitempty"`
	URL        string            `json:"url"`
	PrivateKey string            `json:"private_key,omitempty"`
	Kubeconfig string            `json:"kubeconfig,omitempty"`
	Extra      map[string]string `json:"extra,omitempty"`
}

// Instance is fab's record of a live fixture. Docker-backed fixtures
// keep using ContainerID as their lifecycle handle. Other targets store
// their own provider metadata in the target-specific fields below.
type Instance struct {
	Name        string           `json:"name"`
	Profile     string           `json:"profile"`
	Engine      string           `json:"engine"`
	Image       string           `json:"image"`
	ContainerID string           `json:"container_id"`
	Target      string           `json:"target,omitempty"`
	ArtifactID  string           `json:"artifact_id,omitempty"`
	Kubernetes  *KubernetesState `json:"kubernetes,omitempty"`
	Creds       Creds            `json:"creds"`
	CreatedAt   string           `json:"created_at"`
}

// ProviderTarget returns the lifecycle provider for an instance.
// Empty means "docker" for state-file backward compatibility.
func (i *Instance) ProviderTarget() string {
	if i == nil || i.Target == "" {
		return "docker"
	}
	return i.Target
}

// ArtifactKey is the stable local filename stem for engine artifacts
// such as SSH private keys and kubeconfig files. Docker instances used
// ContainerID for this historically; non-Docker targets set ArtifactID.
func (i *Instance) ArtifactKey() string {
	if i == nil {
		return ""
	}
	if i.ArtifactID != "" {
		return i.ArtifactID
	}
	return i.ContainerID
}

// KubernetesState is the lifecycle metadata needed to delete objects
// fab created in a Kubernetes target cluster.
type KubernetesState struct {
	Context        string                `json:"context,omitempty"`
	KubeconfigPath string                `json:"kubeconfig_path,omitempty"`
	Namespace      string                `json:"namespace,omitempty"`
	EndpointMode   string                `json:"endpoint_mode,omitempty"`
	Labels         map[string]string     `json:"labels,omitempty"`
	Objects        []KubernetesObjectRef `json:"objects,omitempty"`
}

// KubernetesObjectRef records an object fab created. Destroy primarily
// deletes by labels so it can clean up across implementation changes;
// refs are kept for humans and future precise deletion.
type KubernetesObjectRef struct {
	APIVersion string `json:"api_version,omitempty"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

// Engine is what each database backend implements. Create is the
// only side-effecting method; Destroy uses the Docker ID stored on
// the Instance, so any process with Docker access can tear down an
// instance another process started.
type Engine interface {
	// Info returns engine metadata: default image, port, supported
	// seed types. Agents call this (via `fab engines`) to learn how
	// to author a profile without reading the source.
	Info() Info

	// Create starts a container seeded per the profile and returns
	// an Instance ready for use. The container is NOT auto-reaped —
	// fab's state file is the source of truth for cleanup.
	Create(ctx context.Context, name string, p *profile.Profile) (*Instance, error)

	// Destroy stops + removes the container by Docker ID.
	Destroy(ctx context.Context, containerID string) error
}

// Info is the static, agent-discoverable metadata for one engine.
// Lives next to the Engine interface so each implementation owns
// its own catalog entry instead of a parallel registry drifting
// behind the code.
type Info struct {
	// Slug is what a profile's `engine:` field must equal.
	Slug string `json:"slug"`

	// DefaultImage is the image fab uses when a profile omits `image:`.
	DefaultImage string `json:"default_image"`

	// DefaultPort is the container-side port fab maps to a random
	// host port (0 for engines like ssh where the port is fixed at
	// the protocol layer).
	DefaultPort int `json:"default_port"`

	// SupportedSeeds is the set of `seed[].type` values this engine
	// will accept. Anything else is rejected at create time with a
	// clear error — typos never silently no-op.
	SupportedSeeds []string `json:"supported_seeds"`

	// Description is one short sentence: what this engine spins up
	// and what credentials it returns.
	Description string `json:"description"`
}
