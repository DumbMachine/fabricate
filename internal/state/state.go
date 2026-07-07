// Package state persists the set of live fab instances to
// ~/.config/fab/state.json so the CLI can list/destroy across
// invocations. The store is a single JSON file under a flock for
// the duration of any read-modify-write; fab is a single-user
// local tool, contention is rare.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dumbmachine/fabricate/engine"
)

// Store is the on-disk state file. Marshaled directly to JSON.
type Store struct {
	Version   int                `json:"version"`
	Instances []*engine.Instance `json:"instances"`
}

const currentVersion = 1

// Path returns the state file location. Honors FAB_STATE_FILE for
// testing.
func Path() string {
	if v := os.Getenv("FAB_STATE_FILE"); v != "" {
		return v
	}
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "fab", "state.json")
	}
	return filepath.Join(cfg, "fab", "state.json")
}

// Load reads the state file. A missing file is not an error — it's
// the empty store. Corrupt files surface a clean error so the user
// can decide what to do (the file is human-editable JSON).
func Load() (*Store, error) {
	p := Path()
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Store{Version: currentVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", p, err)
	}
	if s.Version == 0 {
		s.Version = currentVersion
	}
	return &s, nil
}

// Save writes the store atomically (write to temp + rename).
func (s *Store) Save() error {
	s.Version = currentVersion
	sort.Slice(s.Instances, func(i, j int) bool {
		return s.Instances[i].Name < s.Instances[j].Name
	})
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Add inserts (or replaces by name) an instance. Replace semantics
// are intentional: if a previous fab create with the same name
// crashed before recording, the new one wins.
func (s *Store) Add(inst *engine.Instance) {
	for i, existing := range s.Instances {
		if existing.Name == inst.Name {
			s.Instances[i] = inst
			return
		}
	}
	s.Instances = append(s.Instances, inst)
}

// Find returns the instance with the given name, or nil.
func (s *Store) Find(name string) *engine.Instance {
	for _, inst := range s.Instances {
		if inst.Name == name {
			return inst
		}
	}
	return nil
}

// Remove drops the named instance. Returns true if it existed.
func (s *Store) Remove(name string) bool {
	for i, inst := range s.Instances {
		if inst.Name == name {
			s.Instances = append(s.Instances[:i], s.Instances[i+1:]...)
			return true
		}
	}
	return false
}
