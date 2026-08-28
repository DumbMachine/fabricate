// LEGACY INFRASTRUCTURE CODE.
//
// This file belongs to the retired container-profile CLI. It is not part of
// the current API-sandbox product. Do not change or extend it. A refactored
// infrastructure workflow may return in a future version.
package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/engine/awsconsole"
	"github.com/dumbmachine/fabricate/engine/kubernetes"
	"github.com/dumbmachine/fabricate/engine/mongodb"
	"github.com/dumbmachine/fabricate/engine/mysql"
	"github.com/dumbmachine/fabricate/engine/postgres"
	"github.com/dumbmachine/fabricate/engine/prometheus"
	"github.com/dumbmachine/fabricate/engine/redis"
	"github.com/dumbmachine/fabricate/engine/ssh"
	"github.com/dumbmachine/fabricate/internal/output"

	"github.com/spf13/cobra"
)

// engines is the static engine registry. Each entry maps a profile
// `engine:` slug to a concrete implementation. Keep this aligned
// with the embedded profile directory layout in profiles/.
var engines = map[string]engine.Engine{
	postgres.Engine:   postgres.New(),
	mysql.Engine:      mysql.New(),
	mongodb.Engine:    mongodb.New(),
	redis.Engine:      redis.New(),
	prometheus.Engine: prometheus.New(),
	ssh.Engine:        ssh.New(),
	kubernetes.Engine: kubernetes.New(),
	awsconsole.Engine: awsconsole.New(),
}

// resolveEngine returns the engine for the named slug or a clear
// error. We don't fall back to a default — the profile carries the
// engine slug, and a missing engine is a real misconfiguration.
func resolveEngine(name string) (engine.Engine, error) {
	e, ok := engines[name]
	if !ok {
		return nil, fmt.Errorf("engine %q not supported in this build (have: %s)", name, supportedEngines())
	}
	return e, nil
}

// supportedEngines lists the registered engine slugs, sorted. Used
// in error messages so users see "have: mongodb, mysql, postgres,
// prometheus, redis, ssh" instead of a Go map iteration order.
func supportedEngines() string {
	names := make([]string, 0, len(engines))
	for name := range engines {
		names = append(names, name)
	}
	sort.Strings(names)
	return joinComma(names)
}

func joinComma(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	out := xs[0]
	for _, x := range xs[1:] {
		out += ", " + x
	}
	return out
}

// engineInfos returns Info for every registered engine, sorted by
// slug. The CLI uses this for `fab engines` (agent discovery) and
// the docs/help layer.
func engineInfos() []engine.Info {
	infos := make([]engine.Info, 0, len(engines))
	for _, e := range engines {
		infos = append(infos, e.Info())
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Slug < infos[j].Slug })
	return infos
}

var enginesCmd = &cobra.Command{
	Use:   "engines",
	Short: "List engines and the seed types each one accepts",
	Long: `Engines prints the full registry: every engine slug, its default
image, its default in-container port, the set of seed types it
understands, and a one-line description. Agents authoring a new
profile call this to learn what to put in profile.yaml.

  fab engines              # human-readable table
  fab engines -o json      # machine-readable, agent-friendly`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := output.Resolve(outputFlag)
		if err != nil {
			return err
		}
		return output.Engines(os.Stdout, engineInfos(), f)
	},
}
