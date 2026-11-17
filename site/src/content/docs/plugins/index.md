---
title: Plugins
description: Everything about writing Dalibre plugins on one page, from a first book source to the full API reference. Also available as plain Markdown at /docs/plugins.md.
---

A Dalibre plugin is a folder with a `plugin.json` and a JavaScript (or
TypeScript) module.
Plugin API 1 offers four **capabilities**, and one plugin may have any
of them:

| Capability | What it does | Where users see it |
|---|---|---|
| **Book source** | Searches and browses a catalogue, and says where each book's file is. Dalibre downloads the file and adds the book to the library, with the metadata the plugin gave. | The **Get books** page |
| **Metadata source** | Looks a book up by its title, authors and identifiers, and returns candidates with their details. | Edit metadata › **Fetch metadata** |
| **File type** | Works on books' files: each file Dalibre adds, and the books the user runs it on. Cleans up, converts or unlocks with the user's own key, as calibre's file type plugins do. | Nowhere as files are added; the **book menu** to run it on books |
| **Actions** | Runs on the books the user chose, or on the whole library: find duplicates, check the library, merge books. Returns what it found and what it wants changed; Dalibre shows the books and applies the changes. | The **book menu**, and the sidebar's **Tools** |

**On this page:**

- [Overview](#overview)
- [Quick start](#quick-start)
- [plugin.json](#pluginjson)
- [Book sources](#book-sources)
- [Metadata sources](#metadata-sources)
- [File type plugins](#file-type-plugins)
- [Plugin actions](#plugin-actions)
- [Runtime and errors](#runtime-and-errors)
- [Permissions and limits](#permissions-and-limits)
- [The dalibre-plugin tool](#the-dalibre-plugin-tool)
- [Publishing](#publishing)
- [Type reference](#type-reference)

## Overview

### Plugins give data, never UI

A plugin returns plain objects; Dalibre draws every screen with its own
controls. Your plugin never sees a window, a DOM or a dialog.

That's a deliberate trade:

- **Users learn it once.** Every book source has the same search box,
  filters, shelves, detail panel and download progress.
- **Plugins can't impersonate.** A plugin can't fake a sign-in page or a
  system dialog, and its icon is picked from Dalibre's own set, never an
  image.
- **Dalibre knows what a plugin does before running it.** Capabilities,
  filters, settings and permissions are *declared* in `plugin.json`, so
  the app can lay out the source picker and ask for consent without
  loading any code.
- **The UI can change without breaking you.** Your contract is data, not
  layout.

### How a plugin runs

Plugins are JavaScript or TypeScript modules (ES2023), run by the
JavaScript engine built into Dalibre ([moejs](https://github.com/MoeForge/moejs)).
They use the standard APIs you know: `fetch`, `URL`, `setTimeout`,
`TextEncoder`, `node:fs`. There is no build step.

```text
my-source/
├── plugin.json        what the plugin is, adds and asks for
├── main.js            the functions Dalibre calls (or main.ts)
├── lib/…              more modules, imported by relative path (optional)
├── dalibre.d.ts       types for your editor (optional)
└── jsconfig.json
```

1. Dalibre reads `plugin.json` and checks it: unknown fields are errors.
2. When the plugin is first needed, Dalibre runs `main.js` once as a
   module. Module-level variables live until Dalibre quits or reloads the
   plugin.
3. Dalibre calls your exports (`search`, `identify`, …) with plain
   objects, and checks what you return. Bad results are dropped with a
   warning; the rest are shown.
4. Your script reaches the network with `fetch`, limited to the hosts
   you declared, and reads and writes files only in its own folders.
   Books are downloaded by Dalibre, not by your script.

Calls into one plugin run one at a time; different plugins run in
parallel. A plugin that throws or runs out of time only fails that one
call.

### What a book source looks like

```json
{
  "id": "com.example.books",
  "name": "Example Books",
  "version": "0.1.0",
  "api": 1,
  "capabilities": { "bookSource": {} },
  "permissions": { "hosts": ["books.example.com"] }
}
```

```js
// @ts-check
export async function search({ text, page }) {
  const res = await fetch(
    `https://books.example.com/api?q=${encodeURIComponent(text)}&p=${page}`
  );
  const { books, next } = await res.json();
  return {
    items: books.map((b) => ({
      id: b.id,
      title: b.title,
      authors: b.authors,
      cover: b.cover,
      formats: [{ format: "EPUB", url: b.epub }],
    })),
    hasMore: Boolean(next),
  };
}
```

That is a complete plugin. Everything else (filters, browsable
collections, a detail view, signed download links) is optional and added
one export at a time.

### Built-in plugins

Three plugins ship inside Dalibre, and are good worked examples:

- **Project Gutenberg** (`dalibre.gutenberg`), a book source on
  gutenberg.org's own OPDS catalog: search with language and sort
  filters, *Most popular* and *Recently added* shelves, and `get` for
  each book's details and file sizes.
- **NeoDB** (`dalibre.neodb`), a metadata source on neodb.social's open
  catalog: by NeoDB id or ISBN when the book has one, else by title and
  first author, with ranking. Candidates carry NeoDB, Douban, Goodreads,
  Open Library and Google Books ids.
- **Google Books** (`dalibre.googlebooks`), a metadata source on the
  Books API, with an optional API key in its settings for when Google's
  shared quota runs out.

## Quick start

> **The fastest start: [dalibre-plugin-template](https://github.com/djyde/dalibre-plugin-template).** On GitHub,
> click **Use this template**, clone your new repository, and set your own
> `id` and `name` in `plugin.json`. It has a starter `main.js`, the types
> and schema for your editor, and a workflow that checks the plugin on
> each push and publishes a release when you push a tag. The same files
> come from `npx dalibre-plugin init my-plugin`.

In this guide you'll write a book source for Project Gutenberg's
70,000 public-domain books, read from its OPDS catalog. By the end it
will search, filter by language, show covers, offer shelves to browse
and download EPUBs into your library.

You need Dalibre and a text editor. You don’t need Node or npm, and there is no build step.

### 1. Make a folder

Make a folder called `gutenberg`, and download three files into it for
your editor:

- [`dalibre.d.ts`](/sdk/dalibre.d.ts): the plugin API's types.
- [`jsconfig.json`](/sdk/jsconfig.json): tells your editor to check
  `main.js` against them.
- [`plugin.schema.json`](/sdk/plugin.schema.json): the manifest's JSON
  Schema.

```sh
mkdir gutenberg && cd gutenberg
for f in dalibre.d.ts jsconfig.json plugin.schema.json; do
  curl -fsSO "https://dalibre.randynamic.org/sdk/$f"
done
```

They're optional, but worth it: with `// @ts-check` at the top of
`main.js`, VS Code and other editors check your code and complete the
API as you type, and `plugin.json` is validated as you write it.

> Started from the [template](https://github.com/djyde/dalibre-plugin-template) or `npx dalibre-plugin init`? These
> files are there already.

### 2. Pick an id

Every plugin has a permanent `id`: it's how Dalibre tells your plugin
apart from every other, across updates. Use a domain you control,
reversed (`com.example.books`), or your GitHub name. This guide uses
`io.github.you.gutenberg`.

### 3. Describe the plugin

Replace `plugin.json` with:

```json
{
  "$schema": "./plugin.schema.json",
  "id": "io.github.you.gutenberg",
  "name": "Gutenberg (OPDS)",
  "version": "0.1.0",
  "api": 1,
  "description": "70,000 public-domain books from Project Gutenberg",
  "author": "You",
  "license": "MIT",
  "icon": "library",
  "capabilities": {
    "bookSource": {
      "filters": [
        {
          "key": "language", "title": "Language", "type": "language",
          "options": [
            { "value": "", "title": "Any language" },
            { "value": "en" }, { "value": "fr" }, { "value": "de" }, { "value": "zh" }
          ]
        }
      ]
    }
  },
  "permissions": {
    "hosts": ["www.gutenberg.org", "gutenberg.org"]
  }
}
```

Two things matter here:

- **`capabilities.bookSource`** tells Dalibre to list the plugin on the
  Get books page, and declares one filter. Dalibre draws the drop-down
  and names the languages; you only get the chosen code.
- **`permissions.hosts`** lists every host the plugin reaches: the
  catalog, the files and the covers, redirects included.

### 4. Write search

Project Gutenberg publishes its catalog as
[OPDS](https://opds.org), an Atom XML feed that most e-book catalogs
speak. Each book in a search is an `<entry>` like this:

```xml
<entry>
  <id>https://www.gutenberg.org/ebooks/1342.opds</id>
  <title>Pride and Prejudice</title>
  <content type="text">Jane Austen</content>
  …
</entry>
```

The sandbox has no XML parser, but the feed is generated and
well-formed, so a few regular expressions are enough. Replace `main.js`
with:

```js
// @ts-check
const SITE = "https://www.gutenberg.org";
const PER_PAGE = 25; // what the catalog returns per page

/**
 * @param {Dalibre.Query} query
 * @returns {Promise<Dalibre.Page>}
 */
export async function search({ text, page, filters }) {
  return feed(text, filters.language, "downloads", page);
}

/**
 * A page of the catalog: books matching text, in sort order.
 * @param {string} text @param {string} language @param {string} sort @param {number} page
 * @returns {Promise<Dalibre.Page>}
 */
async function feed(text, language, sort, page) {
  // The catalog's own syntax narrows by language: "dickens l.fr".
  const query = [text, language ? `l.${language}` : ""].filter(Boolean).join(" ");
  let url = `${SITE}/ebooks/search.opds/?sort_order=${sort}&start_index=${(page - 1) * PER_PAGE + 1}`;
  if (query) url += `&query=${encodeURIComponent(query)}`;

  const res = await fetch(url);
  if (res.status === 429) throw dalibre.error("rateLimited");
  if (!res.ok) throw dalibre.error("unavailable", `Project Gutenberg answered ${res.status}.`);
  const xml = await res.text();

  /** @type {Dalibre.Result[]} */
  const items = [];
  for (const [, entry] of xml.matchAll(/<entry>([\s\S]*?)<\/entry>/g)) {
    // Books link to /ebooks/<number>.opds; other entries are lists of authors or subjects.
    const id = /\/ebooks\/(\d+)\.opds/.exec(tag(entry, "id"))?.[1];
    if (!id) continue;
    const author = tag(entry, "content");
    items.push({
      id,
      // With a language asked for, titles end in it: "Oliver Twist (French)".
      title: tag(entry, "title").replace(/\s+\([A-Z][a-z]+\)$/, ""),
      authors: author ? [author] : [],
      identifiers: { gutenberg: id },
      url: `${SITE}/ebooks/${id}`,
      cover: `${SITE}/cache/epub/${id}/pg${id}.cover.medium.jpg`,
      formats: [
        { format: "EPUB", url: `${SITE}/ebooks/${id}.epub3.images` },
        { format: "MOBI", url: `${SITE}/ebooks/${id}.kindle.images` },
      ],
    });
  }
  return { items, hasMore: xml.includes('rel="next"') };
}

/** The text inside the first <name>…</name>, or "". @param {string} xml @param {string} name */
function tag(xml, name) {
  const m = new RegExp(`<${name}[^>]*>([\\s\\S]*?)</${name}>`).exec(xml);
  return m ? decode(m[1]).trim() : "";
}

/** @param {string} s */
function decode(s) {
  return s
    .replace(/&#(\d+);/g, (_, d) => String.fromCodePoint(Number(d)))
    .replace(/&quot;/g, '"').replace(/&apos;/g, "'")
    .replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
}
```

What's going on:

- `search` is called with what the user typed, the page (from 1) and the
  value of every filter you declared. When the user asks for more, it's
  called again with `page + 1`, as long as you returned `hasMore: true`.
- Each result needs an `id`, a `title` and at least one format. The rest
  is optional, but users choose from what they see: authors and a cover
  make the choice easy. Gutenberg keeps files and covers at predictable
  addresses, so one request per search is enough.
- `identifiers.gutenberg` lets Dalibre mark a result **In library** when
  the user already has that book.
- `dalibre.error("rateLimited")` tells Dalibre to show "Try again in a
  while" instead of a generic failure.

### 5. Load it into Dalibre

In Dalibre, open **Plugins › Add plugin › From a folder…**
and pick the plugin's folder. Grant the hosts it asks for.

Dalibre loads the plugin from that folder, not a copy, and reloads it a
moment after you save a change. Open **Get books**, pick
*Gutenberg (OPDS)* in the source picker, and search: try
*pride and prejudice*, then *dickens* with the language set to French,
then download a book into your library.

If the plugin doesn't load, Plugins shows why: a field
`plugin.json` doesn't allow, a missing export, a script error.

From a terminal, `npx dalibre-plugin dev .` does the same: it adds the
folder to the running Dalibre and prints each reload, with its errors.
It needs Dalibre's MCP server on (Settings › MCP).

Your own AI agent can do this loop for you over [MCP](/docs/mcp/#write-a-plugin): it starts
the plugin, adds its folder, calls its functions and reads their
results, logs and errors. You still allow the hosts it reaches.

> Dalibre already ships a Project Gutenberg source built on this same
> catalog. Put yours next to it and compare: the built-in one also
> exports `get`, which reads each book's own entry for its description,
> subjects and file sizes.

### 6. Add browsable shelves (optional)

With the search box empty, a source can show shelves of covers. Add
`"collections": true` to `capabilities.bookSource` in `plugin.json`, and
two exports to `main.js`:

```js
const SHELVES = [
  { id: "popular", title: "Most popular", sort: "downloads" },
  { id: "new", title: "Recently added", sort: "release_date" },
];

export function collections() {
  return SHELVES.map(({ id, title }) => ({ id, title }));
}

/** @param {Dalibre.Query} query @returns {Promise<Dalibre.Page>} */
export async function list({ collection, page, filters }) {
  const shelf = SHELVES.find((s) => s.id === collection);
  if (!shelf) throw dalibre.error("notFound");
  return feed("", filters.language, shelf.sort, page);
}
```

Save, and the Get books page shows your two shelves, each with *See all*.

### 7. Share it

Push the folder to a GitHub repository, with `plugin.json` at its root.
Anyone can then install it from **Plugins › Install from
GitHub**. Tag releases so they get your updates: see
[Publishing](#publishing).

## plugin.json

`plugin.json` sits at the root of your plugin's folder. Dalibre reads it
before running any code: it's how the app lays out the source picker,
draws your filters and settings form, and asks the user for consent.

### A full example

```json
{
  "$schema": "./plugin.schema.json",
  "id": "com.example.books",
  "name": "Example Books",
  "version": "1.2.0",
  "api": 1,
  "description": "Books from example.com",
  "author": "Ann Author",
  "homepage": "https://github.com/ann/dalibre-example-books",
  "license": "MIT",
  "main": "main.js",
  "icon": "globe",
  "capabilities": {
    "bookSource": {
      "collections": true,
      "filters": [
        { "key": "language", "title": "Language", "type": "language",
          "options": [{ "value": "", "title": "Any language" }, { "value": "en" }, { "value": "fr" }] },
        { "key": "sort", "title": "Sort", "type": "select", "default": "popular",
          "options": [{ "value": "popular", "title": "Most popular" }, { "value": "new", "title": "Newest" }] }
      ]
    }
  },
  "permissions": {
    "hosts": ["api.example.com", "*.cdn.example.com"],
    "localNetwork": false
  },
  "settings": [
    { "key": "apiKey", "title": "API key", "type": "secret", "required": true,
      "description": "From your account page on example.com." }
  ]
}
```

**Unknown fields are errors**, so a misspelt one stops the plugin from
loading rather than being silently ignored. Point `"$schema"` at a copy
of [`plugin.schema.json`](/sdk/plugin.schema.json), and your editor
validates and completes the file as you type.

### Identity

| Field | |
|---|---|
| `id` | **Required.** Permanent and unique: lowercase letters, digits and `-`, in dot-separated parts, up to 64 characters. Use a domain you control, reversed (`com.example.books`), or your GitHub name (`io.github.ann.books`). Changing it makes a different plugin. |
| `name` | **Required.** Shown to users; 1 to 64 characters. |
| `version` | **Required.** A [semantic version](https://semver.org): `1.2.0`, `2.0.0-beta.1`. Bump it in every release, or users won't be offered the update. |
| `api` | **Required.** The plugin API you wrote for: `1`. |
| `dalibre` | The oldest Dalibre your plugin works in, as `">=0.4.0"`, when it uses what plugin API 1 gained since it began (actions, `covers`, `remember`, toggle, select and number settings): an older Dalibre refuses it, and its Marketplace shows it as needing an update. `dalibre-plugin check` warns when you need it. |
| `description` | One line, shown under your name in the source picker and when the user installs. Say what the catalogue holds: "Over 70,000 free public-domain ebooks". |
| `author`, `homepage`, `license` | Shown when the user installs. `license` is an SPDX id (`MIT`, `Apache-2.0`). The Get books page's *Open site* button opens `homepage`. |
| `icon` | One of Dalibre's icons: `globe` (default), `library`, `book-open`, `database`, `network`, `building`, `bookmark`, `layers`. Never an image, so a plugin can't dress up as another app. |
| `main` | The main module, relative to `plugin.json`: a `.js`, `.mjs`, `.ts` or `.mts` file. Default `main.js`. |

> Ids starting with `dalibre` are kept for the plugins that ship inside
> Dalibre. A package using one isn't installed.

### capabilities

**Required.** What the plugin adds to Dalibre: an object with one or
more keys.

```json
"capabilities": {
  "bookSource": { "collections": true, "filters": [ … ] },
  "metadata": { "fields": ["title", "authors", "cover", "description"] },
  "fileType": { "title": "Clean up EPUB" },
  "actions": [{ "id": "find", "title": "Find duplicates…", "scope": "library" }]
}
```

#### bookSource

Lists the plugin on the **Get books** page. Export `search`; see
[Book sources](#book-sources).

| Field | |
|---|---|
| `collections` | `true` when you export `collections` and `list`: shelves to browse while the search box is empty. |
| `filters` | Up to 4 drop-downs under the search box. See [filters](#filters). |

#### metadata

Lists the plugin in **Fetch metadata**. Export `identify`, and ask for
`permissions.library: "read"`; see
[Metadata sources](#metadata-sources).

| Field | |
|---|---|
| `fields` | Optional: what you can fill, from `title`, `authors`, `series`, `tags`, `publisher`, `pubdate`, `language`, `identifiers`, `cover` and `description`. |

#### fileType

Runs the plugin on books' files. Export `process`; see
[File type plugins](#file-type-plugins). It needs no permission: the
files are handed to it.

| Field | |
|---|---|
| `title` | What the book menu calls running it: `"Clean up EPUB"`. Default: the plugin's name. Up to 64 characters. |
| `onImport` | `false` to run only from the book menu, not on the files Dalibre adds. Default `true`. |
| `onPostImport`, `onPostAdd`, `onPostDelete` | `true` to be called after a file is added, a book is added, a format is removed. See [hooks](#hooks). |

#### actions

Adds actions to the book menu and the sidebar's **Tools**: a list of
`{ id, title, scope, … }`. Export `run`; see [Actions](#plugin-actions).

#### The array form

API 1 began with `"capabilities": ["source"]`. It still works and means
a book source with no filters or collections; `["metadata"]` works too.
Prefer the object form in new plugins.

### Filters

Each filter is a drop-down Dalibre draws under the search box. Its value
arrives in `query.filters[key]` on every `search` and `list` call.

| Field | |
|---|---|
| `key` | **Required.** The name in `query.filters`: a letter, then letters, digits or `_`. |
| `title` | **Required.** Shown on the drop-down. |
| `type` | **Required.** `"select"`: options with titles. `"language"`: options are ISO 639 codes, and Dalibre names them in the user's language; `""` with a title means "any". |
| `options` | **Required.** 1 to 64 `{ value, title? }`. `title` is required for `select`. |
| `default` | The option chosen at first. Without it, the first option. |

Every declared filter always has one of its options' values: the
`default`, else the first, until the user picks.

In **All sources**, only a language filter shows, shared by every source
that has one. While browsing collections, only the language shows too: a
collection keeps its own order.

### permissions

**Required.** What the plugin may reach. Users grant these when they
install; see [Permissions & limits](#permissions-and-limits).

| Field | |
|---|---|
| `hosts` | **Required.** Every host your plugin reaches, through `fetch`, file downloads, covers **and redirects**. `*.example.com` matches any subdomain, not `example.com` itself. |
| `library` | `"read"`: your plugin sees the details of the books it is asked about. **Required for a metadata source** and for [hooks](#hooks). `"write"`: your hooks may also change those details. The install dialog says so. |
| `localNetwork` | `true` to reach this computer and the local network: a home OPDS or calibre server. Users are warned. Default `false`. |

### settings

Values the user enters, up to 32. Dalibre draws the form in the plugin's
settings, and your code reads them from `dalibre.settings`.

```json
"settings": [
  { "key": "server", "title": "Server address", "default": "http://192.168.1.10:8080" },
  { "key": "token", "title": "Access token", "type": "secret", "required": true,
    "description": "Create one under Account › Tokens." }
]
```

| Field | |
|---|---|
| `key` | **Required.** The name in `dalibre.settings`: a letter, then letters, digits or `_`. |
| `title` | **Required.** The field's label. |
| `type` | `text` (default), `secret` or `file`. Secrets are masked, hidden in logs, and kept in the system's keychain rather than Dalibre's settings file. A `file` setting is a file the user chooses, such as a key: see [file settings](#file-settings). |
| `description` | A line of help under the field. |
| `default` | The value until the user sets one. |
| `required` | The plugin isn't called until it's filled; the source shows *Needs setup* instead. |

All values are strings.

#### File settings

A `file` setting shows a **Choose file…** button. The file the user
picks is copied into your [data folder](#your-folders), and the setting's
value is the path you read it at:

```json
{ "key": "keyFile", "title": "Key file", "type": "file", "required": true,
  "description": "Export it from your account page." }
```

```js
import { readFile } from "node:fs/promises";
const key = await readFile(dalibre.settings.keyFile);   // "/data/.settings/keyFile/user-key.der"
```

It applies as soon as it is chosen; up to 16 MB. With `dalibre-plugin`,
`--set keyFile=path/to/user-key.der` copies the file in the same way.

## Book sources

A book source lets users search and browse a catalogue from Dalibre's
**Get books** page, open a book in the detail panel, and download it
into their library with the metadata you gave.

Declare it in `plugin.json`:

```json
"capabilities": { "bookSource": { "collections": true, "filters": [ … ] } }
```

and export the functions for each step:

| Export | Called when | Returns | Required |
|---|---|---|---|
| [`search(query)`](#search) | The user searches, or asks for more | `Page` | Yes |
| [`collections()`](#collections-and-list) | The browse page opens | `Collection[]` | With `collections` |
| [`list(query)`](#collections-and-list) | A shelf loads, or *See all* | `Page` | With `collections` |
| [`get(item)`](#get) | A result is opened in the detail panel | `Result` | No |
| [`resolve(item, format)`](#resolve) | The user downloads | `DownloadRequest` | No |

### search

```js
/** @param {Dalibre.Query} query @returns {Promise<Dalibre.Page>} */
export async function search({ text, page, cursor, filters }) { … }
```

Called when the user searches with your source, and again with
`page + 1` when they ask for more.

```ts
query: {
  text: string,                      // what the user typed, trimmed; may be empty
  page: number,                      // from 1
  cursor: string,                    // "" at first; what your last page returned
  filters: Record<string, string>,   // every declared filter's value
}

Page: { items: Result[], hasMore?: boolean, cursor?: string }
```

- Return `{ items: [] }` when nothing is found.
- `text` may be empty: return popular or new books, or nothing.
- Set `hasMore: true` to let the user ask for the next page.
- If your API pages by token rather than number, return it as `cursor`;
  you get it back as `query.cursor` in the next call.
- At most 100 items per page.

With **All sources** picked, every enabled source is searched at once,
and each shows its first 3 results under its name. One source failing or
needing setup is a state of its own group, never of the page.

### Results

A **Result** is one book.

| Field | | |
|---|---|---|
| `id` | **required** | Unique within your source: a string, up to 256 characters. |
| `title` | **required** | |
| `formats` | **required** | At least one `{ format, url?, size? }`. |
| `authors` | | Array of names, "First Last". |
| `description` | | Plain text or simple HTML. |
| `publisher`, `series`, `seriesIndex`, `tags` | | |
| `language` | | ISO 639: `"en"`, `"eng"`, `"zh-CN"`. |
| `pubdate` | | `"2006"`, `"2006-01"`, `"2006-01-02"` or an RFC 3339 time. |
| `identifiers` | | `{ isbn: "…", goodreads: "…" }`, as calibre keeps them. Add your own id under your plugin's name, so users can find the book's page later. |
| `cover` | | An image URL on one of your hosts. Shown in the results, and used if the file has no cover. |
| `url` | | The book's page on your site; the detail panel links to it. |
| `fields` | | Up to 12 more lines for the detail panel: `[{ label: "Rights", value: "Public domain" }]`. |
| `extra` | | Anything JSON. Handed back unchanged to `resolve`. |

#### Formats

A format is `{ format, url?, size? }`:

- `format` is one of `EPUB`, `KEPUB`, `AZW3`, `AZW`, `MOBI`, `PDF`,
  `FB2`, `DJVU`, `CBZ`, `CBR`, `CB7`, `TXT`, `RTF`, `DOCX`, `ODT`,
  `HTMLZ`, `LIT`, `PRC`, `PDB`, `LRF`, `SNB`, `TCR` (any case).
- `size` is in bytes, shown to the user.
- `url` is where to download it. Leave it out and export
  [`resolve`](#resolve) when links must be made at download time.

#### Results are checked

Dalibre checks every result you return:

- A result missing `id`, `title` or `formats`, or with an unknown format,
  is **dropped**.
- An invalid `cover` or `pubdate` is **ignored**.
- An unknown field (`author` for `authors`) is **ignored**.

Each gives a warning in Dalibre's log; users never see them. If a book
you expect is missing from the results, check its `id`, `title` and
`formats` first.

#### In library

Dalibre marks a result **In library** (instead of *Get*) when one of its
`identifiers` matches a book the user already has. Give your own id and
the ISBN whenever you know them.

### Filters

Filters are drop-downs under the search box, declared in
`capabilities.bookSource.filters` and passed in `query.filters`.

```json
"filters": [
  { "key": "language", "title": "Language", "type": "language",
    "options": [{ "value": "", "title": "Any language" }, { "value": "en" }, { "value": "fr" }] },
  { "key": "sort", "title": "Sort", "type": "select", "default": "popular",
    "options": [{ "value": "popular", "title": "Most popular" }, { "value": "new", "title": "Newest" }] }
]
```

```js
export async function search({ text, page, filters }) {
  // filters.language is "", "en" or "fr"; filters.sort is "popular" or "new"
}
```

Every declared filter is always present, with one of its options'
values. Field by field: [plugin.json › Filters](#filters).

### collections and list

With `"collections": true`, the Get books page shows your collections as
shelves of covers while the search box is empty, each with *See all*.

```js
export function collections() {
  return [
    { id: "popular", title: "Most popular" },
    { id: "new", title: "Recently added" },
  ];
}

/** @param {Dalibre.Query} query @returns {Promise<Dalibre.Page>} */
export async function list({ collection, page, cursor, filters }) {
  // a Page of that collection, as search returns
}
```

- `collections()` is called once per session; return at most 12.
- `list(query)` gets the same query as `search`, with `collection` set to
  a collection's `id` and `text` empty.
- While browsing, only a language filter shows; a collection keeps its
  own order.

### get

```js
/** @param {Dalibre.Result} item @returns {Promise<Dalibre.Result>} */
export async function get(item) { … }
```

Optional. Called when the user opens a result in the detail panel.
Return everything you know: description, subjects, language, file sizes,
`url`, `fields`.

It costs a request per book opened, so `search` can return only what its
listing has, and `get` fills in the rest. The `id` is kept; formats and
`extra` you leave out are kept from `item`.

```js
export async function get(item) {
  const b = await (await fetch(`${API}/books/${item.id}`)).json();
  return {
    ...item,
    description: b.summary,
    tags: b.subjects,
    url: b.page_url,
    fields: [{ label: "Rights", value: b.rights }],
  };
}
```

Without `get`, the detail panel shows the search result as it is.

### resolve

```js
/** @param {Dalibre.Result} item @param {Dalibre.FormatName} format
    @returns {Promise<Dalibre.DownloadRequest>} */
export async function resolve(item, format) { … }
```

Optional. Without it, Dalibre downloads `format.url`. Export it when
links expire, need a second request, or need headers:

```js
export async function resolve(item, format) {
  const auth = { Authorization: `Bearer ${dalibre.settings.apiKey}` };
  const res = await fetch(
    `https://api.example.com/books/${item.id}/download?format=${format}`,
    { headers: auth },
  );
  if (res.status === 404) throw dalibre.error("notFound");
  return { url: (await res.json()).url, headers: auth };
}
```

- If you export `resolve`, it's called for **every** download.
- `item` is the result as `search` returned it (after checking), `extra`
  included. `format` is upper case.
- The file name comes from `filename`, else the server's
  `Content-Disposition`, else the title. Its extension is always the
  format's.

#### Dalibre does the download

Your script never touches the file. Dalibre:

- streams it to disk with progress and cancel, in the jobs popover;
- follows redirects, only within your hosts;
- refuses an HTML page (a login or error page) where a book was expected;
- turns HTTP 401, 403, 404 and 429 into the [errors](#errors)
  users understand;
- imports the book with the metadata of your result.

This keeps files off the plugin's side of the sandbox, and gives every
source the same download behaviour.

### How states map to the page

| Your plugin | The Get books page shows |
|---|---|
| A required setting is empty, or you throw `auth` | *Needs setup*, with a button to your settings |
| You throw `rateLimited` or `unavailable` | A banner with *Retry* |
| You throw `notFound` or `invalid` | Your message |
| Anything else thrown | "The plugin failed", with your message |
| `{ items: [] }` | No results |

## Metadata sources

A metadata source looks books up. Given a book's title, authors and
identifiers, it returns candidates with their details. Dalibre shows
them in Edit metadata › **Fetch metadata**, next to the book's own
values, with a checkbox per field, and fills in the fields the user
picks. Only *Save* writes the library.

It's the equivalent of calibre's metadata sources.

### Declaring it

```json
{
  "capabilities": {
    "metadata": { "fields": ["title", "authors", "cover", "description"] }
  },
  "permissions": {
    "hosts": ["api.example.com"],
    "library": "read"
  }
}
```

- `"library": "read"` is required: your plugin sees the books the user
  looks up with it, and the install dialog says so.
- `fields` (optional) lists what you can fill, from `title`, `authors`,
  `series`, `tags`, `publisher`, `pubdate`, `language`, `identifiers`,
  `cover` and `description`.

A plugin can be both a book source and a metadata source; declare both
keys under `capabilities`.

### identify

```js
/** @param {Dalibre.Book} book @returns {Promise<Dalibre.Candidate[]>} */
export async function identify(book) { … }
```

Called when the user opens Fetch metadata, or searches in it. `book` is:

```ts
{
  title: string,                        // may be empty when identifiers are given
  authors: string[],
  identifiers: Record<string, string>,  // lowercase keys: { isbn, goodreads, … }
  language?: string,                    // ISO 639, when the book has one
}
```

That's the book's own values, or, when the user typed something else in
the step's search box, `{ title: "what they typed" }`, or
`{ identifiers: { isbn } }` when they typed an ISBN. **Nothing else of
the library is sent.**

```js
export async function identify({ title, authors, identifiers }) {
  const q = identifiers.isbn || [title, authors[0]].filter(Boolean).join(" ");
  if (!q) return [];
  const res = await fetch(
    `https://api.example.com/books?q=${encodeURIComponent(q)}`
  );
  if (res.status === 429) throw dalibre.error("rateLimited");
  if (!res.ok) throw dalibre.error("unavailable");

  const { books } = await res.json();
  return books.map((b) => ({
    id: b.key,
    title: b.title,
    authors: b.authors,
    publisher: b.publisher,
    pubdate: b.date,
    identifiers: { isbn: b.isbn, example: b.key },
    cover: b.image,
    description: b.summary,
  }));
}
```

### Candidates

A candidate is a [result](#results) without
`formats` or `extra`:

- `id` and `title` are required; everything else is optional, and shown
  next to the book's value.
- Return candidates **best first**, at most 20, and `[]` when nothing
  matches.
- Give everything you know in `identify`. There is no second call for
  details.
- Dalibre fetches `cover` when the candidate is shown, so it must be on
  one of your hosts.
- `identifiers` are merged into the book's (keys are lower-cased). Add
  your own id under your plugin's name, so a later lookup can go
  straight to the same record.

### Ranking

Rank with what you're given:

1. An **identifier** match first: your own id, then the ISBN. When the
   book has one and you find it, that's usually the only candidate worth
   returning.
2. Then the **title**: exact, then prefix, then contains. Compare
   normalised (lower case, no punctuation or accents).
3. Then the **authors** and the **language**.

The built-in NeoDB plugin is a complete example: it looks up by NeoDB id
or ISBN when the book has one (an ISBN search keeps only that edition),
else searches by title and first author, then by title alone, and scores
the merged results by title, authors and language.

### With several metadata sources

Fetch metadata searches every enabled metadata source at once, or the one
picked in its source menu. Candidates are listed by plugin, then by your
rank.


## File type plugins

A file type plugin works on books' files, as calibre's file type plugins
do: it cleans them up, converts them, or unlocks them with the user's own
key. It gets a copy of a book's file and returns a new one.

```json
"capabilities": { "fileType": { "title": "Clean up EPUB" } }
```

```js
import { readFile, writeFile } from "node:fs/promises";

/** @param {Dalibre.File} file @returns {Promise<Dalibre.Processed>} */
export async function process(file) {
  if (file.format !== "EPUB") return null;            // not for us: leave it
  const book = await readFile(file.path);
  const cleaned = clean(book);
  if (!cleaned) return null;                          // nothing to change
  await writeFile(file.path, cleaned);                // in place, or beside it
  return { path: file.path, report: ["Removed 3 unused fonts"] };
}
```

### When it runs

- **As files are added**: every file Dalibre adds, whether dropped,
  chosen with Add books, downloaded from Get books, added as a format, or
  added by an AI agent, goes through every file type plugin, one after
  the other, before it is added. Set `"onImport": false` to skip this.
- **From the book menu**: your `title` shows in the menu of a book (and
  of a selection). It runs at once on the books, in the background, with
  no dialog. Each book's first format you might take is used (EPUB, then
  AZW3, MOBI, PDF…).

Dalibre writes what you return: in place of the old file, which is kept
as `ORIGINAL_EPUB` (as calibre does) and can be put back with **Undo**;
or, when you return another format (a KFX book made into EPUB), as a
format of its own.

### process

`process(file)` gets:

| | |
|---|---|
| `path` | Where to read the file: a copy in `/work`. Change it in place, or write the new file beside it. |
| `name` | The file's own name: `"Dune - Frank Herbert.epub"`. |
| `format` | Its format, upper case: `"EPUB"`. |
| `reason` | `"import"` as the file is added, `"manual"` from the book menu. |
| `book` | From the book menu: `{ title, authors, identifiers }`. |

It returns:

- `null`: leave the file as it is. Return it fast for files that aren't
  yours: every added file comes to you.
- the new file's path, in `/work` or `/data`, named with its format's
  extension (`book.epub`);
- or `{ path, report }`, `report` being a few lines on what you did, shown
  by `dalibre-plugin process`.

You get the whole file, and memory isn't limited, so a large book is
fine; the call has 10 minutes.

### When it fails

- As a file is added, a plugin that throws is passed over: the file is
  added as it was, and the user is told.
- From the book menu, throw `dalibre.error("auth", …)` when you need
  something from the user, such as a key: the run stops, and the user is
  offered your settings. Any other error is counted, and the other books
  still run.

### Hooks

As calibre's file type plugins, yours can ask to be called after the
library changed. Ask for each in `plugin.json`, export it, and ask for
`permissions.library`:

| Ask with | Export | Called after | calibre |
|---|---|---|---|
| `onPostImport` | `postimport(event)` | a file was added to a book: a new book, or a new format | `on_postimport` |
| `onPostAdd` | `postadd(event)` | a book was added, with all its files | `on_postadd` |
| `onPostDelete` | `postdelete(event)` | a format was removed | `on_postdelete` |

```json
"capabilities": { "fileType": { "onImport": false, "onPostAdd": true } },
"permissions": { "library": "write" }
```

```js
export async function postadd({ book, files }) {
  const pages = await countPages(files[0].path);
  return { metadata: { identifiers: { pages: String(pages) } } };
}
```

- `event.book` is the book's details: `{ id, title, authors, series,
  seriesIndex, tags, publisher, pubdate, languages, identifiers, rating,
  comments, formats }`. `postimport` also gets `format` and `file` (a copy
  in `/work`), `postadd` gets `files`, `postdelete` gets `format`.
- Return `null`, or `{ metadata: { … } }` with the fields to change.
  calibre's hooks write the database themselves; yours say what to change,
  and Dalibre writes it. That needs `"library": "write"`, which the user
  is shown, in a warning, when installing. Identifiers are merged: an
  empty value removes one.
- They run after the library changed, in the background. One that fails
  is logged, as calibre does: what happened to the book stands.
- A plugin may have only hooks: `"onImport": false` and no `process`. It
  isn't offered in the book menu then.
- calibre's conversion hooks (`on_preprocess`, `on_postprocess`,
  `on_postconvert`) have no counterpart: Dalibre doesn't convert books
  through plugins.

### Trying it

```sh
dalibre-plugin process . "My Book.epub"                  # as an import, saved beside
dalibre-plugin process . "My Book.epub" --manual -o out  # as from the book menu
dalibre-plugin process . book.epub --set keyFile=user-key.der
```

## Plugin actions

Actions are what the user runs on the books they chose, from the book
menu, or on the whole library, from the sidebar's **Tools**: find
duplicates, check the library, merge books. An action returns what it
found and what it wants changed; Dalibre shows the books as a list and
applies the changes.

### Declaring them

A plugin with `"actions"` in its capabilities exports `run(action,
input)`, called with the id of the action the user chose:

```json
"capabilities": {
  "actions": [
    { "id": "find", "title": "Find duplicates…", "icon": "copy", "scope": "library" },
    { "id": "exempt", "title": "Not duplicates", "scope": "books", "books": { "min": 2 }, "in": "results" },
    { "id": "merge", "title": "Merge into one book", "scope": "books",
      "books": { "min": 2 }, "formats": ["EPUB"], "files": true }
  ]
},
"permissions": { "hosts": [], "library": "read" }
```

- `scope`: `"books"` runs on the books chosen, from the book menu and the
  selection's **Plugins** button; `"library"` runs on every book, from
  the sidebar's **Tools**.
- `title` is what the menu says; end it in `…` when the action asks
  something first. `icon` is one of Dalibre's own (the schema lists
  them).
- `books` is how many books it takes (`min`, `max`), `formats` the
  formats it works on (every book must have one): Dalibre greys the item
  out otherwise, without running your code.
- `files: true` gives you a copy of each book's file in `/work`;
  `covers: true` a copy of each one's cover, for the books that have
  one.
- `in: "results"` shows the action only among the books your own results
  list (below), where it comes first.

### run

```js
export async function run(action, input) {
  const { by } = await dalibre.form({
    submit: "Find duplicates",
    fields: [
      { key: "by", title: "Compare", type: "select", value: "title",
        options: [{ value: "title", title: "Title and author" }, { value: "isbn", title: "ISBN" }] },
    ],
  });
  const seen = new Map();
  let n = 0;
  for await (const book of dalibre.library.books()) {
    dalibre.progress(++n, 0);
    const key = by === "isbn" ? book.identifiers.isbn : book.title + "|" + book.authors.join();
    if (key) seen.set(key, [...(seen.get(key) ?? []), book]);
  }
  const groups = [...seen.values()].filter((books) => books.length > 1)
    .map((books) => ({ title: books[0].title, books: books.map((b) => ({ id: b.id })) }));
  return groups.length ? { groups } : { message: "No duplicates" };
}
```

`input` is `{ books, files? }`: `books` are the books chosen, in the
user's order, as a hook sees them (`id`, `title`, `authors`, `tags`,
`identifiers`, `formats`, `comments`, `titleSort`, `authorSort`,
`added`, `modified`, `hasCover`, `uuid`…), and empty for a `"library"`
action; `files` has each book's file by book id (`{ path, name, format
}`), and `covers` each one's cover's path. Authors are as calibre shows them: "Herbert, Frank", not
calibre's stored "Herbert| Frank".

### While it runs

An action has three things only Dalibre can give:

| | |
|---|---|
| `await dalibre.form({ submit?, fields, remember? })` | Asks the user, in a dialog Dalibre draws under your action's title, and resolves to the answers by `key`. A field is `{ key, title, type, value?, options? }` with `type` `"text"`, `"toggle"`, `"select"` or `"multiselect"`; `value` is what it starts with. With `remember: true` the form starts with the user's last answers to it instead, as calibre plugins keep their options. Cancel rejects with kind `"cancelled"`: leave it uncaught and the run ends quietly. Call it when you know what to ask (after reading the book, say), as often as you need. The run's time doesn't count while it is open. |
| `dalibre.library.books(options?)` | An async iterator over every book in the library; `comments` only with `{ comments: true }`, which reads more. |
| `dalibre.progress(done, total)` | The job's bar; `total` 0 when you don't know. |

### What it returns

Return `null`, or what you found and want changed:

| | Dalibre |
|---|---|
| `message` | says it in a toast: "No duplicates" |
| `groups: [{ title, books: [{ id, note? }] }]` | opens the books as a list under each group's title, with `note` under a book's title. It is a book list like any other: the book menu and the detail panel work in it. A book shows once, under the first group that has it |
| `changes: [{ book, metadata }]` | changes books' details, as a hook's `metadata`: at once with `"library": "write"` (and Undo), else shown to the user first, who applies them. A field left out stays as it is; `""` (or `[]`) clears it: `{ series: "" }` takes a book out of its series |
| `add: [{ path, metadata? }]` | adds each file as a new book |
| `formats: [{ book, path }]` | adds the file to the book; a file of the same format is kept as `ORIGINAL_<format>` |
| `open` | opens the page in the browser; it must be on one of your hosts |

An action never deletes: the books it ran on stay as they were.

### Trying it

A run has 10 minutes and is a job the user can stop. Try it with
`dalibre-plugin run . find --library ~/Calibre\ Library --answer by=isbn`
or `dalibre-plugin run . merge a.epub b.epub`, or on books of a library
with `--library DIR --book 12 --book 40`: without a window, the
answers to your forms come from `--answer` (as from an AI agent), and
`dalibre.interactive` is false. Your code is the same either way.

## Runtime and errors

Plugins run in [moejs](https://github.com/MoeForge/moejs), an ES2023
JavaScript engine written in Go and built into Dalibre. A plugin gets the
standard APIs of the web and a part of Node's, and Dalibre's rules on top:
the hosts it may reach, its own folders, and a time limit per call.

### Modules

`main.js` (or `main.ts`, set with `"main"` in `plugin.json`) is an ES
module, run once when Dalibre loads the plugin. It exports the functions
Dalibre calls:

```js
// @ts-check
import { toResult } from "./lib/map.js";   // your own files, relative paths

/** @param {Dalibre.Query} query @returns {Promise<Dalibre.Page>} */
export async function search({ text, page, cursor }) { … }

/** @param {Dalibre.Result} item @param {Dalibre.FormatName} format */
export async function resolve(item, format) { … }   // optional
```

`export default { search, resolve }` and CommonJS
(`module.exports = { search }`, `exports.search = …`) work too.

- **Imports** are relative paths to `.js`, `.mjs`, `.ts` or `.mts` files
  in your plugin's folder. As in TypeScript, `"./lib.js"` finds `lib.ts`
  when there is no `lib.js`, and `"./lib"` finds `lib.ts` or `lib.js`.
- **Node's modules** `node:fs`, `node:fs/promises` and `node:buffer`
  (or `fs`, `fs/promises`, `buffer`) can be imported. See
  [your folders](#your-folders).
- Package names (`"lodash"`) can't be imported; bundle them in (see
  [npm packages](#typescript-and-npm-packages)).
- Modules are **strict mode**: writing to a read-only property (anything
  in `dalibre`) throws, as in browsers.
- Your functions may be `async`.

#### State

Keep state across calls in module variables: a token, a cache.

```js
let token = "";
async function signIn() {
  if (token) return token;
  const res = await fetch("https://api.example.com/token", { method: "POST", … });
  return (token = (await res.json()).token);
}
```

It lasts until Dalibre quits or reloads plugins, **or until a call runs
out of time**, after which your module is loaded afresh. What must
survive goes in [your data folder](#your-folders).

### What you can use

| | |
|---|---|
| Network | `fetch`, `Request`, `Response`, `Headers`, `AbortController`, `AbortSignal` |
| Streams | `ReadableStream`, `WritableStream`, `TransformStream`, `TextDecoderStream`, `TextEncoderStream` |
| Text and data | `URL`, `URLSearchParams`, `TextEncoder`, `TextDecoder`, `atob`, `btoa`, `structuredClone`, typed arrays, `Buffer` |
| Time | `setTimeout`, `setInterval`, `setImmediate` and their `clear…`, `queueMicrotask`, `performance.now()` |
| Files | `node:fs`, `node:fs/promises` over [your folders](#your-folders) |
| Formatting | `Intl` (dates and numbers in English, Chinese, Japanese, Korean, German, French, Spanish, Portuguese, Russian and Italian) |
| Logging | `console.log`, `info`, `warn`, `error`, `debug`: to Dalibre's log and the CLI's stderr, with secret settings masked |
| Dalibre | [the `dalibre` object](#the-dalibre-object) |

They behave as in browsers and Node. What differs is below.

#### fetch

```js
const res = await fetch("https://api.example.com/search?q=dune", {
  headers: { Accept: "application/json" },
});
if (!res.ok) throw dalibre.error("unavailable", `example.com answered ${res.status}.`);
const data = await res.json();
```

- Only hosts in [`permissions.hosts`](#hosts), redirects included, and
  not [the local network](#local-network) unless you ask for it.
- At most **50 requests per call**, 10 redirects per request, and
  **16 MB per response**. Books are downloaded by Dalibre (return their
  URL, or a [`resolve`](#resolve) request), not fetched.
- **Cookies** are kept as a browser keeps them, for your plugin alone and
  in memory, and sent with later requests and with Dalibre's downloads.
  Sign in by posting a form once, and stay signed in until the plugin is
  loaded again.
- Dalibre sets the `User-Agent` (`Dalibre <id>/<version>`).
- Requests run in parallel: `Promise.all` over several fetches works.
- A refused request, a network failure or a response over the limit
  rejects with a `TypeError` (`"fetch failed"`) whose `cause` says why. If
  you don't catch it, Dalibre counts it as a `permission` error (your
  plugin asked for a host it didn't declare) or `unavailable` (the
  network).
- Requests a call leaves running end with the call.

#### Timers

Timers work as in Node. A call ends when the promise your export returns
settles; until then, Dalibre runs your timers, fetches and file
operations. So you can wait:

```js
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
for (let attempt = 0; ; attempt++) {
  const res = await fetch(url);
  if (res.status !== 429 || attempt === 2) return res;
  await sleep(1000 * 2 ** attempt);   // back off, then try again
}
```

- The call's **60 seconds** include the time spent waiting.
- A timer you leave running after the call settles waits, and fires
  during your plugin's next call. Clear what you don't need.
- An exception thrown in a timer's callback fails the call in progress.
- A promise that nothing can settle any more (no timer, fetch or file
  operation left) ends the call with "never settled".

#### Your folders

Through `node:fs`, your plugin sees two folders of its own, and nothing
else:

- `/data` keeps what must survive restarts: a sign-in token, a cache, a
  downloaded word list, [file settings](#file-settings).
- `/work` holds the files [`process`](#process) is handed, one folder a
  call, emptied when the call ends.

```js
import { readFile, writeFile, mkdir } from "node:fs/promises";

async function cachedToken() {
  try {
    return await readFile("/data/token", "utf8");
  } catch {
    const token = await signIn();
    await writeFile("/data/token", token);
    return token;
  }
}
```

- Relative paths are relative to `/data`. Nothing else can be reached:
  other paths, `..` and links that lead out fail with `EACCES`.
- `/data` holds **64 MB**; a write once it's full fails with `ENOSPC`.
- Only your plugin sees them. Uninstalling the plugin deletes them.
- They are in a hidden `.data/<id>` folder in Dalibre's plugins folder.
  With `dalibre-plugin`, it is `.data` in your plugin's folder, which
  `pack` leaves out.
- Not there: streams (`createReadStream`), file handles (`fs.open`,
  `FileHandle`), `watch`. Read and write whole files.

#### What's not there

- `process`, `require`, and Node's other modules (`path`, `crypto`,
  `child_process`, `net`…).
- The DOM: no `document`, `window`, `DOMParser`. Parse HTML or XML with
  regular expressions or a small bundled parser; the built-in Project
  Gutenberg plugin reads OPDS (Atom XML) with a few regular expressions.
- `crypto` (WebCrypto), `Blob`, `FormData`, `CompressionStream`,
  `WebAssembly`, `Temporal`. Bundle a pure-JavaScript library when you
  need hashing, ciphers or zip.

### The dalibre object

What only Dalibre can give you:

| | |
|---|---|
| `dalibre.settings` | The user's values for your [settings](#settings), by key; defaults where unset. Read-only strings. |
| `dalibre.error(kind, message?)` | An `Error` Dalibre understands. See [errors](#errors). |
| `dalibre.plugin` | `{ id, version }` from your manifest. |
| `dalibre.api` | The plugin API version of the running Dalibre. |

Everything in the `dalibre` object is frozen.

### TypeScript and npm packages

Write `main.ts` and set `"main": "main.ts"` in `plugin.json`: Dalibre
runs TypeScript as it is, dropping the types, so error messages and stack
traces point into your `.ts` files. TypeScript that changes what the code
does at run time can't be used: `enum`, namespaces with values,
constructor parameter properties (`constructor(private x: T)`),
`import x = require()`, decorators. Types aren't checked when the plugin
runs; check them in your editor.

For types, put [`dalibre.d.ts`](/sdk/dalibre.d.ts) and
[`jsconfig.json`](/sdk/jsconfig.json) (or a `tsconfig.json` with the same
options) next to your main module. It uses TypeScript's `WebWorker`
library for `fetch`, `URL` and the timers; for `node:fs` and `Buffer`, add
`@types/node` (`npm i -D @types/node`). In plain JavaScript, `// @ts-check`
at the top of `main.js` has your editor check JSDoc types:

```js
// @ts-check
/** @param {Dalibre.Query} query @returns {Promise<Dalibre.Page>} */
export async function search(query) { … }
```

npm packages are bundled in, as long as they need nothing from the list
[above](#whats-not-there). Pure-JavaScript libraries do: `fflate` for
zip, `@noble/ciphers` and `node-forge` for ciphers. Leave Node's modules
to Dalibre with `--external`:

```sh
esbuild main.ts --bundle --format=esm --target=es2023 --external:'node:*' --outfile=main.js
```

`self` is the global object, as in a Web Worker, for libraries that look
for it.

Then set `"main"` back to `main.js` and ship the bundle: Dalibre has no
build step.

### Errors

Throw `dalibre.error(kind, message)` so Dalibre can tell the user what to
do:

| kind | When | Dalibre |
|---|---|---|
| `auth` | Credentials missing, expired or refused | Offers your plugin's settings |
| `rateLimited` | The service asks you to slow down (429) | "Try again in a while" |
| `unavailable` | The service is down or answers oddly | "Try again later" |
| `notFound` | The book or file no longer exists | Says so |
| `invalid` | The query can't be searched (too short…) | Shows your message |

```js
if (res.status === 401) throw dalibre.error("auth", "The API key was refused.");
if (res.status === 429) throw dalibre.error("rateLimited");
if (text.length < 3) throw dalibre.error("invalid", "Type at least 3 letters.");
```

- Anything else thrown (a `TypeError`, a plain `Error`) is reported as a
  **bug in the plugin**, with your message; the stack goes to the log.
- A `fetch` you don't catch counts as `unavailable` (the network) or
  `permission` (a host you didn't declare).
- Messages are shown to users: write them for readers, in a sentence.

## Permissions and limits

Plugins are code from strangers, so Dalibre holds them to what they
declare in `plugin.json`, and users see all of it before they install.

### Hosts

Every request, redirect, download and cover must be to a host in
`permissions.hosts`, over `http` or `https`.

```json
"permissions": { "hosts": ["api.example.com", "*.cdn.example.com"] }
```

- `*.example.com` matches any subdomain, **not** `example.com` itself.
  List both if you need both.
- Redirects are checked at every hop. A download that redirects to a CDN
  needs the CDN listed.
- Credentials in URLs (`https://user:pw@…`) are refused; send them in
  headers.

### Local network

Without `"localNetwork": true`, addresses on the user's computer or
network (`127.0.0.1`, `192.168.x.x`, `10.x.x.x`, `fe80::`…) are refused
**after** name resolution, so listing a host that resolves to one doesn't
help.

Ask for it only when the plugin is for a server at home, such as a
calibre content server or an OPDS catalog on a NAS. Users are warned at
install.

### Library

`"library": "read"` lets a metadata source see the title, authors,
identifiers and language of the books the user looks up with it, and a
file type plugin's [hooks](#hooks) the details of the books they run for,
and nothing else. The install dialog says so. A book source doesn't need
it.

`"library": "write"` lets hooks change those details, without asking each
time. The install dialog shows it as a warning. Even then a plugin never
writes the library itself: it returns what to change, and Dalibre writes
it. A metadata source returns candidates; the user picks the fields, and
only *Save* writes.

### Consent

Users grant your permissions when they install. The install dialog
shows your name, author, description, what the plugin adds (Get books,
Metadata, Book files), and every host and permission it asks for. A file
type plugin is shown as reaching the user's books' files.

Updates are held to the same consent:

- An update asking for the **same or less** installs silently.
- An update asking for **more** (a new host, the local network, library
  access) is installed but **stays off** until the user grants it again.

So list the hosts you need up front: `*.cdn.example.com` rather than one
CDN node.

### Limits

| Limit | |
|---|---|
| One call (`search`, `list`, `get`, `resolve`, `identify`…) | 60 seconds; then the script is stopped |
| One `process` call | 10 minutes |
| Memory | Not limited, as calibre doesn't limit its plugins |
| Requests per call | 50 |
| One `fetch` response | 16 MB (files go through the download) |
| Redirects per request | 10 |
| `/data` | 64 MB |
| A file setting's file | 16 MB |
| Results per page | 100 |
| Metadata candidates | 20 |
| Collections | 12 |
| Download | 2 GB |
| Cover | 10 MB |
| Scripts | 4 MB, all modules together |
| Package | 32 MB unpacked, 1000 files |
| Settings | 32 |
| Filters | 4, of 1 to 64 options each |

Calls into one plugin run one at a time; different plugins run in
parallel. A plugin that throws or times out only fails that call. The
next one runs normally (after a timeout, with the module loaded afresh).

## The dalibre-plugin tool

`dalibre-plugin` runs your plugin exactly as Dalibre does, without the
app: the same interpreter, checks, permissions and limits. It needs
Node, or nothing at all as a binary from a
[Dalibre release](https://github.com/djyde/dalibre/releases/latest).

```sh
npm i -g dalibre-plugin        # or put npx in front of each command
```

| Command | |
|---|---|
| `init <dir> --id ID --name NAME` | Makes what [dalibre-plugin-template](https://github.com/djyde/dalibre-plugin-template) holds: `plugin.json`, `main.js`, the types and schema, `.gitignore`, and a GitHub workflow that checks it on each push and releases it on a `v*` tag |
| `check .` | Validates `plugin.json`, loads the script, lists its exports and what it asks for |
| `dev .` | Adds the folder to the running Dalibre, which reloads it as you save, and prints each reload with its errors. Needs Dalibre's MCP server on |
| `search . "dune"`, `get`, `download`, `collections`, `list` | Calls a book source as the Get books page does; `--json` prints exactly what Dalibre gets |
| `identify . "Dune" --author "Frank Herbert"` | What Fetch metadata shows |
| `process . book.epub` | Runs a file type plugin on a file |
| `run . find --library DIR` | Runs an action; `--answer key=value` answers its forms |
| `pack .` | Makes `<id>-<version>.dalibre-plugin` |
| `docs [topic]`, `types [Name]` | This guide by topic, and the API's types, for the tool's version |

`--set key=value` gives a setting; otherwise the values saved in
Dalibre for your plugin's id are used. Logs go to stderr. Each version
of the tool is the Dalibre release of the same number, and holds your
plugin to what that release runs: `dalibre-plugin version`.

## Publishing

### Packages

A `.dalibre-plugin` file is a zip of your plugin's folder, with
`plugin.json` at its root, named `<id>-<version>.dalibre-plugin`.
`dalibre-plugin pack .` makes it, leaving out hidden files and
`node_modules`.

Users can install the file directly. Publish it
as a GitHub release asset, with your source next to it.

### Installing from GitHub

Users can install straight from your repository: **Plugins ›
Install from GitHub**, with `owner/repo` or a github.com link. They pick
one of your releases (the latest by default) or a branch:

- A **release** installs its `.dalibre-plugin` asset. A release without
  one installs the repository as tagged.
- A **branch** installs the repository at the branch's latest commit. So
  keep `plugin.json` at the repository's root, or in a `plugin` folder.
  There is no build step: commit what `main.js` imports, bundled output
  included.

A typical release, with the workflow [the template](https://github.com/djyde/dalibre-plugin-template) and `init`
give you:
bump `version` in `plugin.json`, then tag it. The workflow checks the
plugin, makes sure the tag matches the version, packs it and creates the
release.

```sh
git tag v1.2.0 && git push origin v1.2.0
```

Without the workflow:

```sh
npx dalibre-plugin pack .
gh release create v1.2.0 com.example.books-1.2.0.dalibre-plugin --generate-notes
```

### Updates

Dalibre checks for updates at launch and once a day: a newer release
that isn't a pre-release, or a new commit on the branch followed.

- An update that asks for **no new permissions** can install by itself.
- One that asks for **more** waits for the user.
- **Bump `version` in each release**, or it won't be seen as newer.

### The Marketplace

**Plugins › Marketplace** (the toolbar's Plugins button) lists plugins users install with a
click: the plugins in Dalibre's [marketplace/](https://github.com/djyde/dalibre/tree/main/marketplace)
folder, and the community's. To list yours, open a pull request adding
your repository to
[marketplace/community.json](https://github.com/djyde/dalibre/blob/main/marketplace/community.json):

```json
{ "repo": "ann/dalibre-example-books", "category": "sources", "calibre": "Example Books" }
```

- `category` is one of `library`, `metadata`, `epub`, `language`,
  `sources`; `calibre`, optional, names the calibre plugin yours does the
  work of, so calibre's users find it.
- Your repository needs a release, as above. The Marketplace lists its
  latest, checked with `dalibre-plugin check`, and picks up new releases
  within a day.
- Installing still shows users what your plugin asks for; updates follow
  the same rules as above.

### Compatibility

Plugin API 1 is a promise:

- Within API 1, Dalibre only **adds**: new optional fields in what you
  receive, new optional fields you may return, new functions on
  `dalibre`. Nothing you rely on changes or goes away.
- Check for a new feature before using it:
  `if (dalibre.somethingNew) { … }`.
- Fields you return that Dalibre doesn't know yet are ignored with a
  warning, never an error, so returning extra data is safe.
- A breaking change will be API 2, announced in Dalibre's release notes,
  with API 1 plugins kept running alongside it for a transition period.
- A plugin declaring a newer `api` than the user's Dalibre isn't loaded,
  and the user is told to update Dalibre; so is one whose `dalibre` asks
  for a newer Dalibre.

**Moving from `dalibre.fetch`.** Earlier versions of API 1 had
`dalibre.fetch` and `dalibre.log`; they are gone, for the standard
`fetch` and `console`. Replace `dalibre.fetch(` with `fetch(`, and await
the body: `res.json()` and `res.text()` now return promises
(`await res.json()`).

### Guidelines

Users see your name, author, description and what you ask for before
they install. A plugin that asks for little, says what it does and links
its source gets installed.

- **Only sources you may use.** Publish plugins for catalogues whose
  terms allow it: public-domain libraries, stores and services the user
  has an account with, the user's own servers. Dalibre doesn't host,
  review or endorse plugins.
- **Be polite.** One request per search where the API allows it. Cache
  what doesn't change, in module variables. Honour `429` by throwing
  `rateLimited` rather than retrying in a loop.
- **Fill in metadata.** Users see results before they download: authors,
  language, size and cover make the choice easy. Add your own id to
  `identifiers`.
- **Ask only for what you need.** Exact hosts beat wildcards. Avoid
  `localNetwork` unless the plugin is for a server at home.
- **Never log secrets, or send them to a host other than the one they're
  for.**
- **Write errors for readers.** Messages are shown to users, in a
  sentence.

## Type reference

Download [`dalibre.d.ts`](/sdk/dalibre.d.ts) next to your code. In plain
JavaScript, add `// @ts-check` to `main.js` and refer to these types in
JSDoc (`@param {Dalibre.Query} query`). In TypeScript, they're global.
`npx dalibre-plugin types <Name>` prints any of them.

### Exports

What Dalibre calls. A book source implements `Source`; a metadata source
`MetadataSource`; a file type plugin `FileType`; one with actions
`Actions`; a plugin with several
capabilities, each of them.

```ts
interface Source {
  /** The user searched, or asked for the next page. */
  search(query: Query): Page | Promise<Page>;
  /** With "collections": true: what to browse while the search box is empty. At most 12. */
  collections?(): Collection[] | Promise<Collection[]>;
  /** With "collections": true: a page of query.collection. */
  list?(query: Query): Page | Promise<Page>;
  /** The user opened a result. Formats and extra you leave out are kept from item. */
  get?(item: Result): Result | Promise<Result>;
  /** Where to download item as format, when format.url won't do. */
  resolve?(item: Result, format: FormatName): DownloadRequest | Promise<DownloadRequest>;
}

interface MetadataSource {
  /** Candidates for book, best first; at most 20. [] when nothing matches. */
  identify(book: Book): Candidate[] | Promise<Candidate[]>;
}

interface FileType {
  /** A book's file: return null, the new file's path, or { path, report }. */
  process(file: File): Processed | Promise<Processed>;
  /** With onPostImport, onPostAdd, onPostDelete: after the library changed. */
  postimport?(event: HookEvent): HookResult | Promise<HookResult>;
  postadd?(event: HookEvent): HookResult | Promise<HookResult>;
  postdelete?(event: HookEvent): HookResult | Promise<HookResult>;
}

interface Actions {
  /** action is the id of the action chosen. */
  run(action: string, input: ActionInput): ActionResult | null | void | Promise<ActionResult | null | void>;
}
```

### Book sources

#### Query

```ts
interface Query {
  /** What the user typed, trimmed. May be empty. */
  text: string;
  /** The page asked for, from 1. */
  page: number;
  /** The cursor the previous page returned; "" on the first page. */
  cursor: string;
  /** The value of every filter plugin.json declares, by key. */
  filters: Record<string, string>;
  /** list only: the id of the collection asked for. */
  collection?: string;
}
```

#### Page

```ts
interface Page {
  /** At most 100. [] when nothing is found. */
  items: Result[];
  /** Whether the user can ask for the next page. */
  hasMore?: boolean;
  /** Handed back as query.cursor for the next page. */
  cursor?: string;
}
```

#### Collection

```ts
/** A shelf to browse while the search box is empty: "Most popular". */
interface Collection {
  id: string;
  title: string;
}
```

#### Result

```ts
/** A book the source found. Only id, title and formats are required. */
interface Result {
  /** Unique within this source, at most 256 characters. */
  id: string;
  title: string;
  authors?: string[];
  /** Plain text or simple HTML. */
  description?: string;
  publisher?: string;
  /** ISO 639: "en", "eng", "zh-CN". */
  language?: string;
  /** "2006", "2006-01", "2006-01-02" or an RFC 3339 time. */
  pubdate?: string;
  series?: string;
  seriesIndex?: number;
  tags?: string[];
  /** As calibre keeps them. Use your plugin's id as a key for its own id. */
  identifiers?: Record<string, string>;
  /** An image URL on a host in permissions.hosts. */
  cover?: string;
  /** The book's page on your site; the detail panel links to it. */
  url?: string;
  /** More lines for the detail panel, at most 12. */
  fields?: Field[];
  /** At least one. */
  formats: Format[];
  /** Anything JSON; handed back unchanged to resolve. */
  extra?: unknown;
}

/** A line of the detail panel: { label: "Rights", value: "Public domain" }. */
interface Field {
  label: string;
  value: string;
}
```

#### Format

```ts
type FormatName =
  | "EPUB" | "KEPUB" | "AZW3" | "AZW" | "MOBI" | "PDF" | "FB2" | "DJVU"
  | "CBZ" | "CBR" | "CB7" | "TXT" | "RTF" | "DOCX" | "ODT" | "HTMLZ"
  | "LIT" | "PRC" | "PDB" | "LRF" | "SNB" | "TCR";

interface Format {
  format: FormatName;
  /** Bytes, if known; shown to the user. */
  size?: number;
  /** Where to download it. Leave it out and export resolve for links made at download time. */
  url?: string;
}
```

#### DownloadRequest

```ts
/** Where Dalibre downloads a file from. */
interface DownloadRequest {
  url: string;
  headers?: Record<string, string>;
  /** The file's name; else the server's, else the title. The extension is fixed to the format. */
  filename?: string;
}
```

### Metadata sources

#### Book

```ts
/**
 * What a metadata source is asked to look up: what the user has for the
 * book, or what they typed in Fetch metadata. Nothing else of the library.
 */
interface Book {
  /** May be empty when identifiers are given. */
  title: string;
  authors: string[];
  /** Lowercase keys: { isbn: "9780765382030", goodreads: "20518872" }. */
  identifiers: Record<string, string>;
  /** ISO 639, when the book has a language. */
  language?: string;
}
```

#### Candidate

```ts
/** A Result without formats. id and title are required; give every field you know. */
type Candidate = Omit<Result, "formats" | "extra">;
```

### File type plugins

```ts
interface File {
  /** Where to read it: a copy of the file in /work, which you may change or write beside. */
  path: string;
  /** The file's own name: "Dune - Frank Herbert.epub". */
  name: string;
  /** Its format, upper case: "EPUB". */
  format: string;
  /** "import": Dalibre is adding it. "manual": the user ran the plugin on the book. */
  reason: "import" | "manual";
  /** From the book menu: the book the file belongs to. */
  book?: Book;
}

/** null leaves the file; a path (in /work or /data) is the new file. */
type Processed = null | string | { path?: string; report?: string[] };

interface HookEvent {
  book: BookInfo;      // id, title, authors, series, seriesIndex, tags, publisher,
                       // pubdate, languages, identifiers, rating, comments, formats
  format?: string;     // postimport: the format added; postdelete: removed
  file?: HookFile;     // postimport: a copy of the file added, in /work
  files?: HookFile[];  // postadd: copies of the new book's files
}

/** null, or the details to change: with permissions.library "write". */
type HookResult = null | { metadata?: Partial<{ title: string; authors: string[];
  series: string; seriesIndex: number; tags: string[]; publisher: string;
  pubdate: string; languages: string[]; identifiers: Record<string, string>;
  rating: number; comments: string }> };
```

### The dalibre object

```ts
declare const dalibre: {
  /** The plugin API version of the running Dalibre. */
  readonly api: number;
  readonly plugin: { readonly id: string; readonly version: string };
  /** The user's values for the settings in plugin.json; defaults when not set. */
  readonly settings: Readonly<Record<string, string>>;
  /** throw dalibre.error("auth", "The API key was refused") */
  error(kind: ErrorKind, message?: string): Error;
  /** In an action's run: asks the user a form; resolves to the answers by key. */
  form(form: Form): Promise<Record<string, string | boolean | string[]>>;
  /** Whether dalibre.form would show in a window (false for an AI agent or the CLI). */
  readonly interactive: boolean;
  /** In an action's run: the job's progress bar. total 0 when unknown. */
  progress(done: number, total: number): void;
  readonly library: {
    /** In an action's run: every book in the library. */
    books(options?: { comments?: boolean }): AsyncGenerator<BookInfo, void, void>;
  };
};

type ErrorKind =
  | "auth"         // credentials missing or refused: Dalibre offers the plugin's settings
  | "notFound"     // the book or file is gone
  | "rateLimited"  // the service asks to slow down
  | "unavailable"  // the service is down
  | "invalid";     // the query can't be searched
```

Everything else (`fetch`, `URL`, the timers, `console`) is the standard
API: see [what you can use](#what-you-can-use).
