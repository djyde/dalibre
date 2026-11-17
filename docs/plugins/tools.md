# Plugin tools

Status: built, except dalibre.mcp (Open questions). Extends plugin API 1
without breaking it; see [design.md](design.md) for the sandbox this
steps outside of, and [capabilities.md](capabilities.md) for what plugins
add. Plugin authors read [README.md](README.md#tools).

UI: Figma, Screens › **20 · Plugin tools**.

## The idea

Some plugins are best written around a program that already does the
work: pandoc for export, Kindle Previewer for KFX, a site's own CLI that
already handles its sign-in, mirrors and quotas. Today such a plugin
needs a local HTTP bridge the user installs, starts and keeps running,
with a runtime of its own (Node), a port, a login service, and settings
for all of it.

A **tool** is such a program, declared in the manifest. Dalibre gets it,
checks it, and runs it for the plugin:

- **Getting it**: Dalibre downloads a pinned build and checks its
  sha256, or uses the one a package manager (Homebrew, winget, Scoop)
  already installed, or installs it with that package manager, or uses a
  file the user chooses. The user picks at install; the default needs no
  choice.
- **Running it**: `dalibre.exec(id, args)`, only the tools declared,
  without a shell, with the system proxy passed in.
- **Trust**: a tool isn't sandboxed. It is shown as such at install, its
  build is pinned by hash, and changing it is a new permission.

Not in scope: runtimes (Node, Python, Java), installers, services that
start at login. A tool is one executable file.

## Manifest

```json
"tools": {
  "pandoc": {
    "version": "3.6.4",
    "min": "3.6",
    "homepage": "https://pandoc.org",
    "license": "GPL-2.0-or-later",
    "packages": {
      "brew": "pandoc",
      "winget": "JohnMacFarlane.Pandoc",
      "scoop": "main/pandoc"
    },
    "platforms": {
      "darwin-arm64": {
        "url": "https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-arm64-macOS.zip",
        "sha256": "…",
        "size": 32505856,
        "bin": "pandoc-3.6.4-arm64/bin/pandoc"
      },
      "windows-amd64": {
        "url": "https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-windows-x86_64.zip",
        "sha256": "…",
        "size": 31457280,
        "bin": "pandoc-3.6.4/pandoc.exe"
      }
    }
  }
}
```

| Field | |
|---|---|
| key (`pandoc`) | The tool's id within this plugin, for `dalibre.exec`. Not global: two plugins may each declare a `zlib`. Up to 4 tools. |
| `version` | **Required.** The version Dalibre downloads. Exact, never `latest`. |
| `min` | The oldest version the plugin accepts from a package manager or a chosen file. Default `version`. |
| `homepage`, `license` | Shown at install. `license` is an SPDX id. |
| `packages` | Optional. Names in `brew` (a tap's formula as `owner/tap/name`), `winget` (the package id), `scoop` (`bucket/name`). Fully qualified, so Dalibre never takes another program of the same name: homebrew-core's `zlib` is a compression library, `heartleo/tap/zlib` a CLI. |
| `platforms` | **Required.** One entry per `os-arch`, from `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`, `windows-amd64`, `windows-arm64`. A platform not listed has no download. |
| `url` | An https URL: a `.zip`, `.tar.gz`, `.tgz`, or the executable itself. |
| `sha256` | **Required.** Of the file at `url`. |
| `size` | Bytes, shown at install and checked after download. |
| `bin` | The executable's path inside the archive. Omit when `url` is the executable. |

A plugin with `tools` is checked by `dalibre-plugin check` as any other,
plus each platform's download and hash (below).

## Where a tool comes from

For each tool, the first that applies:

1. **A file the user chose.** Used as is; its version is read once
   (`--version`), and refused under `min`.
2. **A package manager's install**, when `packages` names it and it is
   at `min` or later. Found by reading the package manager's own files,
   not by running it (Homebrew alone takes a second to start, and isn't
   on a GUI app's `PATH`):
   - Homebrew: under `/opt/homebrew`, `/usr/local` and
     `/home/linuxbrew/.linuxbrew`, `Cellar/<name>/<version>/INSTALL_RECEIPT.json`,
     whose `source.tap` must match the declared tap. Run through
     `opt/<name>/bin/<bin>`, which survives `brew upgrade`.
   - Scoop: `~/scoop/apps/<name>/current/manifest.json` and its bucket.
   - winget: a portable package's folder under
     `%LOCALAPPDATA%\Microsoft\WinGet\Packages\<id>_*`; else
     `winget list --id <id> --exact` (cached), and then the program on
     `PATH`, the one case where `PATH` is read, after winget confirmed
     the package.
3. **Dalibre's download** of `version`.

`PATH` is otherwise never searched by name.

A package manager's install is used at whatever version it has, `min` or
later; there is no maximum. If it drops under `min` or disappears, the
tool falls back to the download (the plugin page says why).

## Install

What the user sees (frames 1–5):

- Each tool is the **first row** of "This plugin will be able to", in
  the warning tone, since it isn't sandboxed: "Run pandoc 3.6.4 on this
  computer". It never sits below the other permissions, however many
  there are.
- Under it, a select says where it comes from, already chosen:
  - **Installed · Homebrew** when a package manager has an accepted
    version. Nothing to download.
  - Otherwise **Download · 31 MB**, the last source the user picked
    for a plugin, or **Choose file…** when there is no build for this
    computer.
- The select's menu offers: Download with Dalibre (size), Install with
  Homebrew / winget / Scoop (only when that package manager is on this
  computer and `packages` names the tool), and Use a … I have (a file).
- Install from GitHub also shows the repository's README.md, rendered,
  beside the install details (frame 1). The Marketplace's dialog doesn't:
  its page already describes the plugin.

On Install, the plugin is installed and stays **off until its tools are
there**:

- **Download**: to a temporary file, checked against `size` and
  `sha256`, unpacked, `bin` checked to exist, made executable, moved into
  the store. A failure leaves the plugin off with Try again.
- **Package manager**: Dalibre shows the command it will run
  (`brew install pandoc`), then runs it in the background with its
  output, Stop and Close (frame 4). Close doesn't stop it; the plugin
  page shows its progress. Afterwards Dalibre finds the install as
  above. On failure: the last lines, Copy log, Try again, and Download
  with Dalibre (frame 5).
  - No password: Homebrew needs none; winget runs with `--scope user`,
    and a package that only installs machine-wide isn't offered.
  - winget asks to accept the package's agreements. Dalibre shows their
    links and passes `--accept-package-agreements` only after the user
    agrees.
- **File**: the file picker, then the version check.

Nothing runs a package manager without the user choosing it for that
tool.

## The store

Downloads live in `<plugins>/.tools/<sha256>/`, shared by every plugin
declaring the same build. A build no installed plugin declares is
removed. Uninstalling a plugin never touches a package manager's install
or a chosen file.

macOS: Dalibre's own downloads carry no quarantine attribute, so an
unsigned tool isn't blocked by Gatekeeper.

## Running

```js
const { code, stdout, stderr } = await dalibre.exec("pandoc", [
  "/work/book.epub", "-o", "/work/book.docx",
], { timeout: 120_000 });
```

`dalibre.exec(id, args, options?)` → `{ code, stdout, stderr }`:

- Only tools declared in `tools`; any other id is an error.
- `args` is an array, passed as is: **no shell**. An arg that is a path
  under `/work` or `/data` is mapped to the real folder.
- Working directory: `/work`.
- Environment: only `PATH`, `HOME` (`USERPROFILE`), `TMPDIR` (`TEMP`),
  `LANG` and `LC_*`, the system proxy as `HTTP_PROXY`, `HTTPS_PROXY`,
  `NO_PROXY` (Dalibre reads it from the OS: macOS's network settings,
  Windows' internet settings, GNOME's or the environment on Linux), and
  `options.env`.
- `options.stdin`: a string written to its input, then closed. Secrets
  go this way, never in `args` (which other processes can see).
- `options.timeout`: default 60 s, at most the call's deadline.
  `stdout` and `stderr` stop at 16 MB each.
- `options.onLine(stream, line)`: each line as it comes, for progress.
- The call's deadline, a timeout, or the user cancelling kills the
  tool's whole process group.
- `code` is its exit code; Dalibre doesn't treat non-zero as an error.

A download a tool made is a book to add: `resolve` may return
`{ path }`, a file under `/work`, instead of `{ url }`. Dalibre moves it
into the library as it does a downloaded file.

### Readiness

A tool can be present and still not usable (signed out, no LaTeX for
PDF). Plugins may export `check()`, called when the plugin loads and
when its page opens:

```js
export async function check() {
  const r = await dalibre.exec("zlib", ["profile", "--json"]);
  if (r.code !== 0) throw dalibre.error("auth", "Sign in to Z-Library");
}
```

Its error kinds give the plugin's states as elsewhere (`auth` → Needs
sign-in, `settings` → Needs setup). Missing tools are Dalibre's to
report and need no `check`.

## Plugin page

Plugins › Installed lists a plugin's tools under **Tools**, above Can
access (frame 6), each a ToolRow: the version and where it comes from
(Downloaded by Dalibre, From Homebrew and its path, Your file), with
Change, which opens the same menu as at install. States (frame 7):
downloading, installing, couldn't get it (Try again), gone (removed
outside Dalibre), no build for this computer (Choose file…).

## Updates

A tool's build is part of the plugin's version. A plugin update that
changes a tool's `url` or `sha256`, or adds a tool, asks for a new
permission and waits for the user, as more hosts do today. The new build
is downloaded when they allow it.

Tools must not update themselves. Dalibre passes the usual switches off
in their environment where it knows them; a tool that updates itself
anyway isn't listed in the Marketplace.

## Authors

```sh
dalibre-plugin tool add brew:heartleo/tap/zlib   # from a Homebrew formula
dalibre-plugin tool add gh:jgm/pandoc@3.6.4      # from a GitHub release
dalibre-plugin check .
```

`tool add` fills `tools.<name>`: `version`, `packages`, and `platforms`
from the formula's per-platform `url` and `sha256`, or from the
release's assets and `checksums.txt`, matching goreleaser and
cargo-dist names (`_darwin_arm64`, `_x86_64`, `-aarch64-apple-darwin`,
…). The values are fixed when the plugin is written. Dalibre never reads
a formula or a release at runtime, or the tap's owner could change the
program without a plugin update.

`check` downloads every platform's file, verifies `size`, `sha256` and
`bin`, and runs this platform's with `--version`. The Marketplace's
pull-request check does the same.

## Code

- `internal/plugin/tools.go`: manifest fields, platform key, resolve
  order, the store.
- `internal/plugin/pkgmgr/`: Homebrew, Scoop and winget lookups and
  installs.
- `internal/plugin/exec.go`: `dalibre.exec`, path mapping, environment,
  limits, process groups.
- `internal/sysproxy/`: the OS proxy, also for the plugin HTTP client
  (today it reads only the environment, which a GUI app on macOS doesn't
  get).
- `cmd/dalibre-plugin`: `tool add`, `check`.

## Open questions

- **Mirroring.** Copy Marketplace plugins' tool builds to the
  `marketplace` branch, so a deleted or replaced release asset doesn't
  break installs?
- **Linux distro packages** (apt, dnf, pacman): names and versions vary
  too much to declare; a chosen file covers them for now.
- **Long-running tools.** `dalibre.mcp(id, args)`: a tool started as an
  MCP stdio server, kept while Dalibre runs, from the same `tools`
  entry. For CLIs that already ship one (`zlib mcp`).
