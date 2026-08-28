package cli

import "testing"

func TestRootExposesOnlyAPISandboxCommands(t *testing.T) {
	commands := make(map[string]bool)
	for _, command := range rootCmd.Commands() {
		commands[command.Name()] = true
	}
	for _, name := range []string{"run", "logs", "environment", "service", "resource", "scenario"} {
		if !commands[name] {
			t.Errorf("public command %q is missing", name)
		}
	}
	for _, name := range []string{"create", "destroy", "creds", "wait", "engines", "profiles", "ls"} {
		if commands[name] {
			t.Errorf("legacy infrastructure command %q is still public", name)
		}
	}
}
