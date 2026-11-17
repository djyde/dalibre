# Plugin system: design

For Dalibre's developers. Plugin authors read [README.md](README.md),
which is the contract.

Status: plugin API 1, book sources. Engine, consent, packaging, author
CLI, library import, the Get books page, the Plugins dialog and the
built-in Project Gutenberg plugin are done and tested. What comes next is
in [capabilities.md](capabilities.md).

## Goals

1. **Anyone can write one.** A text editor and one CLI; no toolchain,
   no build step, the same on Windows, macOS and Linux.
2. **Installing one is safe.** Plugins are untrusted code. A plugin
   reaches only the hosts the user granted, never the disk, other
   processes or the local network (unless granted), and can't hang or
   crash the app.
3. **A stable contract.** Plugins written today keep working; mistakes
   in them are reported precisely to their author and degrade gracefully
   for users.
4. Dalibre stays one binary with no cgo.

## Runtime

JavaScript (ES2023, ES modules) in
[moejs](https://github.com/MoeForge/moejs), a JavaScript engine written in
Go: no cgo and no C transpiled to Go. One runtime per plugin, on its own
event loop.

Plugins get standard APIs rather than Dalibre's own: the engine's
builtins, and its `webapi`, `eventloop` and `node` packages: `fetch` and
the Streams, `URL`, timers, `node:fs` over the plugin's data folder,
`Buffer`. Dalibre adds only what nothing standard covers: `dalibre.settings`,
`dalibre.error(kind)`, `dalibre.plugin`, `dalibre.api`. Each standard API
is fenced by Go: `fetch` goes through the plugin's sandboxed
`http.Client` and an allow hook, `node:fs` through an `os.Root`.
TypeScript modules run as they are (`CompileTS` erases the types).

| Option | Why not |
|---|---|
| modernc.org/quickjs (QuickJS transpiled to Go) | The engine before moejs. A memory bug in transpiled C corrupts our process; its VMs held memory outside Go's heap until closed. |
| goja (pure-Go JS) | The first version used it. No memory limit: a one-line plugin grew the heap to 1.2 GB in 3 s, stoppable only by timeout. Incomplete ES2015+, no ES modules. |
| QuickJS via cgo (`buke/quickjs-go`) | cgo breaks the one-binary, no-cgo build. |
| QuickJS via wasm (`fastschema/qjs`) | v0.0.6, untouched since 2025. |
| Go `plugin` | No Windows; same toolchain and cgo required. |
| Subprocess + JSON-RPC | Users must install a runtime or authors ship binaries per OS; a process has the whole disk and network. |
| WebAssembly (any language) | Every author needs a compiler and a build; too much for "call an API, map JSON". |

Against modernc.org/quickjs on macOS (arm64): the app binary grows by
4.3 MB (40.2 → 44.6 MB). moejs's own benchmarks put a new runtime at
1.4 µs and a call at about a fifth of QuickJS-in-Go's. Decrypting a 5 MB
Adept-style EPUB in a plugin with pure-JS libraries (`node-forge` for
RSA, `@noble/ciphers` for AES, `fflate` for zip) takes about 0.7 s.

Each runtime has its own mutable copy of the builtins
(`MutableIntrinsics`), so a bundled polyfill that patches a prototype
works as in a browser; `self` is the global object, as in a Web Worker.
Memory isn't limited, as calibre doesn't limit its plugins': a file type
plugin works on whole books, and a limit only turned large ones away.

### The host boundary

Besides the standard APIs, the Go side and the script share three host
functions that take and return strings:

| | |
|---|---|
| `__host_info()` | the API version, plugin id/version and settings, as JSON |
| `__host_log(level, text)` | logs, masking secrets |
| `__host_settle(json)` | the outcome of the call in progress; stops the loop |
| `__host_html(html)` | the tokens of `dalibre.html.tokens`, as JSON |

`internal/plugin/prelude.js` runs first in every runtime: it takes those
functions off the global object, builds the frozen `dalibre` object and
`console` on them, and defines `__dalibre_load` and `__dalibre_call`
(non-writable), which import the plugin and call its exports, reporting
each outcome (a value, or a thrown error with its `kind`) through
`__host_settle`. The prelude's value hands Go the same two functions.
Go calls one inside `Loop.Run`, which runs timers, fetches and file
operations until `__host_settle` stops it, or until nothing is left that
could settle the call ("never settled"). Arguments and outcomes cross as
JSON, so neither side holds the other's objects. A `fetch` the script
doesn't catch is reported with the kind its cause says (`permission`
for the allow hook's refusal, `unavailable` for the network).

Modules load through a Go resolver: only relative `.js`/`.mjs`/`.ts`/
`.mts` paths inside the plugin's folder (`./lib.js` finds `lib.ts`, as in
TypeScript), 4 MB in all, each compiled once per runtime; and
`node:fs`, `node:fs/promises`, `node:buffer` from moejs.

## Trust model

The plugin author is untrusted; the user decides what a plugin may do,
once, at install.

| Threat | Defence |
|---|---|
| Read or write the user's files | `node:fs` reaches only the plugin's own folder (`<plugins>/.data/<id>`), through an `os.Root`, so neither `..` nor a link leaves it, and in it only `/data` (writes stop at 64 MB) and `/work`. A book's file reaches a file type plugin as a copy in `/work`, emptied after each call; what it returns is checked to be in `/work` or `/data` and moved out by the host, which alone writes the library. No process or other Node modules. Downloads are written by the host, to a temp folder, under a sanitized name. |
| Reach hosts it didn't declare | Every request, redirect, download and cover URL is checked against the granted `permissions.hosts` (for `fetch`, in its allow hook, at every hop). Only http(s); no credentials in URLs. The one exception is `permissions.covers: "any"`, granted at install: covers from any https host, fetched by the host without the plugin's credentials, images only, still never local addresses. |
| Reach the user's machine or LAN (router admin, local services) | Without `localNetwork`, the dialer refuses loopback, private, link-local, CGNAT, multicast and unspecified addresses **after** DNS resolution, so a listed host resolving to 127.0.0.1 (DNS rebinding) fails too. |
| Exfiltrate other plugins' secrets | Each plugin has its own runtime, settings, cookies and data folder. |
| Hang or overload the app | Per-call deadline (60 s, 10 min for `process`; interrupts the script), 50 requests per call, 16 MB per fetch body, 64 MB of data, stack depth cap, 4 MB of scripts, results capped at 100. Calls into one plugin are serialized; the call's deadline also ends its fetches and wakes a loop waiting on a timer. A runtime that ran out of time is discarded and the plugin loaded afresh on its next call. |
| Huge or hostile files | Downloads capped at 2 GB and streamed; covers 10 MB; packages 32 MB / 1000 files, zip-slip and non-regular entries refused. |
| Change the library | A plugin never writes it. Hooks return the changes they want, which the host checks and writes, and only with `permissions.library: "write"`, shown as a warning at install. |
| Run programs | Only the `tools` its manifest declares, each a build pinned by SHA-256 (or a package manager's install of the named package, or a file the user chose), shown at install as unsandboxed; through `dalibre.exec`, without a shell, with a minimal environment, and killed with its process group at the call's end. A changed build is a new permission. See [tools.md](tools.md). |
| Gain permissions silently in an update | Granted permissions are stored per plugin; a version asking for more stays unloaded until the user grants again. |
| Deface the host API | `dalibre`, its settings and info are frozen. |
| Leak secrets through logs | Values of `secret` settings are masked in plugin logs. |

### Known gaps

- **Engine bugs.** A bug in moejs could let a plugin reach past what the
  host gave it. It is Go, so it can't corrupt memory, and a panic inside
  it comes back as an error that drops the runtime. Keep the dependency
  current.
- **No memory limit.** A plugin that allocates without end takes the
  computer's memory until the system stops Dalibre, as a calibre plugin
  can. Accepted, as calibre does: a limit turned large books away, and
  the deadline still ends a plugin that loops.
- **The data quota is checked before each write**, from what the folder
  holds, so one write can pass 64 MB by its own size (at most the memory
  limit, which bounds what the script can hand over).
- **CPU in native code.** Interrupts are checked between JS
  instructions; a single huge native call (a giant regex, `JSON.parse` of
  16 MB) can overrun the deadline briefly.
- **Proxies.** When the user has an HTTP(S) proxy in the environment, the
  proxy is dialed as is and resolves names itself, so the local-network
  check is up to the proxy. Common for users in China (local proxy on
  127.0.0.1); not worth breaking.
- **Secrets at rest** live in `settings.json` in plain text. Should move
  to the OS keychain.
- **No signing.** A package's integrity is whatever its download channel
  gives. Signing makes sense together with a plugin index.

## Lifecycle

```
               Inspect(package)          user grants
 .dalibre-plugin ───────────▶ consent? ───────────────▶ Install → <plugins>/<id>/
                                 │ already covered                  │
                                 └──────────────────────────────────┤
 startup / settings change                                          ▼
 Discover(<plugins>) ── for each: disabled? granted ⊇ asked? ── Load → ready
                                       │ no
                                       └▶ pluginsPending (UI asks; askConsent → grantConsent)
```

State per plugin id, in `settings.json`:

```json
"plugins": {
  "gutenberg": { "disabled": false, "granted": { "hosts": ["gutendex.com", "…"] }, "values": { "languages": "en" } }
}
```

The plugins folder is `<config>/Dalibre/plugins` (`%AppData%` on
Windows), or `$DALIBRE_PLUGINS`. Install unpacks into a hidden temp
folder inside it, then swaps it in by rename, so a failed install leaves
the old version.

## Calls

Arguments and results cross between Go and JS as JSON only. `call` runs
the export on the plugin's event loop under the deadline, until the
export's promise settles. A loop that runs out of work first means the
script awaited something that can never settle: an error. Timers left
when a call settles stay for the next call.

What a plugin returns is decoded strictly and checked field by field
(`source.go`):

- the page shape wrong → the call fails (`invalid`), naming what was
  returned and what was expected;
- one result wrong → it's dropped with a warning naming the item and
  field (`items[3].formats[0].format: "EPUBB" isn't one of …`);
- an optional field wrong or unknown → ignored, with a warning.

Warnings go to the log and `dalibre-plugin`; users never see them.

### Errors

Every failure is a `*plugin.Error` with a `Kind` the UI acts on:

| Kind | From | UI |
|---|---|---|
| `auth` | thrown; HTTP 401/403 on download | offer the plugin's settings |
| `settings` | a `required` setting is empty (checked before calling) | offer the plugin's settings |
| `rateLimited` | thrown; 429 | try later |
| `unavailable` | thrown; network failure; 5xx | try later |
| `notFound` | thrown; 404/410; format not offered | say so |
| `invalid` | thrown; malformed return | "the plugin failed", details in log |
| `timeout` | deadline or cancel | say so |
| `permission` | a refused request | "the plugin failed", details in log |
| `script` | any other exception | "the plugin failed", stack in log/CLI |

## Download into the library

`App.downloadBook(plugin, result, format)`:

1. `Resolve`: the format's URL, or the plugin's `resolve`.
2. Host downloads into a temp folder (progress to the UI, cancel).
3. Host fetches the cover, if any (failure ignored).
4. `Library.ImportWith(file, metadata, cover)`: the plugin's metadata
   wins, the file fills gaps; identifiers merged; the file's cover first.
5. Temp folder removed, library reloaded, new book selected.

## Code

| | |
|---|---|
| `internal/plugin/manifest.go` | manifest, validation, permissions, consent check |
| `internal/plugin/plugin.go` | the runtime: load, discover, modules, calls, recovery, error mapping |
| `internal/plugin/prelude.js` | the script side of the host boundary: `dalibre`, `console`, calling exports |
| `internal/plugin/host.go` | host functions, the network sandbox (client, dialer, URL checks), error kinds |
| `internal/plugin/web.go` | `fetch`'s client and allow hook: hosts, request count, user agent, credentials, body cap |
| `internal/plugin/files.go` | the plugin's own folder (`/data`, `/work`), its quota, file settings |
| `internal/plugin/process.go` | the file type contract: `process`, the copy in `/work`, checking and moving out what it returns |
| `internal/app/filetype.go` | file type plugins on import: the chain every added file goes through |
| `internal/app/runplugin.go` | running a file type plugin from the book menu, writing its files with ORIGINAL_ kept, Undo |
| `internal/plugin/hooks.go` | file type hooks: `postimport`, `postadd`, `postdelete`, their events and the changes they return |
| `internal/plugin/source.go` | the book source contract: search, list, collections, get, checking, resolve, download, cover |
| `internal/plugin/install.go` | inspect, install, uninstall, pack |
| `internal/app/plugins.go` | loading (built-in and installed), consent, enable/disable, settings, `downloadBook` |
| `internal/app/getbooks.go`, `view_getbooks.go`, `view_plugins.go` | the Get books page, the Plugins and permission dialogs |
| `internal/settings` | per-plugin state |
| `internal/calibre/importer.go` | `ImportWith` |
| `cmd/dalibre-plugin` | the author CLI |
| `sdk/` | `dalibre.d.ts`, `plugin.schema.json`, the `init` template (embedded) |
| `plugins/` | built-in plugins, embedded in the app: `gutenberg`, `neodb`, `googlebooks` |

## UI

Designed in Figma first (Screens › 05–07), built in `internal/app`:

1. **Get books** (sidebar): one page with a source picker (each book
   source, and All sources), search, the source's filters, browse
   shelves from its collections, results with covers and formats, the
   detail panel filled by `get`, download per format. "In library" by
   identifier.
2. **Downloads** in the jobs popover: progress,
   cancel, retry, show in library; error kinds turned into what to do.
3. **Plugins** dialog (from the picker's "Get more sources…" and the
   source's settings button): every plugin found (built in, on, off,
   waiting for permission), its switch, settings form with masked
   secrets, what it adds and can reach, uninstall, "Install from file…",
   "Open plugins folder", "Reload plugins".
4. **Permission dialog**: name, author, description, and the
   permissions in plain words, the local network called out; for an
   update, only what is new.

Built-in plugins (`plugins/`, `dalibre.*` ids) load from the binary and
have what they ask for: Dalibre vouches for them. They can be turned off
but not uninstalled; a package or a folder using a `dalibre.` id is
refused.

## Later

- Install from a URL; check for updates (manifest `updateUrl`).
- A plugin index (a JSON file in a repo) to browse and install from,
  with signed packages.
- Secrets in the OS keychain.
- HTML parsing (`DOMParser` or CSS selectors) for sites without an API.
- `crypto.subtle` and `CompressionStream`, if plugins need more than a
  bundled library gives; `FileHandle` in `node:fs` for large files.
- Login flows (open the browser, receive a callback) for OAuth sources.
- Duplicate check by title and author too, not only by identifier.
- More capabilities: `metadata` (fetch metadata and covers for existing
  books, as calibre's metadata sources), `opds` (expose a source as OPDS),
  event hooks.
- Tools: programs a plugin runs outside the sandbox (pandoc, a site's
  CLI), pinned by hash and fetched or found by Dalibre; see
  [tools.md](tools.md). A wasm runtime for native code that can stay
  sandboxed.
- A `dalibre-plugin test` harness with recorded HTTP fixtures, so
  authors can run their plugin in CI.

## Open questions

- **Distribution and liability.** Do we run a plugin index? If so, what
  do we accept (public-domain and licensed sources only?), and who
  reviews? Without one, users find packages on their own and Dalibre
  stays out of what they download.
- **Deprecation policy** for API 1 once API 2 exists: how long, and how
  do we tell users.
- **Metadata precedence**: the plugin's metadata wins over the file's.
  Right for clean catalogues; should a plugin be able to say otherwise?
- **Fan-out search**: search one source at a time, or all at once with
  results grouped by source?
- **Where `dalibre-plugin` ships**: in the app bundle, as a separate
  download, or as `dalibre plugin …` subcommands of the app binary.
