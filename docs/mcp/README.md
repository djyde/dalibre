# Using Dalibre with your agents (MCP)

Dalibre serves the open library to AI agents on your computer, such as
Claude Code, Claude Desktop and Cursor, over the
[Model Context Protocol](https://modelcontextprotocol.io). An agent can
search your books, read their details and files, edit metadata, add
books and formats, get books from Get books' sources, and write
plugins and try them, while Dalibre is open.

How it is built is in [design.md](design.md).

- [Turn it on](#turn-it-on)
- [Connect an agent](#connect-an-agent)
- [Examples](#examples)
- [Tools](#tools)
- [Read only](#read-only)
- [See what agents did](#see-what-agents-did)

## Turn it on

Click the **MCP** button in the toolbar (the bot) and turn on the switch,
or open Settings › MCP. The server listens on `http://127.0.0.1:8765/mcp`
(the port is in Settings › MCP) and only programs on this computer that
send its token can connect.

## Connect an agent

Settings › MCP › Connect an agent has the setup for each agent, with the
token in it; **Copy** copies it.

**Claude Code**: run it in a terminal.

```sh
claude mcp add --transport http dalibre http://127.0.0.1:8765/mcp --header "Authorization: Bearer <token>"
```

**Claude Desktop** starts servers on stdin and stdout only, so it runs
Dalibre's own bridge, `Dalibre mcp`, which needs no token. Add it to
`claude_desktop_config.json`, then restart Claude Desktop:

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

When Dalibre isn't running, the bridge still answers the read tools from
the last library, read-only.

**Cursor**: add it to `~/.cursor/mcp.json`.

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

**New token** in Settings › MCP makes a new token at once; agents set up
with the old one need the new setup.

## Examples

Ask in your own words; the agent picks the tools. What it calls is
shown under each.

### Find and read

> What do I have by Ursula K. Le Guin that I haven't rated?

`search_books {"author": "Ursula K. Le Guin"}`, then the books with no
`rating`.

> Which series am I missing books from?

`list_facets {"kind": "series"}`, then `search_books {"series": "…", "sort": "series"}`
for each, comparing `series_index`.

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
then `update_books` with the values the agent trusts:
`{"publisher": "Tor Books", "identifiers": {"isbn": "9780765382030"}}`.

> Use this picture as the cover of Piranesi: ~/Downloads/piranesi.jpg

`set_cover {"id": 7, "path": "/Users/me/Downloads/piranesi.jpg"}`, or
`"url"` for an image on the web.

### Convert formats

Dalibre doesn't convert; your agent does, with the tools it has, then
adds the result to the book.

> Make an AZW3 of Dune for my Kindle with ebook-convert.

1. `get_book {"id": 5}`: the EPUB's path in `files`.
2. In a terminal:
   `ebook-convert "<the EPUB's path>" /tmp/Dune.azw3`
3. `add_format {"id": 5, "path": "/tmp/Dune.azw3"}`

The file is copied into the book's folder and named as calibre names it;
the book shows AZW3 at once. The converted file has to be outside the
library folder.

> Replace the PDF of Thinking, Fast and Slow with an EPUB made by pandoc,
> and remove the PDF.

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

The agent reads the [plugin docs](../plugins/README.md), then
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

| Tool | | Read only |
|---|---|---|
| `search_books` | Searches and filters the books: query, author, series, tag, publisher, language, format, recently added; sorted; 50 at a time. | ✓ |
| `get_book` | A book's metadata, description, identifiers, and the paths of its files and cover. | ✓ |
| `list_facets` | Authors, series, tags, publishers, languages or formats, with how many books have each. | ✓ |
| `update_books` | Changes the fields given of one or more books; `add_tags` and `remove_tags` for tagging many. | |
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
remove books** in Settings › MCP and they get the read tools only; the
MCP popover then says "Running · read only", with **Allow changes**.

## See what agents did

While an agent works, the MCP button shows it: its icon (or initials)
and name. Its popover lists the agents and their latest calls in words,
as "Searched “Dune” · 1 book"; click a call for its arguments. The list
holds the last 500 calls while Dalibre runs.
