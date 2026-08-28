package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/dumbmachine/fabricate/environment"
	"github.com/dumbmachine/fabricate/environments"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/resources/all"
	"github.com/dumbmachine/fabricate/scenario"
	"github.com/spf13/cobra"
)

var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"environments", "env"},
	Short:   "Inspect and validate environment definitions",
}

var environmentListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List official environments",
	Args:    cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		views, err := officialEnvironmentViews()
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeEnvironmentList(os.Stdout, views, format)
	},
}

var environmentInspectCmd = &cobra.Command{
	Use:   "inspect <environment-or-manifest>",
	Short: "Show one environment and its services",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		spec, err := loadEnvironmentDefinition(args[0])
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeEnvironmentInspect(os.Stdout, spec, format)
	},
}

var environmentValidateCmd = &cobra.Command{
	Use:   "validate <environment-or-manifest>",
	Short: "Validate an environment and every resource/scenario reference",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		spec, err := loadEnvironmentDefinition(args[0])
		if err != nil {
			return err
		}
		if err := validateEnvironmentDefinition(spec, all.Registry()); err != nil {
			return err
		}
		return writeValidation(os.Stdout, "environment", spec.Metadata.Name)
	},
}

var resourceCmd = &cobra.Command{
	Use:     "resource",
	Aliases: []string{"resources"},
	Short:   "Inspect supported provider API resources",
}

var resourceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List supported resources",
	Args:    cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		registry := all.Registry()
		views, err := resourceViews(registry)
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeResourceList(os.Stdout, views, format)
	},
}

var resourceInspectCmd = &cobra.Command{
	Use:   "inspect <resource>",
	Short: "Show a resource's provider hosts, contract, SDK, and scenarios",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		view, err := resourceViewByID(all.Registry(), args[0])
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeResourceInspect(os.Stdout, view, format)
	},
}

var scenarioCmd = &cobra.Command{
	Use:     "scenario",
	Aliases: []string{"scenarios"},
	Short:   "Inspect and validate resource scenarios",
}

var scenarioListCmd = &cobra.Command{
	Use:     "list [resource]",
	Aliases: []string{"ls"},
	Short:   "List scenarios, optionally for one resource",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		resourceID := ""
		if len(args) == 1 {
			resourceID = args[0]
		}
		views, err := scenarioViews(all.Registry(), resourceID)
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeScenarioList(os.Stdout, views, format)
	},
}

var scenarioInspectCmd = &cobra.Command{
	Use:   "inspect <scenario-or-path>",
	Short: "Show one built-in or local scenario document",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		doc, _, err := loadScenarioDefinition(args[0], all.Registry())
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeScenarioInspect(os.Stdout, doc, format)
	},
}

var scenarioValidateCmd = &cobra.Command{
	Use:   "validate <scenario-or-path>",
	Short: "Validate a scenario envelope and resource-specific state",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		doc, resource, err := loadScenarioDefinition(args[0], all.Registry())
		if err != nil {
			return err
		}
		if err := resource.Scenarios().Validate(context.Background(), doc); err != nil {
			return fmt.Errorf("scenario %q: %w", doc.ID, err)
		}
		return writeValidation(os.Stdout, "scenario", doc.ID)
	},
}

var serviceCmd = &cobra.Command{
	Use:     "service",
	Aliases: []string{"services"},
	Short:   "Inspect services defined by an environment",
}

var serviceEnvironment string

var serviceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List services in an environment",
	Args:    cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		spec, err := loadEnvironmentDefinition(serviceEnvironment)
		if err != nil {
			return err
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeServiceList(os.Stdout, serviceViews(spec), format)
	},
}

var serviceInspectCmd = &cobra.Command{
	Use:   "inspect <service>",
	Short: "Show one service in an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		spec, err := loadEnvironmentDefinition(serviceEnvironment)
		if err != nil {
			return err
		}
		serviceSpec, ok := spec.Services[args[0]]
		if !ok {
			return fmt.Errorf("environment %q has no service %q; choose one of: %s", spec.Metadata.Name, args[0], strings.Join(serviceNames(spec), ", "))
		}
		format, err := entityFormat()
		if err != nil {
			return err
		}
		return writeServiceInspect(os.Stdout, serviceView{Name: args[0], Resource: serviceSpec.Resource, Scenario: serviceSpec.Scenario}, format)
	},
}

func init() {
	environmentCmd.AddCommand(environmentListCmd, environmentInspectCmd, environmentValidateCmd)
	resourceCmd.AddCommand(resourceListCmd, resourceInspectCmd)
	scenarioCmd.AddCommand(scenarioListCmd, scenarioInspectCmd, scenarioValidateCmd)
	serviceListCmd.Flags().StringVar(&serviceEnvironment, "environment", "", "Environment name or manifest (required)")
	serviceInspectCmd.Flags().StringVar(&serviceEnvironment, "environment", "", "Environment name or manifest (required)")
	_ = serviceListCmd.MarkFlagRequired("environment")
	_ = serviceInspectCmd.MarkFlagRequired("environment")
	serviceCmd.AddCommand(serviceListCmd, serviceInspectCmd)
	rootCmd.AddCommand(environmentCmd, resourceCmd, scenarioCmd, serviceCmd)
}

type environmentView struct {
	Name     string        `json:"name"`
	Services []serviceView `json:"services"`
}

type environmentInspectView struct {
	APIVersion string                             `json:"apiVersion"`
	Kind       string                             `json:"kind"`
	Metadata   environment.Metadata               `json:"metadata"`
	Services   map[string]environment.ServiceSpec `json:"services"`
	Proxy      *environment.ProxySpec             `json:"proxy,omitempty"`
}

type serviceView struct {
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Scenario string `json:"scenario"`
}

type resourceView struct {
	Resource  httpresource.Descriptor `json:"resource"`
	Scenarios []string                `json:"scenarios"`
}

type scenarioView struct {
	ID              string `json:"id"`
	Resource        string `json:"resource"`
	ResourceVersion string `json:"resource_version"`
}

func entityFormat() (output.Format, error) {
	format, err := output.Resolve(outputFlag)
	if err != nil {
		return "", err
	}
	if format != output.FormatJSON && format != output.FormatTable {
		return "", fmt.Errorf("output format %q is not supported here (want json or table)", format)
	}
	return format, nil
}

func loadEnvironmentDefinition(target string) (environment.Spec, error) {
	if target == "" {
		return environment.Spec{}, fmt.Errorf("environment is required")
	}
	if isManifestTarget(target) {
		return environment.Load(target)
	}
	names, err := environments.Names()
	if err != nil {
		return environment.Spec{}, err
	}
	for _, name := range names {
		if target == name {
			return environments.Load(target)
		}
	}
	return environment.Spec{}, fmt.Errorf("unknown environment %q; choose one of: %s", target, strings.Join(names, ", "))
}

func validateEnvironmentDefinition(spec environment.Spec, registry *httpresource.Registry) error {
	for _, name := range serviceNames(spec) {
		serviceSpec := spec.Services[name]
		resource, ok := registry.Get(serviceSpec.Resource)
		if !ok {
			return fmt.Errorf("environment %q service %q: unknown resource %q", spec.Metadata.Name, name, serviceSpec.Resource)
		}
		doc, err := resource.Scenario(serviceSpec.Scenario)
		if err != nil {
			return fmt.Errorf("environment %q service %q: %w", spec.Metadata.Name, name, err)
		}
		if err := resource.Scenarios().Validate(context.Background(), doc); err != nil {
			return fmt.Errorf("environment %q service %q scenario %q: %w", spec.Metadata.Name, name, doc.ID, err)
		}
	}
	return nil
}

func officialEnvironmentViews() ([]environmentView, error) {
	names, err := environments.Names()
	if err != nil {
		return nil, err
	}
	views := make([]environmentView, 0, len(names))
	for _, name := range names {
		spec, err := environments.Load(name)
		if err != nil {
			return nil, err
		}
		views = append(views, environmentView{Name: name, Services: serviceViews(spec)})
	}
	return views, nil
}

func serviceNames(spec environment.Spec) []string {
	names := make([]string, 0, len(spec.Services))
	for name := range spec.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func serviceViews(spec environment.Spec) []serviceView {
	views := make([]serviceView, 0, len(spec.Services))
	for _, name := range serviceNames(spec) {
		serviceSpec := spec.Services[name]
		views = append(views, serviceView{Name: name, Resource: serviceSpec.Resource, Scenario: serviceSpec.Scenario})
	}
	return views
}

func resourceViews(registry *httpresource.Registry) ([]resourceView, error) {
	views := make([]resourceView, 0, len(registry.Descriptors()))
	for _, descriptor := range registry.Descriptors() {
		view, err := resourceViewByID(registry, descriptor.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func resourceViewByID(registry *httpresource.Registry, id string) (resourceView, error) {
	resource, ok := registry.Get(id)
	if !ok {
		descriptors := registry.Descriptors()
		ids := make([]string, len(descriptors))
		for i, descriptor := range descriptors {
			ids[i] = descriptor.ID
		}
		return resourceView{}, fmt.Errorf("unknown resource %q; choose one of: %s", id, strings.Join(ids, ", "))
	}
	ids, err := resource.ScenarioIDs()
	if err != nil {
		return resourceView{}, fmt.Errorf("resource %q: list scenarios: %w", id, err)
	}
	return resourceView{Resource: resource.Descriptor(), Scenarios: ids}, nil
}

func scenarioViews(registry *httpresource.Registry, resourceID string) ([]scenarioView, error) {
	descriptors := registry.Descriptors()
	if resourceID != "" {
		view, err := resourceViewByID(registry, resourceID)
		if err != nil {
			return nil, err
		}
		descriptors = []httpresource.Descriptor{view.Resource}
	}
	var views []scenarioView
	for _, descriptor := range descriptors {
		resource, _ := registry.Get(descriptor.ID)
		ids, err := resource.ScenarioIDs()
		if err != nil {
			return nil, fmt.Errorf("resource %q: list scenarios: %w", descriptor.ID, err)
		}
		for _, id := range ids {
			doc, err := resource.Scenario(id)
			if err != nil {
				return nil, err
			}
			views = append(views, scenarioView{ID: doc.ID, Resource: doc.Resource, ResourceVersion: doc.ResourceVersion})
		}
	}
	return views, nil
}

func loadScenarioDefinition(target string, registry *httpresource.Registry) (scenario.Document, httpresource.Resource, error) {
	if isScenarioPath(target) {
		raw, err := os.ReadFile(target)
		if err != nil {
			return scenario.Document{}, nil, fmt.Errorf("scenario: read %s: %w", target, err)
		}
		doc, err := scenario.Parse(raw)
		if err != nil {
			return scenario.Document{}, nil, err
		}
		resource, ok := registry.Get(doc.Resource)
		if !ok {
			return scenario.Document{}, nil, fmt.Errorf("scenario %q uses unknown resource %q", doc.ID, doc.Resource)
		}
		return doc, resource, nil
	}
	resourceID, _, ok := strings.Cut(target, ".")
	if !ok {
		return scenario.Document{}, nil, fmt.Errorf("scenario %q must be a built-in scenario ID or JSON path", target)
	}
	resource, exists := registry.Get(resourceID)
	if !exists {
		return scenario.Document{}, nil, fmt.Errorf("scenario %q names unknown resource %q", target, resourceID)
	}
	doc, err := resource.Scenario(target)
	if err != nil {
		ids, listErr := resource.ScenarioIDs()
		if listErr != nil {
			return scenario.Document{}, nil, err
		}
		return scenario.Document{}, nil, fmt.Errorf("unknown scenario %q; choose one of: %s", target, strings.Join(ids, ", "))
	}
	return doc, resource, nil
}

func isScenarioPath(target string) bool {
	if strings.ContainsRune(target, os.PathSeparator) || strings.HasSuffix(target, ".json") {
		return true
	}
	_, err := os.Stat(target)
	return err == nil
}

func writeEnvironmentList(w io.Writer, views []environmentView, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, views)
	}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		resources := make([]string, 0, len(view.Services))
		for _, service := range view.Services {
			resources = append(resources, service.Resource)
		}
		sort.Strings(resources)
		rows = append(rows, []string{view.Name, fmt.Sprintf("%d", len(view.Services)), strings.Join(resources, ",")})
	}
	writeEntityTable(w, []string{"NAME", "SERVICES", "RESOURCES"}, rows)
	return nil
}

func writeEnvironmentInspect(w io.Writer, spec environment.Spec, format output.Format) error {
	if format == output.FormatJSON {
		view := environmentInspectView{
			APIVersion: spec.APIVersion,
			Kind:       spec.Kind,
			Metadata:   spec.Metadata,
			Services:   spec.Services,
		}
		if len(spec.Proxy.Hosts) > 0 || len(spec.Proxy.Passthrough) > 0 || spec.Proxy.UnknownHosts != "" {
			proxy := spec.Proxy
			view.Proxy = &proxy
		}
		return output.JSON(w, view)
	}
	writeEntityTable(w, []string{"FIELD", "VALUE"}, [][]string{
		{"name", spec.Metadata.Name}, {"kind", spec.Kind}, {"api version", spec.APIVersion},
	})
	fmt.Fprintln(w)
	return writeServiceList(w, serviceViews(spec), format)
}

func writeServiceList(w io.Writer, views []serviceView, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, views)
	}
	rows := make([][]string, len(views))
	for i, view := range views {
		rows[i] = []string{view.Name, view.Resource, view.Scenario}
	}
	writeEntityTable(w, []string{"NAME", "RESOURCE", "SCENARIO"}, rows)
	return nil
}

func writeServiceInspect(w io.Writer, view serviceView, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, view)
	}
	writeEntityTable(w, []string{"NAME", "RESOURCE", "SCENARIO"}, [][]string{{view.Name, view.Resource, view.Scenario}})
	return nil
}

func writeResourceList(w io.Writer, views []resourceView, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, views)
	}
	rows := make([][]string, len(views))
	for i, view := range views {
		descriptor := view.Resource
		rows[i] = []string{descriptor.ID, descriptor.DisplayName, descriptor.Version, strings.Join(descriptor.ProviderHosts, ","), fmt.Sprintf("%d", len(view.Scenarios))}
	}
	writeEntityTable(w, []string{"ID", "NAME", "VERSION", "PROVIDER HOSTS", "SCENARIOS"}, rows)
	return nil
}

func writeResourceInspect(w io.Writer, view resourceView, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, view)
	}
	d := view.Resource
	rows := [][]string{
		{"id", d.ID}, {"name", d.DisplayName}, {"version", d.Version},
		{"OpenAPI", d.OpenAPIVersion}, {"OpenAPI digest", d.OpenAPIDigest},
		{"provider hosts", strings.Join(d.ProviderHosts, ", ")},
		{"scenarios", strings.Join(view.Scenarios, ", ")},
		{"SDK", strings.TrimSpace(d.SDK.Package + " " + d.SDK.Language)},
		{"direct verified", fmt.Sprintf("%t", d.SDK.DirectTest)},
		{"proxy verified", fmt.Sprintf("%t", d.SDK.ProxyTest)},
	}
	writeEntityTable(w, []string{"FIELD", "VALUE"}, rows)
	return nil
}

func writeScenarioList(w io.Writer, views []scenarioView, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, views)
	}
	rows := make([][]string, len(views))
	for i, view := range views {
		rows[i] = []string{view.ID, view.Resource, view.ResourceVersion}
	}
	writeEntityTable(w, []string{"ID", "RESOURCE", "RESOURCE VERSION"}, rows)
	return nil
}

func writeScenarioInspect(w io.Writer, doc scenario.Document, format output.Format) error {
	if format == output.FormatJSON {
		return output.JSON(w, doc)
	}
	writeEntityTable(w, []string{"FIELD", "VALUE"}, [][]string{
		{"id", doc.ID}, {"resource", doc.Resource}, {"resource version", doc.ResourceVersion},
		{"contract", doc.Contract}, {"contract version", fmt.Sprintf("%d", doc.ContractVersion)},
	})
	var state any
	if err := json.Unmarshal(doc.State, &state); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\nstate\n%s\n", raw)
	return nil
}

func writeValidation(w io.Writer, kind, name string) error {
	format, err := entityFormat()
	if err != nil {
		return err
	}
	if format == output.FormatJSON {
		return output.JSON(w, map[string]any{"kind": kind, "name": name, "valid": true})
	}
	fmt.Fprintf(w, "%s %s: valid\n", kind, name)
	return nil
}

func writeEntityTable(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
}
