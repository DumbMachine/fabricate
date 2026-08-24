package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/engine/kubernetes"
	"github.com/dumbmachine/fabricate/engine/ssh"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/internal/state"
	"github.com/dumbmachine/fabricate/profile"
	fabtarget "github.com/dumbmachine/fabricate/target"
	"github.com/dumbmachine/fabricate/target/kubetarget"

	"github.com/spf13/cobra"
)

var (
	createProfile     string
	createName        string
	createTimeout     string
	createImage       string
	createTarget      string
	createEndpoint    string
	createKubeContext string
	createKubeconfig  string
	createNamespace   string
)

var createCmd = &cobra.Command{
	Use:   "create <engine>",
	Short: "Provision a new instance from a profile",
	Long: `Create starts an instance for the given engine, seeded per the
named profile, and prints connection credentials. The default target is
local Docker; pass --target kubernetes to create the fixture in the current
or selected kubeconfig context.

The <engine> argument is the profile's catalog directory: a container
engine slug (postgres, mysql, mongodb, redis, prometheus, ssh,
kubernetes, aws_console). HTTP APIs run as foreground environments with
` + "`fab run --environment <file> --proxy -- <command>`" + `. Run ` + "`fab profiles`" + `
to see every (slug, profile) pair.

Examples:
  fab create postgres --profile pagila
  fab create mysql --profile orders-slow
  fab create redis --profile hot-keys
  fab create prometheus --profile api-alerts
  fab create ssh --profile bastion
  fab create aws_console --profile s3-public-audit
  fab create postgres --profile pagila --target kubernetes --namespace fabricate
  fab create postgres --profile finance-ledger -o env > .env`,
	Args: cobra.ExactArgs(1),
	RunE: runCreate,
}

func init() {
	createCmd.Flags().StringVarP(&createProfile, "profile", "p", "", "Profile name (required)")
	createCmd.Flags().StringVarP(&createName, "name", "n", "", "Instance name (default: derived from profile)")
	createCmd.Flags().StringVar(&createTimeout, "timeout", "5m", "Max time to wait for the container to come up (image pull has its own 10m budget)")
	createCmd.Flags().StringVar(&createImage, "image", "", "Override the profile's image (e.g. postgres:15-alpine)")
	createCmd.Flags().StringVar(&createTarget, "target", "", "Provision target: docker or kubernetes (default docker)")
	createCmd.Flags().StringVar(&createEndpoint, "endpoint", "", "Returned endpoint mode: local, localip, or cluster (default local for docker, cluster for kubernetes)")
	createCmd.Flags().StringVar(&createKubeContext, "kube-context", "", "Kubeconfig context for --target kubernetes (default current context)")
	createCmd.Flags().StringVar(&createKubeconfig, "kubeconfig", "", "Kubeconfig path for --target kubernetes (default standard kubeconfig loading)")
	createCmd.Flags().StringVar(&createNamespace, "namespace", "", "Namespace for --target kubernetes (default fabricate)")
	_ = createCmd.MarkFlagRequired("profile")
}

func runCreate(cmd *cobra.Command, args []string) error {
	engineSlug := args[0]
	if createProfile == "" {
		return fmt.Errorf("--profile is required")
	}
	targetOpts, err := fabtarget.Normalize(fabtarget.Options{
		Target:      createTarget,
		Endpoint:    createEndpoint,
		KubeContext: createKubeContext,
		Kubeconfig:  createKubeconfig,
		Namespace:   createNamespace,
	})
	if err != nil {
		return err
	}

	format, err := output.Resolve(outputFlag)
	if err != nil {
		return err
	}

	p, err := profile.LoadForEngine(engineSlug, createProfile)
	if err != nil {
		return err
	}
	if createImage != "" {
		p.Image = createImage
	}

	eng, err := resolveEngine(p.Engine)
	if err != nil {
		return err
	}

	store, err := state.Load()
	if err != nil {
		return err
	}

	name := createName
	if name == "" {
		name = deriveName(p.Name, store)
	}
	if existing := store.Find(name); existing != nil {
		return fmt.Errorf("instance %q already exists (engine=%s, target=%s, id=%s); destroy it first or pass --name",
			name, existing.Engine, existing.ProviderTarget(), output.Short(existing.ArtifactKey()))
	}

	timeout, err := time.ParseDuration(createTimeout)
	if err != nil {
		return fmt.Errorf("invalid --timeout %q: %w", createTimeout, err)
	}

	fmt.Fprintf(os.Stderr, "fab: starting %s instance %q from profile %q on target %s...\n", p.Engine, name, p.Name, targetOpts.Target)

	// Pre-pull the image with full progress output if it isn't
	// cached. testcontainers' built-in pull is silent (no logger
	// surface for the JSON event stream), so without this step a
	// first-time pull looks like a hung create. We do the pull
	// under its OWN context, separate from the create budget, so
	// that a slow pull doesn't eat the wait window.
	image := engine.ResolveImage(p.Image, eng.Info().DefaultImage)
	if targetOpts.Target == fabtarget.Docker && !engine.ImageCached(cmd.Context(), image) {
		// 10-minute ceiling on the pull alone — generous enough for
		// MySQL/Mongo on a slow link, tight enough to fail loudly if
		// the daemon is unreachable.
		pullCtx, pullCancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		if err := engine.EnsurePulled(pullCtx, image, os.Stderr); err != nil {
			pullCancel()
			return fmt.Errorf("create: %w", err)
		}
		pullCancel()
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	if targetOpts.Target == fabtarget.Docker {
		fmt.Fprintf(os.Stderr, "fab: provisioning container (timeout %s)...\n", timeout)
	} else {
		fmt.Fprintf(os.Stderr, "fab: provisioning kubernetes objects (timeout %s)...\n", timeout)
	}
	heartbeatDone := startHeartbeat(ctx, os.Stderr, name, p.Engine)
	start := time.Now()
	var inst *engine.Instance
	if targetOpts.Target == fabtarget.Kubernetes {
		inst, err = kubetarget.Create(ctx, name, p, eng.Info(), targetOpts)
	} else {
		inst, err = eng.Create(ctx, name, p)
		if err == nil {
			err = fabtarget.ApplyDockerEndpoint(inst, targetOpts.Endpoint)
		}
	}
	close(heartbeatDone)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	fmt.Fprintf(os.Stderr, "fab: ready in %s\n", time.Since(start).Round(time.Millisecond))

	// Record under the state lock — parallel `fab create` calls are
	// normal in scripts, and an unlocked read-modify-write drops the
	// slower writer's record (the container then leaks).
	if err := state.Update(func(s *state.Store) error {
		s.Add(inst)
		return nil
	}); err != nil {
		return fmt.Errorf("save state (instance is running, ID=%s): %w", inst.ArtifactKey(), err)
	}

	switch inst.Engine {
	case ssh.Engine:
		output.SetSSHKeyPath(ssh.KeyPath(inst.ArtifactKey()))
	case kubernetes.Engine:
		output.SetKubeconfigPath(kubernetes.KubeconfigPath(inst.ArtifactKey()))
	}
	return output.Instance(os.Stdout, inst, format)
}

// startHeartbeat prints a "still working" line every 15s while the
// engine is provisioning, so a quiet create doesn't look hung. Stops
// when the returned channel is closed. We ignore ctx cancellation
// here on purpose — the caller closes the channel as soon as Create
// returns, error or not, and that's the only stop signal we need.
func startHeartbeat(_ context.Context, w *os.File, name, engineSlug string) chan struct{} {
	stop := make(chan struct{})
	go func() {
		started := time.Now()
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				fmt.Fprintf(w, "fab: still bringing %s %q up (%s elapsed)\n",
					engineSlug, name, time.Since(started).Round(time.Second))
			}
		}
	}()
	return stop
}

func deriveName(profileName string, store *state.Store) string {
	base := strings.ReplaceAll(profileName, "_", "-")
	if store.Find(base) == nil {
		return base
	}
	for i := 2; i < 1000; i++ {
		c := fmt.Sprintf("%s-%d", base, i)
		if store.Find(c) == nil {
			return c
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}
