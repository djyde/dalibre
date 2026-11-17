# MCP server: design

For Dalibre's developers.

Status: proposed, not started.

## Goals

1. **The user's own agent can work on their library.** Claude Code,
   Claude Desktop, Cursor and others search it, read and edit metadata,
   add books, and convert formats with their own tools.
2. **What the agent changes shows in the window right away.** Edits go
   through the same path the UI's edits take.
3. **Safe.** Only this computer can connect, with a token; the user can
   make it read only; nothing is deleted for good.
4. Dalibre stays one binary with no cgo.

## Where it runs

In the app, like sharing the library over OPDS
([opds.go](../../internal/app/opds.go), `applyShare`): a Streamable HTTP
endpoint at `http://127.0.0.1:<port>/mcp`, started and stopped by a
setting.

| Option | Why not |
|---|---|
| A separate process opening `metadata.db` | The window doesn't watch the database, so it would show stale books; plugins (Get books, metadata) and their consent and keychain secrets live in the app. |

For clients that only start stdio servers, the app binary takes an `mcp`
argument (checked in `main` before `mygo.App.Run`) and bridges stdio to
the running app:

```json
{ "command": "/Applications/Dalibre.app/Contents/MacOS/Dalibre", "args": ["mcp"] }
```

When the app isn't running, the bridge opens the last library with
`calibre.OpenReadOnly` and serves the read tools only.

SDK: [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk),
pure Go.

## Layout

```
internal/mcp/        the server and its tools; no UI
  Host               what the app provides: the library, books, writes, sources
internal/app/mcp.go  implements Host; starts and stops the server from settings
cmd/dalibre/main.go  the "mcp" stdio bridge
```

Reads use `libShared.Load()`, as the OPDS server does, on any goroutine.
Writes call the library on the handler's goroutine, as
`App.saveMetadata` does, then `a.post(reload)` so the window refreshes and
keeps its selection.

## Tools

Results carry `structuredContent` and the same JSON as text. Lists are
compact; `get_book` has the rest.

### Read (always on)

| Tool | |
|---|---|
| `search_books` | `query`, `author`, `series`, `tag`, `publisher`, `language`, `format`, `recent`, `sort`, `desc`, `limit` (50), `offset`; through `catalog.Apply`. Each book: id, title, authors, series and index, tags, formats, rating, added. |
| `get_book(id)` | All metadata, comments as plain text, identifiers, each format's file with its absolute path and size, the cover's path. |
| `list_facets(kind)` | Authors, series, tags, publishers, languages or formats, with counts; from `catalog.Tally`. |

### Write (unless read only)

| Tool | |
|---|---|
| `update_books(ids, patch)` | Only the fields given change; `add_tags` and `remove_tags` for tagging many books. `UpdateMetadata`. |
| `set_cover(id, path \| url)` | `SetCover`. |
| `add_books(paths)` | `Prepare` and `Duplicates.Find`; duplicates are reported, not added. |
| `remove_books(ids)` | To the Recycle Bin. |
| `add_format(id, path)` | Adds a file to a book as a format, or replaces the format it has. `AddFormat`. |
| `remove_format(id, format)` | Deletes one format's file and record. `RemoveFormat`. |

Tools that overwrite or delete (`update_books`, `set_cover`,
`add_format`, both removals) carry `destructiveHint`; reads,
`find_books` and `find_metadata` carry `readOnlyHint`.

`add_format` checks the file before copying it: it exists and is a
regular file, its extension is an e-book format Dalibre knows, it is
under a size limit, and it is not inside the library folder (so one
book's file can't be moved onto another).

### Sources (unless read only)

| Tool | |
|---|---|
| `find_books(query, source?)` | Searches Get books' sources. |
| `download_book(source, id, format?)` | Adds a result of `find_books` (the server keeps the last 1000) to the library, as Get books does. |
| `find_metadata(id, source?)` | Candidates from metadata plugins (`plugin.Identify`); the agent picks one and applies it with `update_books`. |

### Plugins (unless read only)

For an agent that writes a plugin ([docs/plugins](../plugins/README.md));
the server's instructions point it at the published docs.

| Tool | |
|---|---|
| `list_plugins` | Each plugin's state: `loaded`, `disabled`, `needs_approval`, `failed`; and the load errors. |
| `create_plugin(path, id, name?)` | The template (`sdk.Init`) in an empty folder. |
| `add_plugin(path)` | Links the folder, as Plugins › Add plugin › From a folder. |
| `reload_plugins` | `loadPlugins`, and waits for it. |
| `test_plugin(id, call, …)` | Calls a loaded plugin's function; returns the result, the log lines (`Plugin.Tap`) and the error with its kind and stack. |
| `enable_plugin(id, enabled?)` | |
| `remove_plugin(id)` | Linked plugins only. |

The user's consent stays the gate. `add_plugin` shows the consent dialog
and waits for the answer (two minutes, then it reports
`needs_approval`); an edit that asks for more waits unloaded, as any
update does. `test_plugin` only calls loaded plugins, so an agent can't
run code or reach a host the user didn't allow. `add_plugin` refuses an
id another plugin has, which would hand it that plugin's grant and
secrets, and `remove_plugin` leaves installed plugins to the user.

### Later

- `read_book(id, chapter?)`: a book's text, chapter by chapter, for
  summaries and questions.
- `convert_book(id, to)`: once Dalibre converts formats itself.

## Converting formats with the user's agent

Dalibre doesn't convert; the agent does, with whatever it has
(`ebook-convert`, pandoc, a script of its own):

1. `get_book(id)` gives the source file's path.
2. The agent converts it to a file outside the library.
3. `add_format(id, path)` puts the result in the book, named as calibre
   names it, written to `.part` then renamed.
4. Optionally `remove_format(id, format)` drops the original.

The window's book view shows the new format after the reload. The
converted file keeps whatever metadata the converter wrote; Dalibre
doesn't rewrite it.

This needs the agent on the same computer. A remote agent would need the
files as MCP resources (`dalibre://book/{id}/format/EPUB`, base64) and
`add_format` taking content or a URL; books run to tens of MB, so that
waits until someone needs it.

## Safety

- Listens on `127.0.0.1` only and checks `Host` and `Origin` against
  DNS rebinding.
- Each request carries a token kept in the settings; the stdio bridge
  reads it from the same settings file. Other programs and web pages on
  the computer can't call the server.
- Agents get every tool by default; read only takes the write and source
  tools away (`settings.MCP.ReadOnly`). Deleting goes to
  the Recycle Bin.

## UI

Figma "13 · Sharing & AI agents".

- **MCP button** in the toolbar (`AgentIndicator`): a bot with
  a green dot while the server runs, red when it can't start. While an
  agent calls tools, or did in the last 30 seconds, it shows that agent:
  its avatar (`AgentAvatar`: the icon from its handshake, else its
  initials) and name, or "2 agents".
- **MCP popover**: the server's switch, status, gear and address;
  the agents, each with when it last worked; Activity, the last calls in
  words, a call's arguments on click, Clear. The log is in memory, the
  last 500 calls. Read only, when on, says so, with Allow changes.
- **OPDS button** for sharing over OPDS, with its own popover. Both
  are named by their protocol in every language.
- **Settings › MCP**: on and off, address and status, read only or allow
  changes, port and token, and config snippets with Copy for Claude
  Code, Claude Desktop and Cursor, then the tools agents get, by group.

## Order

1. Read tools, HTTP and the stdio bridge.
2. Write tools, including `add_format` and `remove_format`, and the
   permission setting.
3. The MCP button and popover with the call log, the OPDS
   button, and Settings › MCP.
4. Source tools.
5. `read_book`.
