package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"dalibre/cmd/dalibre/locales"
)

// Updates of Dalibre itself (Figma "11 · App updates"). Settings › General
// shows the version and checks; a new version found by a background check
// is offered in the update dialog, once per version. Installing downloads
// it, then it runs when Dalibre restarts.

// appUpdateEvery is how often Dalibre checks for a newer version of
// itself, when automatic checks are on.
const appUpdateEvery = time.Hour

// releasesURL is where Dalibre's versions are published (mygo.json).
const releasesURL = "https://github.com/djyde/dalibre/releases"

type appUpdateStatus int

const (
	auIdle          appUpdateStatus = iota // up to date, or not checked yet
	auChecking                             // asking the feed
	auCheckFailed                          // the check the user asked for failed
	auAvailable                            // release is newer than this version
	auDownloading                          // installing release
	auReady                                // installed; runs after a restart
	auInstallFailed                        // installing release failed
)

// appRelease is a newer version of Dalibre.
type appRelease struct {
	Version string
	Notes   string // Markdown, from the CHANGELOG
	Date    time.Time
	Install func(ctx context.Context, progress func(done, total int64)) error
}

// appUpdater finds newer versions of Dalibre: mygo.Updater, or a fake in
// tests.
type appUpdater interface {
	Check(ctx context.Context, beta bool) (*appRelease, error)
}

type mygoUpdater struct{}

func (mygoUpdater) Check(ctx context.Context, beta bool) (*appRelease, error) {
	mygo.Updater.SetPrereleases(beta)
	u, err := mygo.Updater.Check(ctx)
	if err != nil || u == nil {
		return nil, err
	}
	return &appRelease{Version: u.Version, Notes: u.Notes, Date: u.Date, Install: u.Install}, nil
}

// appUpdateState is where updating Dalibre is.
type appUpdateState struct {
	updater  appUpdater // nil checks nothing, as in tests that don't set one
	version  string     // the running version
	relaunch func()     // restarts Dalibre into the update

	status      appUpdateStatus
	release     *appRelease
	err         string
	done, total int64
	cancel      context.CancelFunc
	// dialog shows the update dialog.
	dialog bool
}

// CheckForUpdates is the menu's Check for Updates…: Settings › General,
// checking.
func (a *App) CheckForUpdates() {
	a.post(func() {
		a.openSettings(sectionGeneral)
		a.checkAppUpdate(true)
	})
}

// checkAppUpdate asks for a newer version of Dalibre: when manual, now,
// saying when it fails; else when automatic checks are on and the last
// was an hour ago, quietly.
func (a *App) checkAppUpdate(manual bool) {
	u := &a.au
	switch {
	case u.updater == nil, u.status == auChecking, u.status == auDownloading, u.status == auReady:
		return
	case !manual && (a.settings.AppUpdates.Manual || time.Since(a.settings.AppUpdates.Checked) < appUpdateEvery):
		return
	}
	prev := u.status
	u.status, u.err = auChecking, ""
	up, beta := u.updater, a.settings.AppUpdates.Beta
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := up.Check(ctx, beta)
		a.post(func() {
			if err != nil {
				if !manual {
					u.status = prev
					return
				}
				u.status = auCheckFailed
				u.err = locales.AppUpdatesCheckFailed()
				if errors.Is(err, mygo.ErrUpdatesDisabled) {
					u.err = locales.AppUpdatesDisabled()
				}
				return
			}
			a.settings.AppUpdates.Checked = time.Now()
			a.saveSettings()
			u.release = r
			if r == nil {
				u.status = auIdle
				return
			}
			u.status = auAvailable
			if !manual && r.Version != a.settings.AppUpdates.Skipped {
				u.dialog = true
			}
		})
	})
}

// installAppUpdate downloads the new version, installs it and restarts
// Dalibre into it.
func (a *App) installAppUpdate() {
	u := &a.au
	r := u.release
	if r == nil || u.status == auDownloading || u.status == auReady {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.status, u.err, u.done, u.total, u.cancel = auDownloading, "", 0, 0, cancel
	a.run(func() {
		last := -1
		err := r.Install(ctx, func(done, total int64) {
			// Each percent, not each read.
			pct := 0
			if total > 0 {
				pct = int(done * 100 / total)
			}
			if pct == last {
				return
			}
			last = pct
			a.post(func() { u.done, u.total = done, total })
		})
		canceled := ctx.Err() != nil
		cancel()
		a.post(func() {
			u.cancel = nil
			switch {
			case err == nil:
				// Install and restart: a quit that something cancels
				// leaves Restart now.
				u.status = auReady
				u.relaunch()
			case canceled:
				u.status = auAvailable
			default:
				u.status, u.err = auInstallFailed, err.Error()
			}
		})
	})
}

// cancelAppUpdate stops the download.
func (a *App) cancelAppUpdate() {
	if a.au.cancel != nil {
		a.au.cancel()
	}
}

// skipAppUpdate keeps automatic checks from offering this version again.
func (a *App) skipAppUpdate() {
	if r := a.au.release; r != nil {
		a.settings.AppUpdates.Skipped = r.Version
		a.saveSettings()
	}
	a.au.dialog = false
}

// watchAppUpdates checks now and then while Dalibre runs: the check
// itself waits for an hour since the last. Ticking more often than that
// keeps the checks an hour apart, not two.
func (a *App) watchAppUpdates() {
	a.checkAppUpdate(false)
	go func() {
		for range time.Tick(5 * time.Minute) {
			a.post(func() { a.checkAppUpdate(false) })
		}
	}()
}

// releaseNotes are the items of a release's Markdown notes, as plain
// lines: headings and blank lines left out.
func releaseNotes(md string, max int) []string {
	var out []string
	for line := range strings.Lines(md) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, p := range []string{"- ", "* ", "+ "} {
			line = strings.TrimPrefix(line, p)
		}
		line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
		out = append(out, line)
		if len(out) == max {
			break
		}
	}
	return out
}
