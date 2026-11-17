package pkgmgr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// brewPrefixes are where Homebrew may be: Apple silicon's, Intel's and
// Linux's.
var brewPrefixes = func() []string {
	ps := []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"}
	if home, err := os.UserHomeDir(); err == nil {
		ps = append(ps, filepath.Join(home, ".linuxbrew"))
	}
	return ps
}

// brewName splits a formula as a manifest names it, "pandoc" or
// "owner/tap/name", into its tap (homebrew/core for a bare name) and
// name.
func brewName(pkg string) (tap, name string) {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[:i], pkg[i+1:]
	}
	return "homebrew/core", pkg
}

// findBrew reads Homebrew's Cellar: the newest installed version of the
// formula whose install receipt names the tap asked for. A formula of the
// same name from another tap (homebrew/core's zlib, a library, for
// heartleo/tap's zlib, a program) isn't it.
func findBrew(pkg, command string) (Found, bool) {
	tap, name := brewName(pkg)
	for _, prefix := range brewPrefixes() {
		cellar := filepath.Join(prefix, "Cellar", name)
		entries, err := os.ReadDir(cellar)
		if err != nil {
			continue
		}
		var versions []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if receiptTap(filepath.Join(cellar, e.Name(), "INSTALL_RECEIPT.json")) == tap {
				versions = append(versions, e.Name())
			}
		}
		if len(versions) == 0 {
			continue
		}
		slices.SortFunc(versions, compareVersions)
		v := versions[len(versions)-1]
		// opt/<name> follows upgrades; the Cellar's folder is the version's.
		for _, exe := range []string{
			filepath.Join(prefix, "opt", name, "bin", command),
			filepath.Join(cellar, v, "bin", command),
		} {
			if isFile(exe) {
				return Found{Path: exe, Version: v}, true
			}
		}
	}
	return Found{}, false
}

// receiptTap is the tap an install receipt says the formula came from.
func receiptTap(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var r struct {
		Source struct {
			Tap string `json:"tap"`
		} `json:"source"`
	}
	if json.Unmarshal(data, &r) != nil {
		return ""
	}
	if r.Source.Tap == "" {
		return "homebrew/core" // receipts from before taps were recorded
	}
	return strings.ToLower(r.Source.Tap)
}
