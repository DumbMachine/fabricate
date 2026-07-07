package state

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dumbmachine/fabricate/engine"
)

// TestConcurrentUpdatesLoseNothing is the regression test for the
// parallel-create race: N concurrent Updates must all land. Before
// Update existed (unlocked Load+Add+Save), the last writer won and
// earlier records vanished — leaking their containers.
func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	t.Setenv("FAB_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- Update(func(s *Store) error {
				s.Add(&engine.Instance{Name: fmt.Sprintf("inst-%02d", i), Engine: "redis"})
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Instances) != n {
		t.Fatalf("got %d instances, want %d — records were lost to a write race", len(s.Instances), n)
	}
}

// TestConcurrentUpdatesAcrossProcesses exercises the flock across real
// process boundaries (goroutine tests share one process; the CLI
// doesn't). Each helper process adds one instance.
func TestConcurrentUpdatesAcrossProcesses(t *testing.T) {
	if os.Getenv("STATE_TEST_HELPER") != "" {
		if err := Update(func(s *Store) error {
			s.Add(&engine.Instance{Name: os.Getenv("STATE_TEST_NAME"), Engine: "redis"})
			return nil
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	stateFile := filepath.Join(t.TempDir(), "state.json")
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run", "TestConcurrentUpdatesAcrossProcesses")
			cmd.Env = append(os.Environ(),
				"STATE_TEST_HELPER=1",
				fmt.Sprintf("STATE_TEST_NAME=proc-%02d", i),
				"FAB_STATE_FILE="+stateFile,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("helper %d: %v\n%s", i, err, out)
			}
		}(i)
	}
	wg.Wait()

	t.Setenv("FAB_STATE_FILE", stateFile)
	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.Instances) != n {
		t.Fatalf("got %d instances, want %d — cross-process records were lost", len(s.Instances), n)
	}
}
