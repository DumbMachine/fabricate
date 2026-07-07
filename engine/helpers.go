// Shared helpers used by every engine. Lives here (not in each
// engine package) because the same logic — duration parsing,
// password generation, default fallback — would otherwise repeat
// across six packages and drift.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// OrDefault returns v if it has non-whitespace content, fallback otherwise.
func OrDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// ParseTimeout converts a Go duration string into a Duration,
// falling back if blank, invalid, or non-positive. Used for the
// profile's optional Healthcheck.Timeout knob.
func ParseTimeout(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// GeneratePassword returns a URL-safe random password. base64 of 18
// bytes is 24 chars — enough entropy for an ephemeral container, no
// shell-special characters, no padding to deal with in connection
// URLs.
func GeneratePassword() string {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		panic("fab: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// DockerRM is the universal Destroy implementation. testcontainers
// gives us a container ID at Create time; tearing it down is a
// plain `docker rm -f`. Engines call this from Destroy so we don't
// re-import the testcontainers Provider (which would re-spawn Ryuk
// and other lifecycle machinery) just to remove a known container.
//
// A missing container is treated as already-removed (no error). This
// matters because the state file can outlive a container that was
// pruned out-of-band (manual docker rm, daemon restart with no
// persistent volume, OS reboot on macOS). Returning an error there
// would strand the state entry — `fab destroy` would loop forever
// on a container that no longer exists.
func DockerRM(ctx context.Context, containerID string) error {
	if containerID == "" {
		return fmt.Errorf("empty container id")
	}
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", containerID).CombinedOutput()
	if err != nil {
		if isNoSuchContainer(out) {
			return nil
		}
		return fmt.Errorf("docker rm -f %s: %w: %s", containerID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// isNoSuchContainer recognizes docker's "container doesn't exist"
// error in either the old or new daemon phrasing.
func isNoSuchContainer(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "no such container") ||
		strings.Contains(s, "is not running") && strings.Contains(s, "no such")
}
