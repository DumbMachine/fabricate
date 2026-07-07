// Package output formats results in one of: json (default for
// pipes & agents), table (TTY humans), env (shell-sourceable),
// url (just the connection string).
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/engine/awsconsole"
	"github.com/dumbmachine/fabricate/engine/kubernetes"
	"github.com/dumbmachine/fabricate/engine/mongodb"
	"github.com/dumbmachine/fabricate/engine/mysql"
	"github.com/dumbmachine/fabricate/engine/postgres"
	"github.com/dumbmachine/fabricate/engine/prometheus"
	"github.com/dumbmachine/fabricate/engine/redis"
	"github.com/dumbmachine/fabricate/engine/ssh"
	"github.com/dumbmachine/fabricate/profile"
	"github.com/mattn/go-isatty"
)

// Format picks the rendering shape.
type Format string

const (
	FormatJSON  Format = "json"
	FormatTable Format = "table"
	FormatEnv   Format = "env"
	FormatURL   Format = "url"
)

// Default returns "table" for an interactive stdout, "json"
// otherwise — human-readable at a TTY, machine-readable on a pipe.
func Default() Format {
	if isatty.IsTerminal(os.Stdout.Fd()) {
		return FormatTable
	}
	return FormatJSON
}

// Resolve normalizes a possibly-empty user-supplied format string
// against Default(). Returns an error for unknown formats so the
// CLI surfaces a typo instead of silently picking JSON.
func Resolve(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return Default(), nil
	case "json":
		return FormatJSON, nil
	case "table":
		return FormatTable, nil
	case "env":
		return FormatEnv, nil
	case "url":
		return FormatURL, nil
	default:
		return "", fmt.Errorf("unknown output format %q (want one of: json, table, env, url)", s)
	}
}

// Instance prints one instance in the requested format.
func Instance(w io.Writer, inst *engine.Instance, f Format) error {
	switch f {
	case FormatJSON:
		return writeJSON(w, inst)
	case FormatURL:
		fmt.Fprintln(w, inst.Creds.URL)
		return nil
	case FormatEnv:
		writeCredsEnv(w, inst.Creds)
		return nil
	default:
		writeInstanceTable(w, inst)
		return nil
	}
}

// Creds prints just the creds in the requested format.
func Creds(w io.Writer, c engine.Creds, f Format) error {
	switch f {
	case FormatJSON:
		return writeJSON(w, c)
	case FormatURL:
		fmt.Fprintln(w, c.URL)
		return nil
	case FormatEnv:
		writeCredsEnv(w, c)
		return nil
	default:
		writeCredsTable(w, c)
		return nil
	}
}

// Instances prints many instances.
func Instances(w io.Writer, items []*engine.Instance, f Format) error {
	if f == FormatJSON {
		return writeJSON(w, items)
	}
	if f == FormatTable {
		writeInstancesTable(w, items)
		return nil
	}
	for _, inst := range items {
		if err := Instance(w, inst, f); err != nil {
			return err
		}
	}
	return nil
}

// Profiles prints the profile catalog.
func Profiles(w io.Writer, items []profile.Entry, f Format) error {
	if f == FormatJSON {
		return writeJSON(w, items)
	}
	writeProfilesTable(w, items)
	return nil
}

// Engines prints the engine registry. JSON is what agents will read;
// the table view is for humans introspecting locally.
func Engines(w io.Writer, items []engine.Info, f Format) error {
	if f == FormatJSON {
		return writeJSON(w, items)
	}
	writeEnginesTable(w, items)
	return nil
}

// SchemaJSON describes the profile.yaml shape in a machine-readable
// form. We emit a hand-built object (not a JSONSchema dialect) because
// agents only need three things: the top-level fields, the engine →
// seed-types map, and a free-form description for each field.
func SchemaJSON(w io.Writer, infos []engine.Info) error {
	type field struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Required    bool   `json:"required"`
		Description string `json:"description"`
	}
	enginesByName := map[string]engine.Info{}
	for _, i := range infos {
		enginesByName[i.Slug] = i
	}
	return writeJSON(w, map[string]any{
		"fields": []field{
			{"name", "string", true, "Profile name; what `fab create -p <name>` references."},
			{"engine", "string", true, "One of the engine slugs from `fab engines`."},
			{"label", "string", false, "Short blurb shown in `fab profiles`."},
			{"description", "string", false, "Multiline description; first line surfaces in lists."},
			{"image", "string", false, "Override the engine's default image."},
			{"defaults.database", "string", false, "Engine-specific: SQL db name, Mongo authSource, Redis db index."},
			{"defaults.username", "string", false, "Account name (ignored by Prometheus)."},
			{"defaults.password", "string", false, "Blank = fab generates a random password (preferred)."},
			{"env", "map<string,string>", false, "Extra container env vars (consumed by SSH today)."},
			{"healthcheck.timeout", "duration", false, "How long fab waits for the container to be ready."},
			{"seed", "list<seed_step>", false, "Steps to run after the engine is ready, in array order."},
			{"extensions", "list<string>", false, "Postgres only: CREATE EXTENSION IF NOT EXISTS for each, before seed."},
			{"tags", "list<string>", false, "Free-form labels."},
		},
		"seed_step": map[string]string{
			"type": "one of the seed types accepted by this engine (see engines map)",
			"file": "path to the seed file relative to profile.yaml",
		},
		"engines": enginesByName,
	})
}

func writeEnginesTable(w io.Writer, items []engine.Info) {
	if len(items) == 0 {
		fmt.Fprintln(w, "no engines")
		return
	}
	headers := []string{"ENGINE", "DEFAULT IMAGE", "PORT", "SEEDS", "DESCRIPTION"}
	rows := make([][]string, len(items))
	for i, e := range items {
		port := ""
		if e.DefaultPort > 0 {
			port = fmt.Sprintf("%d", e.DefaultPort)
		}
		rows[i] = []string{
			e.Slug, e.DefaultImage, port,
			strings.Join(e.SupportedSeeds, ","),
			e.Description,
		}
	}
	writeTable(w, headers, rows)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeCredsEnv emits the FAB_* baseline plus engine-conventional
// env vars (PGHOST, MYSQL_*, MONGO_URL, REDIS_URL, etc.) so a user
// can `eval "$(fab creds X --env)"` and have the engine's stock
// client tooling Just Work. The set we emit per engine is small on
// purpose: the canonical URL is everything most tools need, but we
// also surface the parts (host/port/db) because some legacy clients
// only read the parts.
func writeCredsEnv(w io.Writer, c engine.Creds) {
	fmt.Fprintf(w, "FAB_ENGINE=%s\n", c.Engine)
	fmt.Fprintf(w, "FAB_HOST=%s\n", c.Host)
	fmt.Fprintf(w, "FAB_PORT=%d\n", c.Port)
	if c.Username != "" {
		fmt.Fprintf(w, "FAB_USERNAME=%s\n", c.Username)
	}
	if c.Password != "" {
		fmt.Fprintf(w, "FAB_PASSWORD=%s\n", shellQuote(c.Password))
	}
	if c.Database != "" {
		fmt.Fprintf(w, "FAB_DATABASE=%s\n", c.Database)
	}
	fmt.Fprintf(w, "FAB_URL=%s\n", shellQuote(c.URL))

	switch c.Engine {
	case postgres.Engine:
		fmt.Fprintf(w, "DATABASE_URL=%s\n", shellQuote(c.URL))
		fmt.Fprintf(w, "PGHOST=%s\n", c.Host)
		fmt.Fprintf(w, "PGPORT=%d\n", c.Port)
		fmt.Fprintf(w, "PGUSER=%s\n", c.Username)
		fmt.Fprintf(w, "PGPASSWORD=%s\n", shellQuote(c.Password))
		fmt.Fprintf(w, "PGDATABASE=%s\n", c.Database)
	case mysql.Engine:
		fmt.Fprintf(w, "DATABASE_URL=%s\n", shellQuote(c.URL))
		fmt.Fprintf(w, "MYSQL_HOST=%s\n", c.Host)
		fmt.Fprintf(w, "MYSQL_TCP_PORT=%d\n", c.Port)
		fmt.Fprintf(w, "MYSQL_USER=%s\n", c.Username)
		fmt.Fprintf(w, "MYSQL_PWD=%s\n", shellQuote(c.Password))
		fmt.Fprintf(w, "MYSQL_DATABASE=%s\n", c.Database)
	case mongodb.Engine:
		fmt.Fprintf(w, "MONGO_URL=%s\n", shellQuote(c.URL))
		fmt.Fprintf(w, "MONGODB_URI=%s\n", shellQuote(c.URL))
	case redis.Engine:
		fmt.Fprintf(w, "REDIS_URL=%s\n", shellQuote(c.URL))
		fmt.Fprintf(w, "REDISCLI_AUTH=%s\n", shellQuote(c.Password))
	case prometheus.Engine:
		fmt.Fprintf(w, "PROMETHEUS_URL=%s\n", shellQuote(c.URL))
	case awsconsole.Engine:
		fmt.Fprintf(w, "AWS_ACCESS_KEY_ID=%s\n", shellQuote(c.Username))
		fmt.Fprintf(w, "AWS_SECRET_ACCESS_KEY=%s\n", shellQuote(c.Password))
		fmt.Fprintf(w, "AWS_REGION=%s\n", shellQuote(c.Database))
		fmt.Fprintf(w, "AWS_DEFAULT_REGION=%s\n", shellQuote(c.Database))
		fmt.Fprintf(w, "AWS_ENDPOINT_URL=%s\n", shellQuote(c.URL))
		if accountID := c.Extra["account_id"]; accountID != "" {
			fmt.Fprintf(w, "AWS_ACCOUNT_ID=%s\n", shellQuote(accountID))
		}
	case ssh.Engine:
		// Hand back a path the user can `ssh -i` against. The PEM
		// is also on Creds.PrivateKey, but a path is what command
		// lines expect. The cmd layer plumbs in the resolved path
		// via SetSSHKeyPath because writeCredsEnv only has Creds
		// (no container ID) at this layer.
		if sshKeyPathOverride != "" {
			fmt.Fprintf(w, "FAB_SSH_KEY_PATH=%s\n", shellQuote(sshKeyPathOverride))
		}
	case kubernetes.Engine:
		// Same pattern as ssh: kubectl wants a path, not the YAML
		// bytes. The cmd layer plumbs in the resolved file path via
		// SetKubeconfigPath after writing creds.Kubeconfig to disk.
		if kubeconfigPathOverride != "" {
			fmt.Fprintf(w, "KUBECONFIG=%s\n", shellQuote(kubeconfigPathOverride))
		}
	}
}

// sshKeyPathOverride is set by the cmd layer (creds.go) before
// rendering env output so we can include FAB_SSH_KEY_PATH without
// plumbing the container ID through the output API. It's package-
// global and fab is single-threaded per invocation, so the implicit
// state is safe.
var sshKeyPathOverride string

// SetSSHKeyPath lets the cmd layer hand the resolved key path to
// env rendering. Empty string is a no-op (the env block won't
// include the var).
func SetSSHKeyPath(p string) { sshKeyPathOverride = p }

// kubeconfigPathOverride is the kubernetes-engine analogue of
// sshKeyPathOverride. The cmd layer writes creds.Kubeconfig to a
// file (typically ~/.config/fab/kubeconfigs/<id>) and sets the
// resolved path here before rendering env output, so a sourced env
// block puts KUBECONFIG=<path> in the shell.
var kubeconfigPathOverride string

// SetKubeconfigPath lets the cmd layer hand the resolved kubeconfig
// path to env rendering. Empty string is a no-op.
func SetKubeconfigPath(p string) { kubeconfigPathOverride = p }

// shellQuote single-quotes a value POSIX-safely. Defense in depth —
// generated passwords from crypto/rand base64 don't contain quotes,
// but a user-pinned password might.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func writeInstanceTable(w io.Writer, inst *engine.Instance) {
	rows := [][2]string{
		{"name", inst.Name},
		{"engine", inst.Engine},
		{"target", inst.ProviderTarget()},
		{"profile", inst.Profile},
		{"image", inst.Image},
		{"id", Short(inst.ArtifactKey())},
		{"host", inst.Creds.Host},
		{"port", fmt.Sprintf("%d", inst.Creds.Port)},
		{"username", inst.Creds.Username},
		{"password", inst.Creds.Password},
		{"database", inst.Creds.Database},
		{"url", inst.Creds.URL},
	}
	for _, row := range credsExtraRows(inst.Creds) {
		rows = append(rows, row)
	}
	if extra := engineArtifactRows(inst.Engine); len(extra) > 0 {
		rows = append(rows, extra...)
	}
	if cmd := connectHint(inst); cmd != "" {
		rows = append(rows, [2]string{"connect", cmd})
	}
	rows = append(rows, [2]string{"created_at", inst.CreatedAt})
	writeKV(w, rows)
}

func writeCredsTable(w io.Writer, c engine.Creds) {
	rows := [][2]string{
		{"engine", c.Engine},
		{"host", c.Host},
		{"port", fmt.Sprintf("%d", c.Port)},
		{"username", c.Username},
		{"password", c.Password},
		{"database", c.Database},
		{"url", c.URL},
	}
	for _, row := range credsExtraRows(c) {
		rows = append(rows, row)
	}
	if extra := engineArtifactRows(c.Engine); len(extra) > 0 {
		rows = append(rows, extra...)
	}
	writeKV(w, rows)
}

func credsExtraRows(c engine.Creds) [][2]string {
	var rows [][2]string
	switch c.Engine {
	case awsconsole.Engine:
		for _, key := range []string{"account_id", "region", "endpoint_url"} {
			if v := c.Extra[key]; v != "" {
				rows = append(rows, [2]string{key, v})
			}
		}
	}
	return rows
}

// engineArtifactRows surfaces the on-disk artifact path for engines that
// produce one (ssh key, kubeconfig). The path is set by the cmd layer
// via SetSSHKeyPath / SetKubeconfigPath before this is called — when no
// path is wired, the row is omitted.
func engineArtifactRows(engineSlug string) [][2]string {
	var out [][2]string
	switch engineSlug {
	case "ssh":
		if sshKeyPathOverride != "" {
			out = append(out, [2]string{"key_path", sshKeyPathOverride})
		}
	case "kubernetes":
		if kubeconfigPathOverride != "" {
			out = append(out, [2]string{"kubeconfig", kubeconfigPathOverride})
		}
	}
	return out
}

// connectHint returns a copy-pasteable connect command for engines where
// the URL alone isn't enough (ssh's url uses `host:port` syntax that bare
// ssh doesn't accept; kubectl needs --kubeconfig). Empty string means
// "the URL is sufficient" (postgres, mysql, mongodb, redis, prometheus,
// github).
func connectHint(inst *engine.Instance) string {
	switch inst.Engine {
	case "ssh":
		if sshKeyPathOverride != "" {
			return fmt.Sprintf("ssh -i %s -p %d %s@%s",
				sshKeyPathOverride, inst.Creds.Port, inst.Creds.Username, inst.Creds.Host)
		}
	case "kubernetes":
		if kubeconfigPathOverride != "" {
			return fmt.Sprintf("KUBECONFIG=%s kubectl get nodes", kubeconfigPathOverride)
		}
	}
	return ""
}

func writeInstancesTable(w io.Writer, items []*engine.Instance) {
	if len(items) == 0 {
		fmt.Fprintln(w, "no live instances")
		return
	}
	headers := []string{"NAME", "ENGINE", "TARGET", "PROFILE", "HOST", "PORT", "URL"}
	rows := make([][]string, len(items))
	for i, inst := range items {
		rows[i] = []string{
			inst.Name, inst.Engine, inst.ProviderTarget(), inst.Profile, inst.Creds.Host,
			fmt.Sprintf("%d", inst.Creds.Port), inst.Creds.URL,
		}
	}
	writeTable(w, headers, rows)
}

func writeProfilesTable(w io.Writer, items []profile.Entry) {
	if len(items) == 0 {
		fmt.Fprintln(w, "no profiles")
		return
	}
	headers := []string{"NAME", "ENGINE", "SOURCE", "DESCRIPTION"}
	rows := make([][]string, len(items))
	for i, e := range items {
		desc := firstLine(e.Description)
		if e.Label != "" {
			desc = e.Label
		}
		// The slug is what `fab create` takes; show the implementation
		// engine alongside when it differs (httpmock-backed profiles
		// like linear/sprint-board).
		eng := e.Engine
		if e.Slug != "" && e.Slug != e.Engine {
			eng = fmt.Sprintf("%s (%s)", e.Slug, e.Engine)
		}
		rows[i] = []string{e.Name, eng, e.Source, desc}
	}
	writeTable(w, headers, rows)
}

// writeTable prints headers + rows with each column padded to the
// widest cell. Cheap O(N·cols); fine for catalog and instance
// listings, which top out in the dozens.
func writeTable(w io.Writer, headers []string, rows [][]string) {
	colW := make([]int, len(headers))
	for i, h := range headers {
		colW[i] = len(h)
	}
	for _, r := range rows {
		for i, v := range r {
			if len(v) > colW[i] {
				colW[i] = len(v)
			}
		}
	}
	emit := func(vals []string) {
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = pad(v, colW[i])
		}
		fmt.Fprintln(w, strings.Join(parts, "  "))
	}
	emit(headers)
	for _, r := range rows {
		emit(r)
	}
}

func writeKV(w io.Writer, rows [][2]string) {
	keyW := 0
	for _, r := range rows {
		if len(r[0]) > keyW {
			keyW = len(r[0])
		}
	}
	for _, r := range rows {
		fmt.Fprintf(w, "%s  %s\n", pad(r[0], keyW), r[1])
	}
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// Short truncates a Docker container ID to its 12-char prefix —
// the form docker itself uses in `docker ps`.
func Short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
