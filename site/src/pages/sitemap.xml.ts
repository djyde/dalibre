import type { APIRoute } from "astro";
import { getCollection } from "astro:content";
import { defaultLocale, locales } from "../i18n";
import { href, slugOf } from "../lib/docs-nav";

export const GET: APIRoute = async ({ site }) => {
  const docs = await getCollection("docs");
  const abs = (p: string) => new URL(p, site).href;
  // Each language of the landing page lists all of them, itself included.
  const alternates = [
    ...locales.map((l) => `    <xhtml:link rel="alternate" hreflang="${l.lang}" href="${abs(l.path)}"/>`),
    `    <xhtml:link rel="alternate" hreflang="x-default" href="${abs(defaultLocale.path)}"/>`,
  ].join("\n");
  const home = locales.map((l) => `  <url>\n    <loc>${abs(l.path)}</loc>\n${alternates}\n  </url>`);
  const rest = ["/plugins/", "/changelog/", ...docs.map((d) => href(slugOf(d.id)))].map((p) => `  <url><loc>${abs(p)}</loc></url>`);
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">\n${[...home, ...rest].join("\n")}\n</urlset>\n`,
    { headers: { "Content-Type": "application/xml; charset=utf-8" } },
  );
};
