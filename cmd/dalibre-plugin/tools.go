package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"dalibre/internal/plugin"
)

// toolStore is where the CLI keeps the tool builds it downloads: the
// user's cache folder, so check and run don't download them again.
func toolStore() *plugin.ToolStore {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return &plugin.ToolStore{Dir: filepath.Join(dir, "dalibre-plugin", "tools")}
}

// toolPaths finds the plugin's tools for running it: --tool id=path when
// given, else what a package manager installed, else the build,
// downloaded once into the CLI's cache.
func toolPaths(ctx context.Context, m *plugin.Manifest, given setFlags) (map[string]string, error) {
	if len(m.Tools) == 0 {
		return nil, nil
	}
	store := toolStore()
	out := map[string]string{}
	for _, id := range m.ToolIDs() {
		t := m.Tools[id]
		if p, ok := given[id]; ok {
			out[id] = p
			continue
		}
		if p, ok := plugin.FoundInManager(t, id); ok {
			fmt.Fprintf(os.Stderr, "%s %s: %s's, %s\n", id, p.Version, p.Source, p.Path)
			out[id] = p.Path
			continue
		}
		b, ok := t.Build()
		if !ok {
			return nil, fmt.Errorf("tools.%s has no build for %s; pass --tool %s=<path>", id, plugin.Platform(), id)
		}
		exe, err := fetchTool(ctx, store, id, b)
		if err != nil {
			return nil, fmt.Errorf("tools.%s: %w", id, err)
		}
		out[id] = exe
	}
	return out, nil
}

// fetchTool downloads a build into store, saying so on stderr.
func fetchTool(ctx context.Context, store *plugin.ToolStore, id string, b plugin.Build) (string, error) {
	if exe, ok := store.Path(b); ok {
		return exe, nil
	}
	fmt.Fprintf(os.Stderr, "downloading %s from %s…\n", id, b.URL)
	exe, err := store.Fetch(ctx, b, nil)
	var mm *plugin.BuildMismatch
	if errors.As(err, &mm) {
		return "", fmt.Errorf("%s: its SHA-256 is %s, not the %s plugin.json says", b.URL, mm.Got, mm.Want)
	}
	return exe, err
}

// checkTools downloads every platform's build of each tool, checking its
// size, hash and executable, and runs this computer's with --version.
func checkTools(ctx context.Context, m *plugin.Manifest) error {
	store := toolStore()
	for _, id := range m.ToolIDs() {
		t := m.Tools[id]
		fmt.Printf("tool %s %s (min %s)", id, t.Version, t.MinVersion())
		if names := t.Packages.Names(); len(names) > 0 {
			fmt.Printf(", packages: %s", strings.Join(names, ", "))
		}
		fmt.Println()
		for _, key := range plugin.Platforms {
			b, ok := t.Platforms[key]
			if !ok {
				fmt.Printf("  %-14s no build: users there choose a file or a package manager's\n", key)
				continue
			}
			exe, err := fetchTool(ctx, store, id, b)
			if err != nil {
				return fmt.Errorf("tools.%s.platforms.%s: %w", id, key, err)
			}
			fmt.Printf("  %-14s ok\n", key)
			if key == plugin.Platform() {
				out, _ := runVersion(ctx, exe)
				v := plugin.VersionIn(out)
				switch {
				case v == "":
					fmt.Printf("  warning: %s --version printed no version\n", id)
				case !t.Accepts(v):
					return fmt.Errorf("tools.%s: the build says it is %s, older than min %s", id, v, t.MinVersion())
				default:
					fmt.Printf("  runs: %s\n", v)
				}
			}
		}
	}
	return nil
}

func runVersion(ctx context.Context, exe string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--version").CombinedOutput()
	return string(out), err
}

// cmdTool is dalibre-plugin tool add.
func cmdTool(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: dalibre-plugin tool add <dir> <brew:owner/tap/name | gh:owner/repo@tag> [--id ID] [--bin NAME] [--brew F] [--winget ID] [--scoop B/N] [--min V]")
	}
	fs := flag.NewFlagSet("tool add", flag.ExitOnError)
	id := fs.String("id", "", "the tool's id (default: the program's name)")
	bin := fs.String("bin", "", "the program's name in the archives (default: the id)")
	brew := fs.String("brew", "", "its Homebrew formula, for a tool from a GitHub release")
	winget := fs.String("winget", "", "its winget package id")
	scoop := fs.String("scoop", "", "its Scoop bucket/name")
	min := fs.String("min", "", "the oldest version the plugin takes")
	pos := parse(fs, args[1:], "dir", "source")
	dir, src := pos[0], pos[1]
	m, err := plugin.ReadManifest(dir)
	if err != nil {
		return err
	}
	var t plugin.Tool
	var name string
	switch {
	case strings.HasPrefix(src, "brew:"):
		t, name, err = toolFromFormula(ctx, strings.TrimPrefix(src, "brew:"))
	case strings.HasPrefix(src, "gh:"):
		t, name, err = toolFromRelease(ctx, strings.TrimPrefix(src, "gh:"), cmpOr(*bin, *id))
	default:
		return fmt.Errorf("%q: give brew:owner/tap/name or gh:owner/repo@tag", src)
	}
	if err != nil {
		return err
	}
	if *id == "" {
		*id = cmpOr(*bin, name)
	}
	command := cmpOr(*bin, *id)
	if *brew != "" {
		t.Packages.Brew = *brew
	}
	if *winget != "" {
		t.Packages.Winget = *winget
	}
	if *scoop != "" {
		t.Packages.Scoop = *scoop
	}
	if *min != "" {
		t.Min = *min
	}
	// Each build, downloaded once: its hash and size, and the program in
	// an archive.
	for key, b := range t.Platforms {
		fmt.Fprintf(os.Stderr, "%s: %s\n", key, b.URL)
		b, err = inspectBuild(ctx, b, command, strings.HasPrefix(key, "windows"))
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		t.Platforms[key] = b
	}
	if m.Tools == nil {
		m.Tools = map[string]plugin.Tool{}
	}
	m.Tools[*id] = t
	if err := setManifestField(filepath.Join(dir, "plugin.json"), "tools", m.Tools); err != nil {
		return err
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range t.Platforms {
			if !yield(k) {
				return
			}
		}
	})
	fmt.Printf("added tools.%s %s: %s\n", *id, t.Version, strings.Join(keys, ", "))
	if _, err := plugin.ReadManifest(dir); err != nil {
		return fmt.Errorf("plugin.json no longer reads: %w", err)
	}
	return nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var httpClient = &http.Client{Transport: plugin.SystemTransport(), Timeout: 10 * time.Minute}

func get(ctx context.Context, u string, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", plugin.UserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(u, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, plugin.MaxToolSize+1))
}

// inspectBuild downloads build b once: it checks the hash the release
// gave, or fills it in, with the size, and finds the program command in
// an archive.
func inspectBuild(ctx context.Context, b plugin.Build, command string, windows bool) (plugin.Build, error) {
	data, err := get(ctx, b.URL, "")
	if err != nil {
		return b, err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if b.SHA256 != "" && b.SHA256 != got {
		return b, fmt.Errorf("its SHA-256 is %s, not the %s the release lists", got, b.SHA256)
	}
	b.SHA256, b.Size = got, int64(len(data))
	if !plugin.ToolArchive(b.URL) {
		return b, nil
	}
	tmp, err := os.MkdirTemp("", "dalibre-tool-")
	if err != nil {
		return b, err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "download")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return b, err
	}
	root := filepath.Join(tmp, "files")
	os.MkdirAll(root, 0o755)
	if err := plugin.UnpackBuild(file, b, root); err != nil {
		return b, err
	}
	want := command
	if windows {
		want += ".exe"
	}
	var found []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), want) {
			rel, _ := filepath.Rel(root, p)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if len(found) == 0 {
		return b, fmt.Errorf("%s has no %s; pass --bin with the program's name", path.Base(b.URL), want)
	}
	slices.SortFunc(found, func(x, y string) int { return len(x) - len(y) })
	b.Bin = found[0]
	return b, nil
}

// toolFromRelease reads a GitHub release's assets: one build per
// platform, by the names release tools give them (goreleaser's
// _darwin_arm64, cargo-dist's -aarch64-apple-darwin, …), with hashes
// from its checksums file when it has one.
func toolFromRelease(ctx context.Context, spec, command string) (plugin.Tool, string, error) {
	repo, tag, ok := strings.Cut(spec, "@")
	if !ok || strings.Count(repo, "/") != 1 {
		return plugin.Tool{}, "", errors.New("give gh:owner/repo@tag")
	}
	data, err := get(ctx, "https://api.github.com/repos/"+repo+"/releases/tags/"+tag, "application/vnd.github+json")
	if err != nil {
		return plugin.Tool{}, "", err
	}
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return plugin.Tool{}, "", err
	}
	name := path.Base(repo)
	if command == "" {
		command = name
	}
	sums := map[string]string{}
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if strings.Contains(lower, "checksums") || strings.HasPrefix(lower, "sha256sums") {
			if body, err := get(ctx, a.URL, ""); err == nil {
				for line := range strings.Lines(string(body)) {
					f := strings.Fields(line)
					if len(f) == 2 {
						sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
					}
				}
			}
		}
	}
	t := plugin.Tool{Version: strings.TrimPrefix(tag, "v"), Homepage: "https://github.com/" + repo, Platforms: map[string]plugin.Build{}}
	rank := map[string]int{}
	for _, a := range rel.Assets {
		key, score := assetPlatform(a.Name)
		if key == "" || score <= rank[key] {
			continue
		}
		rank[key] = score
		t.Platforms[key] = plugin.Build{URL: a.URL, SHA256: sums[a.Name], Size: a.Size}
	}
	if len(t.Platforms) == 0 {
		return plugin.Tool{}, "", fmt.Errorf("%s %s has no builds named for a platform", repo, tag)
	}
	if lic, err := get(ctx, "https://api.github.com/repos/"+repo, "application/vnd.github+json"); err == nil {
		var r struct {
			License struct {
				SPDX string `json:"spdx_id"`
			} `json:"license"`
		}
		if json.Unmarshal(lic, &r) == nil && r.License.SPDX != "NOASSERTION" {
			t.License = r.License.SPDX
		}
	}
	return t, command, nil
}

var (
	osWords = map[string][]string{
		"darwin": {"darwin", "macos", "apple", "osx", "mac"}, "linux": {"linux"}, "windows": {"windows", "win64", "win", "pc-windows"},
	}
	archWords = map[string][]string{
		"arm64": {"arm64", "aarch64"}, "amd64": {"amd64", "x86_64", "x64", "x86-64"},
	}
	skipExt   = []string{".sha256", ".sig", ".asc", ".pem", ".sbom", ".json", ".txt", ".deb", ".rpm", ".msi", ".pkg", ".dmg", ".apk", ".zst", ".xz", ".bz2"}
	wordSplit = regexp.MustCompile(`[^a-z0-9_]+|_`)
)

// assetPlatform is the platform an asset's name says it is for, and how
// good a build it is (archives before bare programs, glibc before musl);
// "" when it isn't one.
func assetPlatform(name string) (string, int) {
	lower := strings.ToLower(name)
	for _, ext := range skipExt {
		if strings.HasSuffix(lower, ext) {
			return "", 0
		}
	}
	words := wordSplit.Split(strings.ReplaceAll(lower, "x86_64", "x64"), -1)
	has := func(list []string) bool {
		for _, w := range words {
			if slices.Contains(list, w) || slices.Contains(list, strings.ReplaceAll(w, "x64", "x86_64")) {
				return true
			}
		}
		return false
	}
	osName := ""
	for _, o := range []string{"darwin", "windows", "linux"} {
		if has(osWords[o]) {
			osName = o
			break
		}
	}
	arch := ""
	for _, a := range []string{"arm64", "amd64"} {
		if has(archWords[a]) {
			arch = a
			break
		}
	}
	if osName == "darwin" && arch == "" && (strings.Contains(lower, "universal") || strings.Contains(lower, "all")) {
		arch = "arm64"
	}
	if osName == "" || arch == "" {
		return "", 0
	}
	score := 1
	if plugin.ToolArchive("https://x/" + lower) {
		score = 3
	}
	if strings.Contains(lower, "musl") {
		score--
	}
	return osName + "-" + arch, score
}

// toolFromFormula reads a tap's formula, as goreleaser writes one: a url
// and a sha256 for each system and processor. Homebrew's own formulas
// build from source, so their tools come from a release (gh:) with
// --brew instead.
func toolFromFormula(ctx context.Context, formula string) (plugin.Tool, string, error) {
	parts := strings.Split(formula, "/")
	if len(parts) != 3 {
		return plugin.Tool{}, "", errors.New("give a tap's formula, brew:owner/tap/name; for one of Homebrew's own, use gh:owner/repo@tag --brew name")
	}
	owner, tap, name := parts[0], parts[1], parts[2]
	var src []byte
	var err error
	for _, p := range []string{"Formula/" + name + ".rb", name + ".rb"} {
		src, err = get(ctx, "https://raw.githubusercontent.com/"+owner+"/homebrew-"+tap+"/HEAD/"+p, "")
		if err == nil {
			break
		}
	}
	if err != nil {
		return plugin.Tool{}, "", fmt.Errorf("couldn’t read the formula: %w", err)
	}
	t, err := parseFormula(src)
	if err != nil {
		return plugin.Tool{}, "", err
	}
	t.Packages.Brew = formula
	return t, name, nil
}

var (
	rbVersion  = regexp.MustCompile(`^\s*version\s+"([^"]+)"`)
	rbURL      = regexp.MustCompile(`^\s*url\s+"([^"]+)"`)
	rbSHA      = regexp.MustCompile(`^\s*sha256\s+"([0-9a-f]{64})"`)
	rbHomepage = regexp.MustCompile(`^\s*homepage\s+"([^"]+)"`)
	rbLicense  = regexp.MustCompile(`^\s*license\s+"([^"]+)"`)
)

// parseFormula reads the builds of a goreleaser-made formula.
func parseFormula(src []byte) (plugin.Tool, error) {
	t := plugin.Tool{Platforms: map[string]plugin.Build{}}
	osName, arch, url := "", "", ""
	for line := range strings.Lines(string(src)) {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "on_macos"):
			osName = "darwin"
		case strings.HasPrefix(l, "on_linux"):
			osName = "linux"
		case strings.Contains(l, "Hardware::CPU.intel?"):
			arch = "amd64"
		case strings.Contains(l, "Hardware::CPU.arm?"):
			arch = "arm64"
		}
		if m := rbVersion.FindStringSubmatch(line); m != nil {
			t.Version = m[1]
		}
		if m := rbHomepage.FindStringSubmatch(line); m != nil {
			t.Homepage = m[1]
		}
		if m := rbLicense.FindStringSubmatch(line); m != nil {
			t.License = m[1]
		}
		if m := rbURL.FindStringSubmatch(line); m != nil {
			url = m[1]
		}
		if m := rbSHA.FindStringSubmatch(line); m != nil && url != "" && osName != "" && arch != "" {
			t.Platforms[osName+"-"+arch] = plugin.Build{URL: url, SHA256: m[1]}
			url = ""
		}
	}
	if t.Version == "" || len(t.Platforms) == 0 {
		return t, errors.New("the formula has no builds for a system and processor (only goreleaser-made formulas are read)")
	}
	return t, nil
}

// setManifestField sets a top-level field of plugin.json to v, keeping
// the other fields as they are, in their order.
func setManifestField(file, key string, v any) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("plugin.json isn’t an object")
	}
	type field struct {
		key string
		raw json.RawMessage
	}
	var fields []field
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		fields = append(fields, field{tok.(string), raw})
	}
	val, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(fields, func(f field) bool { return f.key == key }); i >= 0 {
		fields[i].raw = val
	} else {
		fields = append(fields, field{key, val})
	}
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, f := range fields {
		k, _ := json.Marshal(f.key)
		var ind bytes.Buffer
		json.Indent(&ind, f.raw, "  ", "  ")
		fmt.Fprintf(&out, "  %s: %s", k, ind.Bytes())
		if i < len(fields)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	return os.WriteFile(file, out.Bytes(), 0o644)
}
