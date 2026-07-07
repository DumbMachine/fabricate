// Image pre-pull. testcontainers reads the docker pull stream
// silently (io.ReadAll on the JSON event stream), so without this
// the user sees nothing during a first-boot pull that can easily
// run 30–120s. We front-run testcontainers with a real `docker
// pull` whose stdout we stream straight through to stderr. Once the
// image is in the local cache, testcontainers' pull is a no-op.
package engine

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ImageCached reports whether the image is already present locally.
// Errors fall back to "not cached" — if the docker CLI is broken,
// we want EnsureImage to surface the failure path, not silently
// skip.
func ImageCached(ctx context.Context, image string) bool {
	if image == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

// EnsurePulled is a no-op if image is cached, otherwise it shells
// out to `docker pull`, streaming the canonical progress output to
// the given progress writer. Callers pass os.Stderr in the CLI path.
// Returns the duration the pull took (0 if cached) and any error.
func EnsurePulled(ctx context.Context, image string, progress io.Writer) error {
	if image == "" {
		return fmt.Errorf("empty image name")
	}
	if ImageCached(ctx, image) {
		return nil
	}
	if progress != nil {
		fmt.Fprintf(progress, "fab: pulling image %s...\n", image)
	}
	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	cmd.Stdout = progress
	// `docker pull` writes progress to stderr; route both streams
	// through the same writer so the user sees a single tail.
	cmd.Stderr = progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker pull %s: %w", image, err)
	}
	return nil
}

// ResolveImage returns the image fab will actually run for a given
// engine + profile pair, applying the profile override → engine
// default fallback. Lives here so the cmd layer can compute it
// without importing every engine package.
func ResolveImage(profileImage, engineDefault string) string {
	v := strings.TrimSpace(profileImage)
	if v != "" {
		return v
	}
	return engineDefault
}
