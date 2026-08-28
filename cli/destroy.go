// LEGACY INFRASTRUCTURE CODE.
//
// This file belongs to the retired container-profile CLI. It is not part of
// the current API-sandbox product. Do not change or extend it. A refactored
// infrastructure workflow may return in a future version.
package cli

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/dumbmachine/fabricate/internal/state"
	fabtarget "github.com/dumbmachine/fabricate/target"
	"github.com/dumbmachine/fabricate/target/kubetarget"

	"github.com/spf13/cobra"
)

var destroyAll bool

var destroyCmd = &cobra.Command{
	Use:   "destroy [name...]",
	Short: "Stop and remove instances",
	Long: `Destroy stops the container(s) and removes the entry from fab's state.

  fab destroy web              # one instance
  fab destroy web other        # several
  fab destroy --all            # everything`,
	RunE: runDestroy,
}

func init() {
	destroyCmd.Flags().BoolVar(&destroyAll, "all", false, "Destroy every live instance")
}

func runDestroy(cmd *cobra.Command, args []string) error {
	if !destroyAll && len(args) == 0 {
		return fmt.Errorf("pass at least one name, or --all")
	}
	s, err := state.Load()
	if err != nil {
		return err
	}

	targets := args
	if destroyAll {
		targets = nil
		for _, inst := range s.Instances {
			targets = append(targets, inst.Name)
		}
	}

	ctx := cmd.Context()
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	for _, name := range targets {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			err := destroyOne(ctx, s, name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fmt.Fprintf(os.Stderr, "fab: destroy %s: %v\n", name, err)
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			fmt.Fprintf(os.Stderr, "fab: destroyed %s\n", name)
		}(name)
	}
	wg.Wait()
	return firstErr
}

// destroyOne tears down one instance and drops its record via
// state.Update — per-name, under the state lock, so parallel destroys
// (and concurrent creates in other processes) never clobber each
// other's records with a stale snapshot.
func destroyOne(ctx context.Context, s *state.Store, name string) error {
	inst := s.Find(name)
	if inst == nil {
		return fmt.Errorf("not found")
	}
	removeRecord := func() error {
		return state.Update(func(st *state.Store) error {
			st.Remove(name)
			return nil
		})
	}
	eng, err := resolveEngine(inst.Engine)
	if err != nil && inst.ProviderTarget() == fabtarget.Docker {
		if rmErr := removeRecord(); rmErr != nil {
			return rmErr
		}
		return fmt.Errorf("%w (state entry removed; container %s may need manual `docker rm -f`)", err, inst.ContainerID)
	}
	switch inst.ProviderTarget() {
	case fabtarget.Kubernetes:
		if err := kubetarget.Destroy(ctx, inst); err != nil {
			return err
		}
	case fabtarget.Docker:
		if err := eng.Destroy(ctx, inst.ContainerID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("target %q is not supported", inst.ProviderTarget())
	}
	return removeRecord()
}
