package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os/exec"
	"strings"

	xssh "golang.org/x/crypto/ssh"
)

// generateKeypair returns the OpenSSH-formatted authorized_keys line
// (one trailing newline) and the PEM-encoded private key. ed25519
// keys are short, fast to generate, and supported by every modern
// sshd including the linuxserver image's.
func generateKeypair() (publicAuthorizedLine, privatePEM string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	sshPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("wrap ed25519 pub: %w", err)
	}

	// MarshalAuthorizedKey already appends a newline.
	pubLine := string(xssh.MarshalAuthorizedKey(sshPub))

	// MarshalPrivateKey lives in x/crypto/ssh and produces an
	// unencrypted OpenSSH private-key PEM — what ssh-keygen would
	// have written for a passphrase-less ed25519 key. That's the
	// format `ssh -i` expects on every platform.
	block, err := xssh.MarshalPrivateKey(priv, "fab")
	if err != nil {
		return "", "", fmt.Errorf("marshal ed25519 priv: %w", err)
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, block); err != nil {
		return "", "", err
	}
	return pubLine, buf.String(), nil
}

// dockerExec runs a command inside the named container and returns
// combined stdout+stderr. Used for SSH seed scripts so we don't
// need to round-trip through a real SSH connection just to set the
// container up.
func dockerExec(ctx context.Context, containerID string, args ...string) (string, error) {
	all := append([]string{"exec", containerID}, args...)
	out, err := exec.CommandContext(ctx, "docker", all...).CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}
