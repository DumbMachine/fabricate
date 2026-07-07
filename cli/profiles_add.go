package cli

// `fab profiles add` installs profile packs from GitHub repos or local
// directories into the user profiles dir — degit-style: it downloads a
// tarball of one commit (no git binary, no history) and copies the
// discovered profiles. Any repo laid out like the built-in catalog
// (<slug>/<name>/profile.yaml, optionally under a profiles/ dir)
// works as a template pack.
//
// The command is deliberately non-interactive: with multiple profiles
// and no selection it prints the list and exits nonzero with the exact
// flags to pass, instead of prompting.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine/httpmock"
	"github.com/dumbmachine/fabricate/internal/output"
	"github.com/dumbmachine/fabricate/profile"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	addList     bool
	addAll      bool
	addForce    bool
	addProfiles []string
	addDir      string
)

var profilesAddCmd = &cobra.Command{
	Use:   "add <source>",
	Short: "Install profiles from a GitHub repo or local directory (template packs)",
	Long: `Add installs profiles from a template pack into
~/.config/fab/profiles/, where they immediately work with fab create.

A template pack is any repo or directory laid out like the built-in
catalog — <slug>/<name>/profile.yaml with seed files beside it,
optionally under a top-level profiles/ dir. A directory whose root is
a single profile.yaml also works.

Sources (no git required — one tarball download, no history):

  fab profiles add acme/fab-profiles                 # GitHub shorthand
  fab profiles add acme/fab-profiles#v1.2.0          # tag / branch / commit
  fab profiles add acme/fab-profiles/packs/payments  # subdirectory
  fab profiles add https://github.com/acme/fab-profiles/tree/main/packs/payments
  fab profiles add ./my-profiles                     # local path

Selection is explicit (never prompts):

  fab profiles add acme/fab-profiles --list          # discover only
  fab profiles add acme/fab-profiles --profile checkout-db
  fab profiles add acme/fab-profiles --all

Private GitHub repos: set GITHUB_TOKEN.`,
	Args: cobra.ExactArgs(1),
	RunE: runProfilesAdd,
}

func init() {
	profilesAddCmd.Flags().BoolVar(&addList, "list", false, "List the pack's profiles without installing")
	profilesAddCmd.Flags().BoolVar(&addAll, "all", false, "Install every profile in the pack")
	profilesAddCmd.Flags().BoolVar(&addForce, "force", false, "Overwrite an existing profile with the same slug/name")
	profilesAddCmd.Flags().StringArrayVar(&addProfiles, "profile", nil, "Profile name (or slug/name) to install; repeatable")
	profilesAddCmd.Flags().StringVar(&addDir, "dir", "", "Install target (default: the user profiles dir)")
	profilesCmd.AddCommand(profilesAddCmd)
}

func runProfilesAdd(cmd *cobra.Command, args []string) error {
	f, err := output.Resolve(outputFlag)
	if err != nil {
		return err
	}

	src, err := parseAddSource(args[0])
	if err != nil {
		return err
	}

	root := src.local
	if root == "" {
		tmp, cleanup, err := fetchGitHubTarball(src)
		if err != nil {
			return err
		}
		defer cleanup()
		root = tmp
	}
	if src.subdir != "" {
		root = filepath.Join(root, filepath.FromSlash(src.subdir))
		if _, err := os.Stat(root); err != nil {
			return fmt.Errorf("source has no directory %q", src.subdir)
		}
	}

	found, err := discoverPack(root)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("no profiles found in %s (want <slug>/<name>/profile.yaml, a profiles/ dir with that layout, or a root profile.yaml)", args[0])
	}

	if addList {
		return printPack(f, found, nil)
	}

	selected, err := selectFromPack(found, addProfiles, addAll)
	if err != nil {
		// Show what's available next to the "pick something" error so
		// the next invocation can be constructed from this output alone.
		_ = printPack(f, found, nil)
		return err
	}

	targetRoot := addDir
	if targetRoot == "" {
		targetRoot = profile.UserDir()
	}
	var installed []packEntry
	for _, e := range selected {
		dst := filepath.Join(targetRoot, e.Slug, e.Name)
		if _, err := os.Stat(dst); err == nil && !addForce {
			return fmt.Errorf("profile %s/%s already exists at %s (pass --force to overwrite)", e.Slug, e.Name, dst)
		}
		if err := copyProfileDir(e.dir, dst); err != nil {
			return fmt.Errorf("install %s/%s: %w", e.Slug, e.Name, err)
		}
		installed = append(installed, e)
	}
	return printPack(f, installed, func(e packEntry) string {
		return fmt.Sprintf("fab create %s -p %s", e.Slug, e.Name)
	})
}

// ---------- source parsing ----------

// addSource is a parsed template-pack location: either a local path or
// a GitHub owner/repo (+optional ref and subdir).
type addSource struct {
	local  string // non-empty → local directory source
	owner  string
	repo   string
	ref    string // "" → HEAD
	subdir string // path inside the repo / local dir
}

// parseAddSource accepts GitHub shorthand (owner/repo[/subdir][#ref]),
// full github.com URLs (including /tree/<ref>/<subdir>), and local
// paths. Inputs are treated as untrusted: path traversal and scheme
// tricks are rejected rather than normalized.
func parseAddSource(s string) (addSource, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return addSource{}, fmt.Errorf("empty source")
	}
	if strings.Contains(s, "..") {
		return addSource{}, fmt.Errorf("source must not contain %q", "..")
	}

	// Local path: explicit prefix, or an existing directory.
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") {
		p := s
		if strings.HasPrefix(p, "~") {
			home, _ := os.UserHomeDir()
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		return addSource{local: p}, nil
	}
	if st, err := os.Stat(s); err == nil && st.IsDir() {
		return addSource{local: s}, nil
	}

	rest := s
	// Full URL form.
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(rest, prefix) {
			rest = strings.TrimPrefix(rest, prefix)
			break
		}
	}
	if strings.Contains(rest, "://") {
		return addSource{}, fmt.Errorf("unsupported source %q (github.com URLs, owner/repo shorthand, or local paths)", s)
	}

	var src addSource
	if i := strings.LastIndex(rest, "#"); i >= 0 {
		src.ref = rest[i+1:]
		rest = rest[:i]
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return addSource{}, fmt.Errorf("source %q is not owner/repo[...], a github.com URL, or a local path", s)
	}
	src.owner, src.repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	parts = parts[2:]
	// URL /tree/<ref>/<subdir...> form.
	if len(parts) > 0 && parts[0] == "tree" {
		if len(parts) < 2 {
			return addSource{}, fmt.Errorf("source %q has /tree/ but no ref", s)
		}
		if src.ref == "" {
			src.ref = parts[1]
		}
		parts = parts[2:]
	}
	src.subdir = path.Join(parts...)
	for _, seg := range []string{src.owner, src.repo, src.ref} {
		if strings.ContainsAny(seg, "?&%\\") {
			return addSource{}, fmt.Errorf("source %q contains invalid characters", s)
		}
	}
	return src, nil
}

// ---------- fetching ----------

// fetchGitHubTarball downloads one commit of the repo as a tarball
// (codeload, same endpoint degit-style tools use) and extracts it to a
// temp dir, which the returned cleanup removes. GITHUB_TOKEN is passed
// through for private repos.
func fetchGitHubTarball(src addSource) (string, func(), error) {
	ref := src.ref
	if ref == "" {
		ref = "HEAD"
	}
	url := fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", src.owner, src.repo, ref)

	fmt.Fprintf(os.Stderr, "fab: fetching %s/%s@%s...\n", src.owner, src.repo, ref)
	body, err := openTarballStream(url, os.Getenv("GITHUB_TOKEN"))
	if err != nil {
		return "", nil, err
	}
	defer body.Close()

	tmp, err := os.MkdirTemp("", "fab-profiles-add-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if err := extractTarGz(body, tmp); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract: %w", err)
	}
	return tmp, cleanup, nil
}

// openTarballStream returns the tarball body, via Go's HTTP client
// first and `curl` as a fallback. The fallback matters on networks
// where curl works but Go's TLS/DNS stack is blocked (corporate
// proxies, TLS-fingerprint filters, VPN split DNS) — curl also
// inherits whatever proxy/CA configuration made it work there.
func openTarballStream(url, token string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		if resp.StatusCode == 200 {
			return resp.Body, nil
		}
		resp.Body.Close()
		hint := ""
		if resp.StatusCode == 404 && token == "" {
			hint = " (private repo? set GITHUB_TOKEN)"
		}
		return nil, fmt.Errorf("fetch %s: HTTP %d%s", url, resp.StatusCode, hint)
	}
	httpErr := err

	curl, lookErr := exec.LookPath("curl")
	if lookErr != nil {
		return nil, fmt.Errorf("fetch %s: %w (and no curl on PATH for fallback)", url, httpErr)
	}
	fmt.Fprintf(os.Stderr, "fab: direct fetch failed (%v); retrying via curl...\n", httpErr)
	args := []string{"-sSfL", "--max-time", "120", url}
	if token != "" {
		args = append(args, "-H", "Authorization: Bearer "+token)
	}
	out, err := exec.Command(curl, args...).Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("fetch %s via curl: %w%s", url, err, msg)
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

// maxPackBytes caps a template pack's total extracted size. Packs are
// YAML + seed files; anything past this is not a profile pack.
const maxPackBytes = 256 << 20

// extractTarGz unpacks a GitHub tarball into dst, stripping the
// tarball's single top-level directory. Entries are validated the way
// untrusted archives must be: no absolute paths, no traversal, no
// symlinks, bounded total size.
func extractTarGz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return fmt.Errorf("archive entry escapes extraction dir: %q", hdr.Name)
		}
		// Strip "<repo>-<sha>/", the tarball's single root dir.
		i := strings.IndexByte(name, '/')
		if i < 0 {
			continue
		}
		rel := name[i+1:]
		if rel == "" {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			written += hdr.Size
			if written > maxPackBytes {
				return fmt.Errorf("archive exceeds %d MiB — not a profile pack", maxPackBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, io.LimitReader(tr, hdr.Size)); err != nil {
				w.Close()
				return err
			}
			w.Close()
		default:
			// Symlinks, devices, etc. have no business in a profile pack.
			continue
		}
	}
}

// ---------- discovery ----------

// packEntry is one installable profile found in a pack.
type packEntry struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Label  string `json:"label,omitempty"`

	dir string // absolute source dir on disk
}

// discoverPack finds profiles under root. Layouts, most specific
// first: the built-in catalog shape <slug>/<name>/profile.yaml, the
// same shape under profiles/, or a single profile.yaml at the root.
func discoverPack(root string) ([]packEntry, error) {
	// Single-profile source: profile.yaml at the root.
	if _, err := os.Stat(filepath.Join(root, "profile.yaml")); err == nil {
		e, err := packEntryFromDir(root, "")
		if err != nil {
			return nil, err
		}
		return []packEntry{*e}, nil
	}

	for _, base := range []string{root, filepath.Join(root, "profiles")} {
		entries, err := scanCatalogLayout(base)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			return entries, nil
		}
	}
	return nil, nil
}

// scanCatalogLayout reads <base>/<slug>/<name>/profile.yaml entries.
func scanCatalogLayout(base string) ([]packEntry, error) {
	slugs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []packEntry
	for _, slugDir := range slugs {
		if !slugDir.IsDir() || strings.HasPrefix(slugDir.Name(), ".") {
			continue
		}
		names, err := os.ReadDir(filepath.Join(base, slugDir.Name()))
		if err != nil {
			continue
		}
		for _, nameDir := range names {
			if !nameDir.IsDir() {
				continue
			}
			dir := filepath.Join(base, slugDir.Name(), nameDir.Name())
			if _, err := os.Stat(filepath.Join(dir, "profile.yaml")); err != nil {
				continue
			}
			e, err := packEntryFromDir(dir, slugDir.Name())
			if err != nil {
				fmt.Fprintf(os.Stderr, "fab: warning: skip %s/%s: %v\n", slugDir.Name(), nameDir.Name(), err)
				continue
			}
			out = append(out, *e)
		}
	}
	return out, nil
}

// packEntryFromDir parses a profile dir into a packEntry. slug may be
// empty (single-profile source) — then it's derived from the yaml:
// env.MOCK_SERVICE for httpmock profiles (matching the catalog
// convention), the engine otherwise.
func packEntryFromDir(dir, slug string) (*packEntry, error) {
	data, err := os.ReadFile(filepath.Join(dir, "profile.yaml"))
	if err != nil {
		return nil, err
	}
	var p struct {
		Name   string            `yaml:"name"`
		Engine string            `yaml:"engine"`
		Label  string            `yaml:"label"`
		Env    map[string]string `yaml:"env"`
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile.yaml: %w", err)
	}
	name := p.Name
	if name == "" {
		name = filepath.Base(dir)
	}
	if slug == "" {
		slug = p.Engine
		if p.Engine == httpmock.Engine && p.Env["MOCK_SERVICE"] != "" {
			slug = p.Env["MOCK_SERVICE"]
		}
	}
	if slug == "" {
		return nil, fmt.Errorf("profile %q declares no engine", name)
	}
	if strings.ContainsAny(slug+name, "/\\") || strings.Contains(slug+name, "..") {
		return nil, fmt.Errorf("unsafe slug/name %q/%q", slug, name)
	}
	return &packEntry{Slug: slug, Name: name, Engine: p.Engine, Label: p.Label, dir: dir}, nil
}

// selectFromPack applies --profile / --all selection. With neither and
// more than one profile found, it errors with instructions instead of
// prompting — this command has to work unattended.
func selectFromPack(found []packEntry, wanted []string, all bool) ([]packEntry, error) {
	if all {
		return found, nil
	}
	if len(wanted) == 0 {
		if len(found) == 1 {
			return found, nil
		}
		return nil, fmt.Errorf("pack has %d profiles — pass --profile <name> (repeatable) or --all", len(found))
	}
	var out []packEntry
	for _, w := range wanted {
		matched := false
		for _, e := range found {
			if e.Name == w || e.Slug+"/"+e.Name == w {
				out = append(out, e)
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("no profile %q in this pack (run with --list to see it)", w)
		}
	}
	return out, nil
}

// ---------- install ----------

// copyProfileDir copies a discovered profile into the user catalog,
// files and subdirs, skipping anything that isn't a regular file.
func copyProfileDir(srcDir, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// printPack renders discovered/installed profiles; createHint, when
// set, adds the runnable create command per row.
func printPack(f output.Format, entries []packEntry, createHint func(packEntry) string) error {
	if f == output.FormatJSON {
		type row struct {
			packEntry
			Create string `json:"create,omitempty"`
		}
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			r := row{packEntry: e}
			if createHint != nil {
				r.Create = createHint(e)
			}
			rows = append(rows, r)
		}
		return output.JSON(os.Stdout, rows)
	}
	for _, e := range entries {
		line := fmt.Sprintf("%s/%s", e.Slug, e.Name)
		if e.Label != "" {
			line += "  — " + e.Label
		}
		fmt.Fprintln(os.Stdout, line)
		if createHint != nil {
			fmt.Fprintf(os.Stdout, "  %s\n", createHint(e))
		}
	}
	return nil
}
