# Plugin capabilities

Status: rollout steps 1–4, 6 and 7 are built (the Get books page, the
book source capability with filters, collections, `get`, and the built-in
Project Gutenberg plugin; a data folder and file settings; the metadata
capability, `permissions.library` and the built-in NeoDB plugin; the
file type capability; actions); the rest is a proposal. Extends plugin API 1
without breaking it; see [design.md](design.md) for the engine, sandbox
and trust model, which this keeps as they are. Plugin authors read
[README.md](README.md).

UI: Figma, Screens › **07 · Get books** (supersedes the per-source
pages in 05) and the Plugins dialog there.

## The idea

A plugin is one package that can **add several capabilities** to
Dalibre. Get books is the first; metadata, library actions and others
follow on the same model.

Obsidian is the model for the package (a manifest, one script, settings,
community list) and the opposite for the UI: an Obsidian plugin gets the
whole app and the DOM. A Dalibre plugin is untrusted code in a sandbox, and
the app draws natively, so:

| Obsidian | Dalibre |
|---|---|
| `manifest.json` + `main.js` | `plugin.json` + `main.js` (as now) |
| `class extends Plugin { onload() { this.addCommand(…) } }`: capabilities registered in code | Capabilities **declared** in `plugin.json`, implemented by exports. Dalibre knows what a plugin adds, and asks the right permissions, before running any of its code |
| Plugins draw views, modals, settings tabs | Plugins **never draw**. They return data; Dalibre's own screens show it (the Get books page, the detail panel, menus, the settings form) |
| `loadData` / `saveData` | `node:fs` over the plugin's own data folder (`/data`) |
| Full access to the vault and the network | Hosts and library access granted per plugin, at install |
| Restricted mode; community list in a GitHub repo | Same: plugins off until the user turns them on; an index repo; install from file always works |
| `minAppVersion` | `api` (as now) |

Why declarative: every capability's UI is the same for all plugins, so
users learn it once; a plugin can't fake a dialog or a sign-in page; the
app can lay out the source picker, filters and settings without loading
the script; and the UI can change without breaking plugins.

## Manifest

`capabilities` becomes an object; the array form (`["source"]`) stays
valid in API 1 and means `{ "bookSource": {} }`.

```json
{
  "id": "gutenberg",
  "name": "Project Gutenberg",
  "version": "0.3.0",
  "api": 1,
  "icon": "globe",
  "description": "70,000 public-domain books",
  "capabilities": {
    "bookSource": {
      "filters": [
        { "key": "language", "title": "Language", "type": "language" },
        { "key": "sort", "title": "Sort", "type": "select",
          "options": [{ "value": "popular", "title": "Most popular" },
                      { "value": "new", "title": "Recently added" }] }
      ],
      "collections": true
    },
    "metadata": { "fields": ["title", "authors", "tags", "cover", "description"] }
  },
  "permissions": {
    "hosts": ["gutendex.com", "www.gutenberg.org", "gutenberg.org"],
    "library": "read"
  },
  "settings": [ … ]
}
```

- `icon`: a name from Dalibre's icon set (Lucide subset), never an image:
  a plugin can't dress up as another app.
- `description`: one line, shown under the name in the source picker.
- The manifest holds what Dalibre must know before it runs the plugin:
  who it is, what it may reach, what it adds to which menus, when to
  wake it. What is only needed once a call is under way comes from the
  call: an action's questions (`dalibre.form`), a reader's formats
  (`connect()`), a source's filters (`filters()`, below).
- Filters are typed (`select` and `language` today; `text` and
  `toggle` when a source needs them) so Dalibre draws them with its own
  controls. In *All sources* only the language filter shows, shared by
  every source that has one. While browsing, only the language shows: a
  collection keeps its own order.

## Capability: Book source (Get books)

The user picks a source on the Get books page, types, browses,
opens a result, downloads. The plugin supplies data for each step:

| Export | Called when | Returns | Required |
|---|---|---|---|
| `search(query)` | the user searches (and "Show more") | `Page` | yes |
| `list(query)` | the search box is empty: the Browse page, a collection's "See all" | `Page` | with `collections` |
| `collections()` | the Browse page opens | `Collection[]` (once per session, cached) | with `collections` |
| `filters()` | the source is picked (proposal) | the filters, as the manifest's (once per session, cached) | no; else the manifest's `filters` |
| `get(item)` | a result is opened in the detail panel | a fuller `Result` | no; else the search result is shown as is |
| `resolve(item, format)` | Download | `DownloadRequest` | no; else `format.url` |

`download` stays with the host, as now: Dalibre streams the file with
progress and cancel, checks it's a book, and imports it. The plugin only
says **where** the file is (`resolve`). That keeps files off the
plugin's side of the sandbox and gives every source the same download
behaviour.

```ts
interface Query {
  text: string;            // as now
  page: number;            // as now
  cursor: string;          // as now
  filters: Record<string, string>; // new: values of the declared filters
  collection?: string;     // new, list() only
}

interface Collection { id: string; title: string }   // "Popular this week"

interface Result {          // as now, plus:
  url?: string;             // the book's page on the site: "Open site"
  fields?: { label: string; value: string }[]; // extra detail rows: "Rights", "Downloads"
}
```

Dalibre, not the plugin, decides:

- **In library**: a result whose identifiers (the plugin's own id,
  ISBN) match a book in the library shows "In library" instead of Get.
- **All sources**: calls `search` on every enabled source in parallel
  (calls into one plugin are still serialized), shows each source's
  first 3 results under its header as they arrive. One source failing
  or needing setup is a state of its
  group, never of the page. This answers the open question in
  design.md: one source by default, all at once when asked.
- **States** from error kinds (unchanged): `auth`/`settings` → Needs
  setup; `rateLimited`/`unavailable` → banner + Retry; the rest → "the
  plugin failed".

`filters()` lets a source take its filters from the site (its
languages, its categories) instead of writing them down; `filters` in
the manifest stays valid, and is what shows until `filters()` answers.

Compatibility: an API 1 source plugin today is a book source plugin with
no filters and no collections, and works unchanged.

## Capability: Metadata

calibre's metadata sources. Shows up as **Fetch metadata** in the Edit
metadata dialog (Figma, Screens › 04 · Edit metadata, "Fetch metadata").

| Export | Returns |
|---|---|
| `identify(book)` | candidates, best first, at most 20: `Result`s without `formats` |

`book` is only what the user asked to look up: `{ title, authors,
identifiers, language? }`, or what they typed in the step's search box
(a title, or an ISBN as `identifiers.isbn`). The candidate's `cover` is
an image URL on the plugin's hosts, fetched by Dalibre; there is no
separate `cover` export. Dalibre shows the candidates next to the
current values, one checkbox per field (on for fields that differ and
the candidate fills), merges identifiers, and fills the form with what
the user picks; only Save writes the library. Needs
`permissions.library: "read"`. The fields a source fills are those its
candidates carry: `fields` in the manifest is still accepted and no
longer means anything.

With several metadata plugins, the step searches all of them at once, or
the one picked in its source menu; candidates are listed by plugin, then
rank.

Built in: **NeoDB** (`dalibre.neodb`, `plugins/neodb`): neodb.social's
catalog API, no account. By NeoDB id or ISBN when the book has one
(an ISBN search keeps only that edition), else by title and first
author, ranked by title, then authors and language. Candidates carry
NeoDB, Douban, Goodreads, Open Library and Google Books ids.

Built in: **Google Books** (`dalibre.googlebooks`, `plugins/googlebooks`):
the Books API, with the user's own API key as an optional `secret`
setting. By Google id or ISBN when the book has one, else by title and
first author (fielded, then free text), ranked as NeoDB. Without a key,
Google's shared quota running out is an `auth` error, so the app
offers the settings.

## Capability: File type

calibre's file type plugins. The plugin exports `process(file)`, gets a
copy of a book's file in `/work` and returns the path of a new one, or
`null`. Every file Dalibre adds goes through the file type plugins, one
after the other (unless `"onImport": false`), and a failure adds the
file as it was. From the book menu (Figma, 16 · Run plugin on books) the
plugin's `title` runs at once on the books, in the background with the
sidebar's progress; the old file is kept as `ORIGINAL_<format>` and a
toast offers Undo. Choosing the action is the whole decision: no dialog.
A `file` setting gives the plugin a file the user chooses, such as a key.

Hooks, as calibre's: `onPostImport`, `onPostAdd` and `onPostDelete` call
`postimport`, `postadd` and `postdelete` after a file is added, a book is
added and a format is removed, with the book's details (they need
`permissions.library: "read"`). Where calibre's hooks get the database,
ours return `{ metadata }` changes, which Dalibre writes with
`permissions.library: "write"`. calibre's conversion hooks come with
the Convert capability.

## Capability: Actions

calibre's interface action plugins, without the interface: Find
Duplicates, Quality Check, EpubMerge, EpubSplit, Count Pages. An action
is something the user runs on books, or on the whole library; the plugin
returns what it found and what it wants changed, and Dalibre shows and
applies it.

```json
"actions": [
  { "id": "find", "title": "Find duplicates…", "icon": "copy", "scope": "library" },
  { "id": "exempt", "title": "Not duplicates", "scope": "books",
    "books": { "min": 2 }, "in": "results" },
  { "id": "merge", "title": "Merge into one book", "scope": "books",
    "books": { "min": 2 }, "formats": ["EPUB"], "files": true }
]
```

- `scope`: `"books"` runs on the selection, from the book menu and the
  selection bar's plugin button, which opens a menu of every plugin's
  actions; `"library"` runs on every book, from the sidebar's **Tools**,
  where each is listed by its title.
- `icon`: a name from Dalibre's icon set, as the plugin's own.
- `books`: how many books it takes (`min`, `max`); `formats`: it shows
  only for books that have one of them. Dalibre greys the item out
  without running any code.
- `files`: the action gets a copy of each book's file in `/work`, as
  `process` does. Only with `"scope": "books"`.
- `in: "results"`: only in the menu of this plugin's own results (below).
- `title` ends in `…` when the action asks something first.

| Export | Called when | Returns |
|---|---|---|
| `run(action, input)` | the user runs the action | `ActionResult` or `null` |

An action that needs to ask the user calls `dalibre.form` when it does,
and Dalibre draws the dialog. Nothing about it is in the manifest:

```js
export async function run(action, input) {
  const { by, loose } = await dalibre.form({
    submit: "Find duplicates",
    fields: [
      { key: "by", title: "Compare", type: "select", value: "title",
        options: [{ value: "title", title: "Title and author" },
                  { value: "isbn", title: "ISBN" }] },
      { key: "loose", title: "Ignore case and punctuation", type: "toggle", value: true },
    ],
  });
  …
}
```

- Fields are of the setting types, plus `multiselect`; `value` is what
  the field starts with. The answers come back by `key`, typed: a
  toggle's is a boolean, a multiselect's an array.
- The dialog's title is the action's and its tile the plugin's, from the
  manifest: a plugin can't dress its dialog up as another's. `submit`
  names the button; Cancel is Dalibre's, and makes `form` throw
  `dalibre.error("cancelled")`, which ends the run quietly.
- It can be called when the answer is known to be needed and with what
  the plugin has found by then (EpubSplit reads the book, then lists its
  parts), and more than once. No call, no dialog: choosing the action is
  the whole decision, as for file type plugins.
- The run's 10 minutes don't count while a form is open.
- Without a window (an AI agent over MCP, `dalibre-plugin`), the caller
  gives the answers by key and `form` returns them at once; one it wasn't
  given makes the run fail with the fields it asked for. The plugin's
  code is the same. `dalibre.interactive` says which it is.

```ts
interface ActionInput {
  books: BookInfo[];                 // the selection, in the user's order; empty for "library"
  files?: Record<number, HookFile>;  // by book id, with "files"
}

interface ActionResult {
  message?: string;        // the toast: "12 duplicate groups"
  groups?: Group[];        // books to show
  changes?: { book: number; metadata: Changes }[];
  add?: { path: string; metadata?: Changes }[];           // new books
  formats?: { book: number; path: string }[];             // new formats
  open?: string;           // a page on the plugin's hosts
}

interface Group { title: string; books: { id: number; note?: string }[] }
```

A `"library"` action reads the books itself, so that a library of 50,000
isn't one argument:

```js
for await (const book of dalibre.library.books()) { … }   // BookInfo, without comments
dalibre.progress(done, total);                            // the job's bar
```

What Dalibre does with the result:

- **`groups`** open as a results view: the book list, showing only these
  books under each group's title, with each book's `note` in its row,
  and the action's title as the page's. It is a book list
  like any other, so delete, edit metadata and every plugin action work
  in it. The plugin's `in: "results"` actions are buttons in the
  selection bar, two at most and the rest in the plugin menu, and **Run
  again…** runs the action again. Find
  Duplicates' groups are its sets of duplicates, Quality Check's its
  problems ("No cover", "Title ends in a space"). Closing the view
  forgets it.
- **`changes`** are applied with `permissions.library: "write"`, with
  Undo in the toast; without it Dalibre shows them first (the fields,
  old and new, per book) and the user applies them.
- **`add`** adds new books, through the import as any file (EpubMerge's
  merged book, EpubSplit's parts); **`formats`** adds a format to a book,
  keeping one it replaces as `ORIGINAL_<format>`. The books run on stay
  as they were: an action never deletes.
- **`open`** opens in the browser, when the host is one of the plugin's.
- A book shows once in the results, under the first group that has it;
  what later groups say of it joins its note.

A run is a job: in the background, with progress and Cancel, 10 minutes.
`process` stays one file in, one out; several in or out is an action.

Reading List is a feature of the app (lists kept in the library), not a
plugin. Goodreads Sync is an action with `changes`, once sign-in flows
exist.

UI: Figma, Screens › **17 · Plugin actions**.

## Capability: Convert (proposal)

calibre's input and output format plugins: KFX Input, KePub Output.
Dalibre has no conversion of its own; this capability is all of it, and
Dalibre's own converters ship as built-in plugins.

```json
"convert": [
  { "from": ["KFX", "KFX-ZIP", "AZW8"], "to": ["EPUB"] },
  { "from": ["EPUB"], "to": ["KEPUB"] }
]
```

| Export | Returns |
|---|---|
| `convert(file, to)` | the new file's path in `/work`, or `{ path, report }` |

`file` is as `process`'s (`path`, `name`, `format`, `book`), with
`reason: "convert"`. The limits are `process`'s: the whole file, 10
minutes.

A converter with something to ask calls `dalibre.form`, as an action
does (Hyphenate, for KEPUB). Converting several books asks once: the
answers are given to the calls that follow. Where nobody chose to
convert (on the way to a device, as files are added), `form` opens no
dialog and returns the user's last answers to it, or the fields' own
`value`s.

- **Routes.** Dalibre knows from the manifests which formats a book can
  become. Where no plugin converts directly it goes through EPUB, in two
  steps at most (KFX → EPUB → KEPUB), as calibre goes through OEB. Two
  plugins for one route: the user's choice in Settings › Plugins, the
  built-in one first.
- **Where it shows.** Book menu and a format's menu › **Convert to**,
  listing the formats the book can become. Sending to a device converts
  on the way when the device takes none of the book's formats. `"onImport": true` on a route converts
  files of its `from` formats as they are added (KFX in, EPUB kept
  beside it), which the user turns on per plugin.
- **The result** is a new format of the same book; a format it replaces
  is kept as `ORIGINAL_<format>`. Metadata and cover are the book's, set
  by Dalibre after the plugin returns.
- **File type plugins** run on converted files with `"onConvert": true`
  (`reason: "convert"`), as calibre's `on_postprocess`.
  `on_preprocess` has no counterpart yet.
- Over MCP: `convert_book`.

A converter is the heaviest thing a plugin does. One needing native code
(KFX Output needs Kindle Previewer) declares it as a tool; see
[tools.md](tools.md).

UI to draw (Figma): Convert to in the book menu, the route choice in
Settings › Plugins.

## Capability: Device (proposal)

calibre's device drivers: KoboTouchExtended, Kindle Collections,
Annotations. Today Dalibre knows one kind of device, a reader connected
over Wi‑Fi (`internal/smartdevice`). This adds readers that mount as a
disk, which the app finds and a plugin understands.

```json
"device": {
  "title": "Kobo",
  "match": { "files": [".kobo/KoboReader.sqlite"] }
}
```

- `match`: Dalibre watches for mounted volumes and gives one to the
  plugin when all of `files` exist on it. Decided without running the
  plugin, which never sees another volume. The volume is the plugin's
  `/device` folder while a call runs.

What the reader can do isn't in the manifest: `connect()` says, once the
plugin has seen the reader, since it depends on the model and its
firmware.

- `formats`, best first: Dalibre sends the first one the book has, or
  converts to the first one it can.
- `collections`, `annotations`: the reader has them.

| Export | Called when | Returns |
|---|---|---|
| `connect()` | the volume appears | `{ id, name, model?, free?, formats, collections?, annotations? }` |
| `books()` | after `connect`, and after sending | `{ path, title, authors, uuid?, identifiers? }[]`: the **On device** mark |
| `send(book, file)` | Send to device | `{ path }`, having written the file under `/device` and whatever the reader's own database needs |
| `remove(item)` | Remove from device | |
| `annotations()` | on connect, when `connect()` said `annotations` | `Annotation[]` |

```ts
interface Annotation {
  book: { uuid?: string; path?: string; identifiers?: Record<string, string> };
  text: string;            // the highlight
  note?: string;
  chapter?: string;
  location?: string;       // the reader's own, kept to tell annotations apart
  created?: string;
  color?: string;
}
```

- `send`'s `book` is a `BookInfo` plus `collections`, when `connect()`
  said `collections`: the names the user's rule gives the book (its tags,
  its series, or a column; one setting per device). The plugin writes
  them the reader's way: Kobo's shelves in its database.
- **Annotations** are matched to books by Dalibre and kept in the
  library's `annotations` table, as calibre's, so both apps show them;
  the book's detail lists them. Fetching again never duplicates
  (`location`), and never removes what the user deleted on the reader.
- The reader's database is SQLite, so device plugins get `node:sqlite`
  over files in `/device`.
- A device shows where the wireless reader does: the Sharing button,
  Send to device and On device in the book menu, Eject.
- `permissions.device: true`. Consent: "Can read and change a Kobo you
  connect".

A device that isn't a disk is a `"remote"` one: only `send`, which
returns an upload for Dalibre to make (as `resolve` returns a download),
for Send to Kindle and the like.

Not covered: readers that connect over MTP, as Kindles since 2024, until
Dalibre speaks MTP; so Kindle Collections has nowhere to run on current
Kindles. Annotations from reading apps on the computer are a metadata
matter, not a device's.

UI to draw (Figma): a connected device in the Sharing popover, the
collections rule, annotations in the book's detail.

## Later capabilities

| Capability | Where it shows | Plugin returns |
|---|---|---|
| `hooks` | none; runs when a book's details change or it is removed (adding books and formats is the file type hooks') | `changes`, as actions |

A plugin never writes the library itself. It returns what it wants
changed; Dalibre shows and applies it. `permissions.library: "write"`
only allows *proposing* changes without a confirmation per book.

## Settings and storage

- Setting types add `select`, `toggle`, `number`, `url` to `text` and
  `secret`. Still a form Dalibre draws.
- ✅ A data folder per plugin, through the standard `node:fs` at `/data`
  (64 MB), for tokens and caches that survive restarts. Secrets move to
  the OS keychain together with `secret` settings.
- Sign-in flows (open the browser, receive a callback) stay in Later.

## Permissions and consent

`permissions` grows two keys:

| | Means | Consent dialog says |
|---|---|---|
| `hosts` | as now | "Can connect to gutendex.com, …" |
| `localNetwork` | as now | warning tone |
| `library: "read"` | sees the books it is asked about (Metadata, Actions) | "Your library: title, authors, identifiers — only for books you ask it to look up" |
| `library: "write"` | may change books without asking each time | warning tone |
| a `"library"` action | sees every book's details when the user runs it | "Your whole library's details, when you run Find duplicates" |
| `device` | reads and writes a reader it recognises | "Can read and change a Kobo you connect" |
| a `tools` entry | runs that program, unsandboxed ([tools.md](tools.md)) | first row, warning tone: "Run pandoc 3.6.4 on this computer", with where it comes from |

Capabilities are listed in the dialog ("Adds to Dalibre: Get books,
Metadata") so the user sees what installing changes. An update that adds
a capability needing a new permission stays off until granted, as for
hosts today.

## Distribution

As Obsidian's community plugins, minus the trust it implies:

1. Restricted by default: Settings › Plugins has "Turn on plugins" with
   one paragraph on what plugins can do; nothing loads before it.
2. **Get more sources…** (source picker) and Settings › Plugins ›
   Browse read an index: a JSON file in a GitHub repo listing
   `{ id, name, description, repo }`; packages are the repo's release
   assets (`.dalibre-plugin`), checked against a hash in the index.
   Being listed means "reviewed for honesty of its manifest", not
   "endorsed".
3. Install from file stays, and needs no index.
4. Updates: the index lists versions; Dalibre checks on start, at most
   daily.

What the index accepts is still the open question in design.md
(public-domain and licensed sources only?), now with a concrete place to
enforce it.

## Rollout

1. ✅ **Get books page** (Figma 07 · Get books): fixed page, source
   picker, search, browse, detail, download, states; downloads in the
   jobs popover; the Plugins dialog and the permission dialog.
2. ✅ `filters`, `collections` + `list`, `get`, `url`, `fields`; the
   object form of `capabilities`; `icon` and one-line `description`.
   All sources. Project Gutenberg rewritten on gutenberg.org's own OPDS
   catalog and built into Dalibre as `dalibre.gutenberg`.
3. ✅ A data folder per plugin (`node:fs` at `/data`, 64 MB), and `file`
   settings. Still to do: other setting types, keychain.
4. ✅ Metadata capability with `permissions.library`; Fetch metadata in
   Edit metadata; NeoDB built in as `dalibre.neodb`.
5. Index, restricted mode, updates.
6. ✅ File type capability: `process`, on import and from the book menu.
7. ✅ Actions: on books, on the library, the results view, `dalibre.form`;
   over MCP, `list_actions` and `run_action`, where the agent answers the
   forms ahead and nothing is written until it says `apply`. Still to
   do: a selection bar for the plugin button (the books chosen show
   their actions in the detail panel).
8. Convert, with Dalibre's own converters as built-in plugins.
9. Devices that mount as a disk; annotations.
10. Hooks on changes.

Each step ships on its own and keeps earlier plugins working.

## Code

| | |
|---|---|
| `internal/plugin/manifest.go` | `capabilities` object (array still accepted), `icon`, filters; `Reserved` ids |
| `internal/plugin/source.go` | `search`, `list`, `collections`, `get`, `resolve`; checks for `Query.filters`, `Collection`, `fields` |
| `internal/plugin/plugin.go` | `LoadBuiltin`: a plugin from an `fs.FS`, as the built-in ones |
| `plugins/` | the built-in plugins, embedded (`plugins.Builtin`) |
| `internal/app/getbooks.go` | the page's state: sources, lists, browse, get, covers, "In library" |
| `internal/app/view_getbooks.go` | the page: picker, filters, results, shelves, groups, states, detail |
| `internal/app/view_plugins.go` | the Plugins dialog and the permission dialog |
| `internal/app/plugins.go` | loading (built-in and installed), consent, downloads |
| `sdk/dalibre.d.ts`, `plugin.schema.json`, `README.md` | the contract |
| `internal/plugin/metadata.go` | `identify`: `Book`, candidates and their checks |
| `internal/plugin/actions.go` | `run`: forms, the library's books, progress, results and their checks |
| `internal/app/actions.go`, `view_actions.go` | running an action, applying what it returns; Tools, the form and review dialogs, the results view |
| `internal/app/fetchmeta.go`, `view_fetchmeta.go` | the Fetch metadata step: search, candidates, comparison, apply |
| `plugins/neodb` | the NeoDB metadata plugin |
| `plugins/googlebooks` | the Google Books metadata plugin |
