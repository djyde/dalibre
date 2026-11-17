import type { APIRoute } from "astro";

const site = "https://dalibre.randynamic.org";

export const GET: APIRoute = () =>
  new Response(
    `# Dalibre

> Dalibre is a lightweight desktop e-book manager that reads and writes calibre libraries. It is extended with sandboxed JavaScript plugins (book sources, metadata sources, file type plugins and actions, with a Marketplace of calibre's most used plugins, ported), serves the library to the user's AI agents over MCP, and sends books to KOReader over calibre's wireless device connection.

## Docs

- [Dalibre and KOReader](${site}/docs/koreader.md): sending books from Dalibre to KOReader over Wi‑Fi, as KOReader connects to calibre, as one Markdown file.
- [Dalibre MCP](${site}/docs/mcp.md): connecting AI agents to a Dalibre library over MCP, with examples and every tool, as one Markdown file.
- [Writing Dalibre plugins](${site}/docs/plugins.md): the complete plugin guide and API reference (plugin API 1) as one Markdown file.
`,
    { headers: { "Content-Type": "text/plain; charset=utf-8" } },
  );
