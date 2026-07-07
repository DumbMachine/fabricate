//go:build !unix

package state

// lockFile on platforms without flock degrades to no locking — the
// pre-Update behavior. Single sequential use stays correct; parallel
// creates can race, which unix avoids via lock_unix.go.
func lockFile(string) (func(), error) { return func() {}, nil }
