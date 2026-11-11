package pkgmgr

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scoopRoot is Scoop's folder: $SCOOP, or ~/scoop.
func scoopRoot() string {
	if r := os.Getenv("SCOOP"); r != "" {
		return r
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "scoop")
}

// findScoop reads Scoop's apps/<name>/current: its manifest's version and
// the bucket its install record names.
func findScoop(pkg, command string) (Found, bool) {
	if runtime.GOOS != "windows" {
		return Found{}, false
	}
	bucket, name, ok := strings.Cut(pkg, "/")
	if !ok {
		return Found{}, false
	}
	current := filepath.Join(scoopRoot(), "apps", name, "current")
	var install struct {
		Bucket string `json:"bucket"`
	}
	if data, err := os.ReadFile(filepath.Join(current, "install.json")); err != nil || json.Unmarshal(data, &install) != nil || !strings.EqualFold(install.Bucket, bucket) {
		return Found{}, false
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if data, err := os.ReadFile(filepath.Join(current, "manifest.json")); err != nil || json.Unmarshal(data, &manifest) != nil {
		return Found{}, false
	}
	if exe := findExe(current, command, 3); exe != "" {
		return Found{Path: exe, Version: manifest.Version}, true
	}
	return Found{}, false
}

// findWinget looks for a portable package's folder under WinGet's
// Packages; for one that installed elsewhere, winget list confirms the
// package and the program is the command on PATH.
func findWinget(id, command string) (Found, bool) {
	if runtime.GOOS != "windows" {
		return Found{}, false
	}
	pkgs := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Packages")
	if entries, err := os.ReadDir(pkgs); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(strings.ToLower(e.Name()), strings.ToLower(id)+"_") {
				continue
			}
			if exe := findExe(filepath.Join(pkgs, e.Name()), command, 4); exe != "" {
				return Found{Path: exe, Version: versionOf(exe)}, true
			}
		}
	}
	v := wingetListed(id)
	if v == "" {
		return Found{}, false
	}
	exe, err := exec.LookPath(command)
	if err != nil {
		return Found{}, false
	}
	return Found{Path: exe, Version: v}, true
}

var (
	wingetMu    sync.Mutex
	wingetCache = map[string]struct {
		v  string
		at time.Time
	}{}
)

// wingetListed is the version winget list reports for package id; ""
// when it isn't installed. Asked at most every five minutes.
func wingetListed(id string) string {
	wingetMu.Lock()
	defer wingetMu.Unlock()
	if c, ok := wingetCache[id]; ok && time.Since(c.at) < 5*time.Minute {
		return c.v
	}
	exe := executable(Winget)
	v := ""
	if exe != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := exec.CommandContext(ctx, exe, "list", "--id", id, "--exact", "--accept-source-agreements", "--disable-interactivity").Output()
		cancel()
		if err == nil {
			for line := range strings.Lines(string(out)) {
				if strings.Contains(strings.ToLower(line), strings.ToLower(id)) {
					v = versionPattern.FindString(line[strings.Index(strings.ToLower(line), strings.ToLower(id))+len(id):])
					break
				}
			}
		}
	}
	wingetCache[id] = struct {
		v  string
		at time.Time
	}{v, time.Now()}
	return v
}

var versionPattern = regexp.MustCompile(`\d+(\.\d+){1,3}`)

// versionOf is the version a program prints for --version.
func versionOf(exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, exe, "--version").CombinedOutput()
	return versionPattern.FindString(string(out))
}

// findExe finds command's program in dir, at most depth folders down.
func findExe(dir, command string, depth int) string {
	want := strings.ToLower(command)
	if runtime.GOOS == "windows" && !strings.HasSuffix(want, ".exe") {
		want += ".exe"
	}
	found := ""
	base := strings.Count(dir, string(filepath.Separator))
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && strings.Count(p, string(filepath.Separator))-base >= depth {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.ToLower(d.Name()) == want {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// compareVersions orders versions as numbers, part by part, Homebrew's
// _1 revision after the version.
func compareVersions(a, b string) int {
	split := func(s string) []int {
		var out []int
		for p := range strings.FieldsFuncSeq(s, func(r rune) bool { return r == '.' || r == '_' || r == '-' }) {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	x, y := split(a), split(b)
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
