# Dalibre Marketplace

The plugins in Dalibre's **Plugins › Marketplace** (the toolbar's Plugins button): the most used calibre plugins
(by [calibre's download stats](https://plugins.calibre-ebook.com/stats.html)),
ported to Dalibre's plugin API from their source, and the community's
own plugins.

## Plugins

| Plugin | Does | Ported from |
|---|---|---|
| [Find Duplicates](plugins/find-duplicates) | Finds books that look like the same book, by title and author or by ISBN | calibre's Find Duplicates |
| [Quality Check](plugins/quality-check) | Finds books with missing or messy metadata | calibre's Quality Check |
| [Clean Metadata](plugins/clean-metadata) | Cleans up titles, authors and series | calibre's Clean Metadata |
| [Manage Series](plugins/manage-series) | Puts books in a series and numbers them | calibre's Manage Series |
| [Count Pages](plugins/count-pages) | Counts words and estimates pages | calibre's Count Pages |
| [Search the Internet](plugins/search-internet) | Opens a search for a book on Goodreads, Wikipedia and other sites | calibre's Search The Internet |
| [Extract ISBN](plugins/extract-isbn) | Finds the ISBN printed in a book and sets it | calibre's Extract ISBN |
| [Apple Books covers](plugins/apple-books) | High-resolution covers from the Apple Books store | calibre's Apple Books covers |
| [Wikidata](plugins/wikidata) | Book details and covers from Wikidata | calibre's Wikidata |
| [EPUB Merge](plugins/epub-merge) | Merges EPUBs into one new book | calibre's EpubMerge |
| [EPUB Split](plugins/epub-split) | Splits sections of an EPUB off into new books | calibre's EpubSplit |
| [Modify ePub](plugins/modify-epub) | Fixes and cleans up EPUBs in place | calibre's Modify ePub |
| [KePub](plugins/kepub) | Converts EPUBs to Kobo's KePub | kepubify, calibre's KoboTouchExtended |
| [Chinese Text Conversion](plugins/chinese-convert) | Converts EPUBs between Simplified and Traditional Chinese | calibre's Chinese Text Conversion |

Each plugin's README names the files it was ported from and what it
doesn't do yet.

## List your plugin

New to writing one? Start from
[dalibre-plugin-template](https://github.com/djyde/dalibre-plugin-template): its workflow makes the release the
Marketplace needs each time you push a version tag.

Open a pull request adding your plugin's repository to
[community.json](community.json):

```json
{ "repo": "ann/dalibre-example-books", "category": "sources", "calibre": "Example Books" }
```

- `category`: `library`, `metadata`, `epub`, `language` or `sources`.
- `calibre`, optional: the calibre plugin yours does the work of, as
  named on [plugins.calibre-ebook.com](https://plugins.calibre-ebook.com),
  so calibre's users find it.
- Your repository has `plugin.json` at its root (or in a `plugin`
  folder) and at least one release, with a `.dalibre-plugin` package
  (`npx dalibre-plugin pack .`) or without one. If it uses what a newer
  Dalibre added, say so with `"dalibre": ">=0.4.0"`: older ones then
  show it as needing an update rather than offering it. See [Writing
  Dalibre plugins](../docs/plugins/README.md).

The pull request's check downloads your latest release and runs
`dalibre-plugin check` on it. Once merged, the Marketplace lists your
latest release and picks up new ones within a day; users see what your
plugin asks for before they install it.

## How it works

- `src/<name>/` is each plugin's code; `src/shared/` is code they share.
- `plugins/<name>/` is each plugin as Dalibre installs it: `plugin.json`,
  its README, and `main.js`, which `node build.mjs` bundles from `src/`
  (with npm packages) and git ignores.
- The Marketplace workflow (`.github/workflows/marketplace.yml`) checks
  them with the `dalibre-plugin` of the same commit, so a plugin can use
  what Dalibre's plugin API gains in the same pull request. On `main` it
  packs each plugin version not packed yet into the `marketplace`
  branch's `packages/`, and writes `marketplace.json` there from them and
  community.json's plugins; Dalibre reads it from that branch. It runs
  again daily for the community's new releases.

```sh
npm ci
go build -o dalibre-plugin ../cmd/dalibre-plugin
node build.mjs                         # bundle every plugin, or: node build.mjs epub-merge
./dalibre-plugin check plugins/epub-merge
./dalibre-plugin run plugins/epub-merge merge a.epub b.epub -o out
node index.mjs --check                 # check community.json's plugins
```

To change a plugin, edit `src/<name>/` and bump `version` in its
`plugin.json`: a published version is never packed again, and Dalibre
offers the new one as an update. A plugin using what the next Dalibre
adds raises its `"dalibre"` to that version.

## License

GPL-3.0-or-later ([LICENSE.md](LICENSE.md)), as the calibre plugins they
are ported from; KePub is MIT, as kepubify.
