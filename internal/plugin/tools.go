package plugin

import (
	"fmt"
	"maps"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// A tool is a program a plugin runs outside its sandbox, as pandoc or a
// site's own CLI: one executable, pinned by version and hash. Dalibre
// downloads it, or uses the one a package manager installed, or a file
// the user chose, and runs it for the plugin through dalibre.exec. See
// docs/plugins/tools.md.

// Tool is an entry of the manifest's tools.
type Tool struct {
	// Version is the version Dalibre downloads: exact, as "3.6.4".
	Version string `json:"version"`
	// Min is the oldest version the plugin takes from a package manager
	// or a chosen file; Version when empty.
	Min      string `json:"min,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	License  string `json:"license,omitempty"`
	// Packages name the tool in package managers that may have installed
	// it, fully qualified.
	Packages Packages `json:"packages,omitempty"`
	// Platforms are its builds, by Platform key.
	Platforms map[string]Build `json:"platforms"`
}

// Packages name a tool in package managers.
type Packages struct {
	// Brew is a formula: "pandoc", or "owner/tap/name" for a tap's.
	Brew string `json:"brew,omitempty"`
	// Winget is a package id, as "JohnMacFarlane.Pandoc".
	Winget string `json:"winget,omitempty"`
	// Scoop is "bucket/name", as "main/pandoc".
	Scoop string `json:"scoop,omitempty"`
}

// Names are the package managers named, as their Source names.
func (p Packages) Names() []string {
	var out []string
	if p.Brew != "" {
		out = append(out, SourceBrew)
	}
	if p.Winget != "" {
		out = append(out, SourceWinget)
	}
	if p.Scoop != "" {
		out = append(out, SourceScoop)
	}
	return out
}

// Name is the tool's name in package manager mgr; "" when not named.
func (p Packages) Name(mgr string) string {
	switch mgr {
	case SourceBrew:
		return p.Brew
	case SourceWinget:
		return p.Winget
	case SourceScoop:
		return p.Scoop
	}
	return ""
}

// Build is one platform's build of a tool.
type Build struct {
	// URL is an https URL of a .zip, .tar.gz or .tgz, or of the
	// executable itself.
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
	// Bin is the executable's path inside the archive; "" when URL is the
	// executable.
	Bin string `json:"bin,omitempty"`
}

// Where a tool comes from.
const (
	SourceDownload = "download" // Dalibre's own download of the pinned build
	SourceBrew     = "brew"
	SourceWinget   = "winget"
	SourceScoop    = "scoop"
	SourceFile     = "file" // a file the user chose
)

// Platforms are the platform keys a build may be for.
var Platforms = []string{"darwin-arm64", "darwin-amd64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}

// Platform is this computer's platform key, as "darwin-arm64".
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// MaxTools is how many tools a plugin may declare.
const MaxTools = 4

// MaxToolSize bounds a tool's download.
const MaxToolSize = 512 << 20

var (
	toolIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionPattern  = regexp.MustCompile(`^v?\d+(\.\d+){0,3}([-+][0-9A-Za-z.-]+)?$`)
	brewPattern     = regexp.MustCompile(`^([a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*/)?[a-z0-9][a-z0-9@+_.-]*$`)
	wingetPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(\.[A-Za-z0-9][A-Za-z0-9_+-]*)+$`)
	scoopPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.@-]*$`)
	toolArchiveExts = []string{".zip", ".tar.gz", ".tgz"}
)

// Build is this computer's build of the tool; false when it has none.
func (t Tool) Build() (Build, bool) {
	b, ok := t.Platforms[Platform()]
	return b, ok
}

// MinVersion is the oldest version the plugin takes.
func (t Tool) MinVersion() string {
	if t.Min != "" {
		return t.Min
	}
	return t.Version
}

// Accepts reports whether version v of the tool will do.
func (t Tool) Accepts(v string) bool { return v != "" && CompareVersions(v, t.MinVersion()) >= 0 }

// Identity is what the user grants of the tool: its version and this
// computer's build. A plugin update that changes it asks again.
func (t Tool) Identity() string {
	if b, ok := t.Build(); ok {
		return t.Version + " " + b.SHA256
	}
	return t.Version
}

// ToolIdentities are the identities of the manifest's tools, by id.
func (m *Manifest) ToolIdentities() map[string]string {
	if len(m.Tools) == 0 {
		return nil
	}
	out := map[string]string{}
	for id, t := range m.Tools {
		out[id] = t.Identity()
	}
	return out
}

// ToolIDs are the manifest's tools' ids, sorted.
func (m *Manifest) ToolIDs() []string {
	ids := make([]string, 0, len(m.Tools))
	for id := range m.Tools {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// CompareVersions compares two versions as numbers, part by part, as
// "3.10" after "3.9"; a leading v and what follows a '-', '+' or '_' (a
// pre-release, a build, Homebrew's revision) are ignored.
func CompareVersions(a, b string) int {
	parts := func(s string) []int {
		s = strings.TrimPrefix(strings.TrimSpace(s), "v")
		if i := strings.IndexAny(s, "-+_ "); i >= 0 {
			s = s[:i]
		}
		var out []int
		for p := range strings.SplitSeq(s, ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	x, y := parts(a), parts(b)
	for i := range max(len(x), len(y)) {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p < q {
				return -1
			}
			return 1
		}
	}
	return 0
}

// toolVersion finds a version in what a tool printed for --version: the
// first thing that looks like one.
var toolVersion = regexp.MustCompile(`\d+(\.\d+){1,3}`)

// VersionIn is the first version in s, as a tool's --version prints it;
// "" when there is none.
func VersionIn(s string) string { return toolVersion.FindString(s) }

func (m *Manifest) checkTools() error {
	if len(m.Tools) > MaxTools {
		return fmt.Errorf("tools: at most %d", MaxTools)
	}
	for _, id := range m.ToolIDs() {
		t := m.Tools[id]
		at := "tools." + id
		switch {
		case !toolIDPattern.MatchString(id):
			return fmt.Errorf("%s: a tool's id must be lowercase letters, digits and '-', starting with a letter", at)
		case !versionPattern.MatchString(t.Version):
			return fmt.Errorf("%s.version %q must be an exact version, as \"3.6.4\"", at, t.Version)
		case t.Min != "" && !versionPattern.MatchString(t.Min):
			return fmt.Errorf("%s.min %q must be a version, as \"3.6\"", at, t.Min)
		case t.Min != "" && CompareVersions(t.Min, t.Version) > 0:
			return fmt.Errorf("%s.min %q is newer than its version %q", at, t.Min, t.Version)
		case t.Homepage != "" && !isWebURL(t.Homepage):
			return fmt.Errorf("%s.homepage %q isn’t an http(s) URL", at, t.Homepage)
		case t.Packages.Brew != "" && !brewPattern.MatchString(t.Packages.Brew):
			return fmt.Errorf("%s.packages.brew %q must be a formula, as \"pandoc\" or \"owner/tap/name\"", at, t.Packages.Brew)
		case t.Packages.Winget != "" && !wingetPattern.MatchString(t.Packages.Winget):
			return fmt.Errorf("%s.packages.winget %q must be a package id, as \"Publisher.Name\"", at, t.Packages.Winget)
		case t.Packages.Scoop != "" && !scoopPattern.MatchString(t.Packages.Scoop):
			return fmt.Errorf("%s.packages.scoop %q must be bucket/name, as \"main/pandoc\"", at, t.Packages.Scoop)
		case len(t.Platforms) == 0:
			return fmt.Errorf("%s.platforms: at least one build", at)
		}
		for _, key := range slices.Sorted(maps.Keys(t.Platforms)) {
			b := t.Platforms[key]
			bat := at + ".platforms." + key
			u, err := url.Parse(b.URL)
			switch {
			case !slices.Contains(Platforms, key):
				return fmt.Errorf("%s: the platform must be one of %s", bat, strings.Join(Platforms, ", "))
			case err != nil || u.Scheme != "https" || u.Host == "":
				return fmt.Errorf("%s.url %q must be an https URL", bat, b.URL)
			case !sha256Pattern.MatchString(b.SHA256):
				return fmt.Errorf("%s.sha256 must be the file's SHA-256, 64 lowercase hex digits", bat)
			case b.Size < 0 || b.Size > MaxToolSize:
				return fmt.Errorf("%s.size must be 0 to %d MB", bat, MaxToolSize>>20)
			}
			archive := ToolArchive(b.URL)
			switch {
			case archive && b.Bin == "":
				return fmt.Errorf("%s.bin: the archive's executable must be named, as \"pandoc-3.6.4/bin/pandoc\"", bat)
			case !archive && b.Bin != "":
				return fmt.Errorf("%s.bin is for an archive (.zip, .tar.gz, .tgz); the url is the executable itself", bat)
			case b.Bin != "" && (path.IsAbs(b.Bin) || strings.Contains(b.Bin, `\`) || slices.Contains(strings.Split(b.Bin, "/"), "..")):
				return fmt.Errorf("%s.bin %q must be a path inside the archive, with '/'", bat, b.Bin)
			}
		}
	}
	return nil
}

// ToolArchive reports whether a build's URL is an archive to unpack,
// rather than the executable.
func ToolArchive(rawURL string) bool {
	p := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		p = u.Path
	}
	p = strings.ToLower(p)
	return slices.ContainsFunc(toolArchiveExts, func(ext string) bool { return strings.HasSuffix(p, ext) })
}
