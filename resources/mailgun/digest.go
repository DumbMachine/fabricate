package mailgun

import (
	"crypto/sha256"
	"encoding/hex"
)

func scenarioDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
