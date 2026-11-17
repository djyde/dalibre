// Package pkgmgr finds programs that package managers (Homebrew, winget,
// Scoop) installed, by reading their own records rather than by name on
// PATH, and installs them with those package managers.
package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dalibre/internal/proc"
)

// The package managers, as plugin.Source* names them.
const (
	Brew   = "brew"
	Winget = "winget"
	Scoop  = "scoop"
)

// Title is how the user knows package manager mgr.
func Title(mgr string) string {
	switch mgr {
	case Brew:
		return "Homebrew"
	case Winget:
		return "winget"
	case Scoop:
		return "Scoop"
	}
	return mgr
}

// Found is a program a package manager installed.
type Found struct {
	Path    string
	Version string
}

// Available reports whether package manager mgr is on this computer.
func Available(mgr string) bool { return executable(mgr) != "" }

// executable is package manager mgr's own program; "" when it isn't
// installed.
func executable(mgr string) string {
	switch mgr {
	case Brew:
		for _, p := range brewPrefixes() {
			if exe := filepath.Join(p, "bin", "brew"); isFile(exe) {
				return exe
			}
		}
	case Scoop:
		if runtime.GOOS != "windows" {
			return ""
		}
		if exe := filepath.Join(scoopRoot(), "shims", "scoop.cmd"); isFile(exe) {
			return exe
		}
	case Winget:
		if runtime.GOOS != "windows" {
			return ""
		}
		if exe := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "winget.exe"); isFile(exe) {
			return exe
		}
		if exe, err := exec.LookPath("winget"); err == nil {
			return exe
		}
	}
	return ""
}

// Find is the program command (as "pandoc") that package manager mgr
// installed as package pkg, as the manifest names it; false when it
// hasn't.
func Find(mgr, pkg, command string) (Found, bool) {
	switch mgr {
	case Brew:
		return findBrew(pkg, command)
	case Scoop:
		return findScoop(pkg, command)
	case Winget:
		return findWinget(pkg, command)
	}
	return Found{}, false
}

// Command is what installs package pkg with mgr, as it is run and shown.
func Command(mgr, pkg string) []string {
	switch mgr {
	case Brew:
		return []string{"brew", "install", pkg}
	case Scoop:
		return []string{"scoop", "install", pkg}
	case Winget:
		return []string{"winget", "install", "--id", pkg, "--exact", "--scope", "user", "--silent",
			"--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}
	}
	return nil
}

// Install runs Command(mgr, pkg), giving each line it prints to line,
// until it ends or ctx is done. Its error says how it ended.
func Install(ctx context.Context, mgr, pkg string, line func(string)) error {
	exe := executable(mgr)
	if exe == "" {
		return fmt.Errorf("%s isn’t installed", Title(mgr))
	}
	args := Command(mgr, pkg)[1:]
	var cmd *exec.Cmd
	if strings.HasSuffix(strings.ToLower(exe), ".cmd") {
		cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", exe}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, exe, args...)
	}
	proc.Group(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(),
		"NONINTERACTIVE=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_INSTALL_CLEANUP=1",
		"PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	proc.Lines(out, line)
	err = cmd.Wait()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.As(err, &exit):
		return fmt.Errorf("%s stopped with exit code %d", Title(mgr), exit.ExitCode())
	}
	return err
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
