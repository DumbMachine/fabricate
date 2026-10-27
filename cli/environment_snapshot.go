package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/environments"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
)

var snapshotState, snapshotOutput string
var environmentSnapshotCmd = &cobra.Command{Use: "snapshot <environment-or-manifest>", Short: "Export current provider state as validated immutable scenario files", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	spec, err := environments.Resolve(args[0])
	if err != nil {
		return err
	}
	if snapshotState == "" || snapshotOutput == "" {
		return fmt.Errorf("state-dir and output are required")
	}
	if err = os.MkdirAll(snapshotOutput, 0700); err != nil {
		return err
	}
	registry := all.Registry()
	for name, service := range spec.Services {
		resource, ok := registry.Get(service.Resource)
		if !ok {
			return fmt.Errorf("unknown resource")
		}
		baseline, err := environment.ResolveScenario(resource, service)
		if err != nil {
			return err
		}
		db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(snapshotState, "services", name, "live.db"))+"?mode=ro")
		if err != nil {
			return err
		}
		doc, err := resource.Scenarios().Dump(context.Background(), db, baseline.Metadata())
		db.Close()
		if err != nil {
			return err
		}
		if err = resource.Scenarios().Validate(context.Background(), doc); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		file := name + ".json"
		if err = os.WriteFile(filepath.Join(snapshotOutput, file), raw, 0600); err != nil {
			return err
		}
		service.ScenarioFile = file
		spec.Services[name] = service
	}
	raw, err := yaml.Marshal(spec)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(snapshotOutput, "environment.yaml"), raw, 0600); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Exported %d provider states to %s\n", len(spec.Services), snapshotOutput)
	return nil
}}

func init() {
	environmentSnapshotCmd.Flags().StringVar(&snapshotState, "state-dir", "", "Running environment state directory")
	environmentSnapshotCmd.Flags().StringVar(&snapshotOutput, "output", "", "New snapshot directory")
	environmentCmd.AddCommand(environmentSnapshotCmd)
}
