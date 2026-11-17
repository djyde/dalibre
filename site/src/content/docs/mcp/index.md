---
title: MCP
description: Let your own AI agents, such as Claude Code, Claude Desktop and Cursor, search and change your Dalibre library over MCP. Setup, examples and every tool. Also available as plain Markdown at /docs/mcp.md.
---

Dalibre serves the open library to AI agents on your computer over the
[Model Context Protocol](https://modelcontextprotocol.io). Your agent can
search your books, read their details and files, edit metadata, add
books and formats, and get books from Get books' sources, while Dalibre
is open.

MCP is in Dalibre 0.2.0 and later.

| What agents can do | Tools |
|---|---|
| Search and read the library | `search_books`, `get_book`, `list_facets` |
| Change the library | `update_books`, `set_cover`, `add_books`, `remove_books`, `add_format`, `remove_format` |
| Use Get books' sources and the metadata plugins | `find_books`, `download_book`, `find_metadata` |
| Write plugins and try them | `list_plugins`, `create_plugin`, `add_plugin`, `reload_plugins`, `test_plugin`, `enable_plugin`, `remove_plugin` |
| Run plugins’ actions | `list_actions`, `run_action` |

## Turn it on

Click the **MCP** button in the toolbar and turn on its switch, or open
Settings › MCP. The server listens on `http://127.0.0.1:8765/mcp`; the
port is in Settings › MCP.

Only programs on this computer can connect, and only with the server's
token. **New token** in Settings › MCP makes a new one at once; agents
set up with the old one need the new setup.

## Connect an agent

Settings › MCP › Connect an agent has the setup for each agent with your
token in it. **Copy** copies it.

### Claude Code

Run it in a terminal:

```sh
claude mcp add --transport http dalibre http://127.0.0.1:8765/mcp --header "Authorization: Bearer <token>"
```

### Claude Desktop

Claude Desktop only starts servers that talk over stdin and stdout, so
it runs Dalibre's own bridge, `Dalibre mcp`, which needs no token. Add
it to `claude_desktop_config.json`, then restart Claude Desktop:

```json
{
  "mcpServers": {
    "dalibre": {
      "command": "/Applications/Dalibre.app/Contents/MacOS/Dalibre",
      "args": ["mcp"]
    }
  }
}
```

That is the path on macOS; the snippet in Settings › MCP has the path on
your computer. When Dalibre isn't running, the bridge still answers the
search and read tools from your last library.

### Cursor

Add it to `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "dalibre": {
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

### Other agents

Any MCP client that speaks Streamable HTTP can connect to
`http://127.0.0.1:8765/mcp` with the header
`Authorization: Bearer <token>`. One that only starts stdio servers can
run `Dalibre mcp`, as Claude Desktop does.

## Examples

Ask in your own words; the agent picks the tools. Under each request is
what it calls.

### Find and read

> What do I have by Ursula K. Le Guin that I haven't rated?

`search_books {"author": "Ursula K. Le Guin"}`, then the books with no
`rating`.

> Which series am I missing books from?

`list_facets {"kind": "series"}`, then
`search_books {"series": "…", "sort": "series"}` for each, comparing
`series_index`.

> Summarize the description of The Left Hand of Darkness.

`search_books {"query": "left hand darkness"}`, then `get_book {"id": 12}`,
whose `comments` is the description as plain text.

### Tidy up metadata

> Tag every Liu Cixin book "Chinese SF" and "Hard SF".

`search_books {"author": "Liu Cixin"}`, then one call for all of them:

```json
update_books {"ids": [3, 4, 9], "patch": {"add_tags": ["Chinese SF", "Hard SF"]}}
```

> The tags "sci-fi", "SciFi" and "Science Fiction" are the same; keep
> "Science Fiction".

`list_facets {"kind": "tags"}`, `search_books {"tag": "sci-fi"}` and
`search_books {"tag": "SciFi"}`, then `update_books` with
`"add_tags": ["Science Fiction"], "remove_tags": ["sci-fi", "SciFi"]`.

> Fill in the missing ISBNs and publishers of my recently added books.

`search_books {"recent": true}`, `find_metadata {"id": 21}` for each,
then `update_books` with the values it trusts:
`{"publisher": "Tor Books", "identifiers": {"isbn": "9780765382030"}}`.

> Use ~/Downloads/piranesi.jpg as the cover of Piranesi.

`set_cover {"id": 7, "path": "/Users/me/Downloads/piranesi.jpg"}`, or
`"url"` for an image on the web.

### Convert formats

Dalibre doesn't convert yet; your agent can, with the tools it has, and
add the result to the book.

> Make an AZW3 of Dune for my Kindle with ebook-convert.

1. `get_book {"id": 5}` gives the EPUB's path in `files`.
2. The agent runs `ebook-convert "<the EPUB's path>" /tmp/Dune.azw3`.
3. `add_format {"id": 5, "path": "/tmp/Dune.azw3"}`

Dalibre copies the file into the book's folder, names it as calibre
does, and the book shows AZW3 at once. The converted file has to be
outside the library folder.

> Replace the PDF of Thinking, Fast and Slow with an EPUB made by
> pandoc, and remove the PDF.

`get_book`, the conversion, `add_format` with the `.epub`, then
`remove_format {"id": 14, "format": "PDF"}`.

### Add books

> Add every e-book in ~/Downloads/books.

`add_books {"paths": ["/Users/me/Downloads/books"]}`. Books already in
the library are reported, not added again.

> Find Pride and Prejudice on Project Gutenberg and add the EPUB.

`find_books {"query": "Pride and Prejudice"}`, then
`download_book {"source": "dalibre.gutenberg", "id": "1342", "format": "EPUB"}`.

### Remove

> Remove the duplicates of Dune, keeping the one with the most formats.

`search_books {"query": "dune"}`, `get_book` for each, then
`remove_books {"ids": [31]}`. Removed books go to the Trash (the Recycle
Bin on Windows), so you can get them back.

### Write a plugin

> Write a Dalibre plugin that gets books from Standard Ebooks, and try
> it.

The agent reads the [plugin docs](/docs/plugins/), then
`create_plugin {"path": "/Users/me/standard-ebooks", "id": "io.github.me.standardebooks"}`,
writes `plugin.json` and `main.js` there, and
`add_plugin {"path": "/Users/me/standard-ebooks"}`. Dalibre asks you to
allow the hosts the plugin reaches; the agent can't allow them for you.
Then it tries it,
`test_plugin {"id": "io.github.me.standardebooks", "call": "search", "text": "dune"}`,
reads the results, the script's logs and errors, fixes the code and
calls `reload_plugins`. The plugin loads from its folder, so it is in
Get books as soon as it works.

## Tools

| Tool | What it does | Read only |
|---|---|---|
| `search_books` | Searches and filters the books by words, author, series, tag, publisher, language, format or recently added; sorted; 50 at a time. | ✓ |
| `get_book` | A book's metadata, description, identifiers, and the paths of its files and cover. | ✓ |
| `list_facets` | Authors, series, tags, publishers, languages or formats, with how many books have each. | ✓ |
| `update_books` | Changes the fields given of one or more books; `add_tags` and `remove_tags` tag many at once. | |
| `set_cover` | Replaces a book's cover with an image file or URL. | |
| `add_books` | Adds e-book files, or every e-book in folders. | |
| `remove_books` | Moves books to the Trash. | |
| `add_format` | Adds a file to a book as a format, or replaces the one it has. | |
| `remove_format` | Deletes one format of a book. | |
| `find_books` | Searches Get books' sources. | |
| `download_book` | Adds a `find_books` result to the library. | |
| `find_metadata` | Looks a book up with the metadata plugins. | |
| `list_plugins` | The plugins: loaded, off, waiting for you to allow them, or failed and why. | |
| `create_plugin` | Starts a plugin in an empty folder from the template. | |
| `add_plugin` | Adds a plugin from its folder; you are asked to allow what it reaches. | |
| `reload_plugins` | Loads the plugins again after an edit. | |
| `test_plugin` | Calls a plugin's `search`, `collections`, `list`, `get`, `resolve`, `identify` or `process` and returns the result, logs and error. | |
| `enable_plugin` | Turns a plugin on or off. | |
| `remove_plugin` | Removes a plugin added from a folder; the folder stays. | |
| `list_actions` | Lists what the plugins can do with books and the library: their actions. | |
| `run_action` | Runs an action and returns what it found and wants changed. Forms are answered with `answers`; nothing is written without `apply`. | |

Settings › MCP lists them too.

## Read only

Agents get every tool by default. Turn off **Let agents edit, add and
remove books** in Settings › MCP and they get only the search and read
tools; the MCP popover then says "Running · read only".

## See what agents did

While an agent works, the MCP button shows it, with its icon or initials
and its name. Its popover lists the agents and their latest calls in
words, such as "Searched “Dune” · 1 book"; click a call to see its
arguments. Dalibre keeps the last 500 calls while it runs.

Edits an agent makes show in the window at once, as your own do.
