// Command docsexamples captures committed docs snapshots for resource pages:
// command output, compiled operation and scenario catalogs, and compatibility
// reports.
// It is a contributor tool, not a user CLI.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dumbmachine/fabricate/internal/docsexamples"
	"github.com/dumbmachine/fabricate/resources/all"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "capture":
		os.Exit(capture())
	case "install-compat":
		os.Exit(installCompat())
	case "operations":
		os.Exit(operations())
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: docsexamples capture --fab <binary> [flags]")
	fmt.Fprintln(os.Stderr, "       docsexamples install-compat --from <report> --resource <id>")
	fmt.Fprintln(os.Stderr, "       docsexamples operations [--resource <id>]")
}

func capture() int {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repo := fs.String("repo", ".", "repository root")
	fab := fs.String("fab", "", "fab binary (required)")
	all := fs.Bool("all", false, "recapture even when provenance matches")
	sinceRef := fs.String("since-ref", "", "git ref; force examples whose inputs or snapshots changed since this ref")
	var ids []string
	var resources []string
	fs.Func("id", "example id to consider; repeat as needed", func(value string) error {
		ids = append(ids, value)
		return nil
	})
	fs.Func("resource", "only examples whose roots include this resource (gmail or resources/gmail); repeat as needed", func(value string) error {
		resources = append(resources, value)
		return nil
	})
	if err := fs.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if *fab == "" {
		fmt.Fprintln(os.Stderr, "docsexamples: --fab is required")
		return 2
	}

	opts := docsexamples.Options{
		IDs:       ids,
		Resources: resources,
		All:       *all,
		SinceRef:  *sinceRef,
	}
	results, err := docsexamples.Capture(*repo, func(environment string, proxy bool, argv []string) ([]byte, error) {
		return docsexamples.RunFab(*fab, environment, proxy, argv)
	}, opts, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docsexamples: %v\n", err)
		return 1
	}
	captured := 0
	for _, result := range results {
		if result.Action == "capture" {
			captured++
		}
	}
	fmt.Fprintf(os.Stderr, "docs-examples: %d captured, %d skipped\n", captured, len(results)-captured)
	return 0
}

func installCompat() int {
	fs := flag.NewFlagSet("install-compat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repo := fs.String("repo", ".", "repository root")
	from := fs.String("from", "", "compatibility report to install (required)")
	resource := fs.String("resource", "", "resource id (required)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if *from == "" || *resource == "" {
		fmt.Fprintln(os.Stderr, "docsexamples: install-compat requires --from and --resource")
		return 2
	}
	if err := docsexamples.InstallCompatibility(*repo, *from, *resource, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "docsexamples: %v\n", err)
		return 1
	}
	return 0
}

func operations() int {
	fs := flag.NewFlagSet("operations", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repo := fs.String("repo", ".", "repository root")
	var resources []string
	fs.Func("resource", "resource id to dump (gmail or resources/gmail); repeat as needed", func(value string) error {
		resources = append(resources, value)
		return nil
	})
	if err := fs.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if err := docsexamples.WriteOperations(*repo, all.Registry(), resources, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}
