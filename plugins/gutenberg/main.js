// @ts-check
// Project Gutenberg, through its own OPDS catalog (www.gutenberg.org/ebooks/*.opds):
// search and browse lists, then each book's page for its details and file sizes.

const SITE = "https://www.gutenberg.org";
const PER_PAGE = 25; // what the catalog returns per page

/** @type {Record<string, string>} the catalog's sort_order for each of our sorts */
const SORTS = { popular: "downloads", new: "release_date", title: "title" };

const COLLECTIONS = [
  { id: "popular", title: "Most popular", sort: "downloads" },
  { id: "new", title: "Recently added", sort: "release_date" },
];

/** @returns {Dalibre.Collection[]} */
export function collections() {
  return COLLECTIONS.map(({ id, title }) => ({ id, title }));
}

/**
 * @param {Dalibre.Query} query
 * @returns {Promise<Dalibre.Page>}
 */
export async function search({ text, page, filters }) {
  return feed(text, filters.language, SORTS[filters.sort] || "downloads", page);
}

/**
 * @param {Dalibre.Query} query
 * @returns {Promise<Dalibre.Page>}
 */
export async function list({ collection, page, filters }) {
  const c = COLLECTIONS.find((c) => c.id === collection);
  if (!c) throw dalibre.error("notFound", `There is no list called ${collection}.`);
  return feed("", filters.language, c.sort, page);
}

/**
 * A page of the catalog's search: books matching text (anything when
 * empty) in language, in sort order.
 * @param {string} text @param {string} language @param {string} sort @param {number} page
 * @returns {Promise<Dalibre.Page>}
 */
async function feed(text, language, sort, page) {
  // The catalog's own query syntax narrows by language: "dickens l.fr".
  const query = [text.trim(), language ? `l.${language}` : ""].filter(Boolean).join(" ");
  let url = `${SITE}/ebooks/search.opds/?sort_order=${sort}&start_index=${(page - 1) * PER_PAGE + 1}`;
  if (query) url += `&query=${encodeURIComponent(query)}`;
  const xml = await fetchText(url);
  /** @type {Dalibre.Result[]} */
  const items = [];
  for (const entry of entries(xml)) {
    // Book entries link to the book's page; the others are lists of
    // authors and subjects matching the query.
    const id = /\/ebooks\/(\d+)\.opds/.exec(tag(entry, "id") || "")?.[1];
    if (!id) continue;
    // The line under the title is the author, or, for a book without one,
    // its release date or download count.
    const author = text_(tag(entry, "content") || "");
    const authors = author && !DATE.test(author) ? [author] : [];
    // With a language asked for, titles end in it: "紅樓夢 (Chinese)".
    let title = text_(tag(entry, "title") || "");
    if (language) title = title.replace(/\s+\([A-Z][a-z]+(?:, [A-Z][a-z]+)*\)$/, "");
    items.push(book(id, title, authors));
  }
  const more = links(xml.split("<entry>")[0]).some((l) => l.rel === "next");
  return { items, hasMore: more };
}

/**
 * Everything the book's page says: description, subjects, language,
 * and the files with their sizes.
 * @param {Dalibre.Result} item
 * @returns {Promise<Dalibre.Result>}
 */
export async function get(item) {
  const xml = await fetchText(`${SITE}/ebooks/${item.id}.opds`);
  const all = entries(xml);
  if (all.length === 0) throw dalibre.error("notFound", `${item.title} is no longer in the catalog.`);
  // The first entry is the edition without images, the last the one with them.
  const entry = all[all.length - 1];
  const content = tag(entry, "content") || "";
  const para = (/** @type {string} */ label) => {
    const m = new RegExp(`<p>\\s*${label}:\\s*([\\s\\S]*?)</p>`).exec(content);
    return m ? text_(m[1]).replace(/\s+/g, " ").trim() : "";
  };
  const authors = [...entry.matchAll(/<author>\s*<name>([\s\S]*?)<\/name>/g)].map((m) => person(text_(m[1])));
  const subjects = [...entry.matchAll(/<category scheme="http:\/\/purl\.org\/dc\/terms\/LCSH" term="([^"]*)"/g)]
    .map((m) => decode(m[1]).replace(/ -- /g, " — "));

  /** @type {Dalibre.Format[]} */
  const formats = [];
  for (const e of all) {
    for (const l of links(e)) {
      if (l.rel !== "http://opds-spec.org/acquisition") continue;
      const f = formatOf(l.href);
      if (f && !formats.some((x) => x.format === f)) {
        formats.push({ format: f, url: l.href, size: Number(l.length) || undefined });
      }
    }
  }
  formats.sort((a, b) => ORDER.indexOf(a.format) - ORDER.indexOf(b.format));

  const released = (tag(entry, "published") || "").slice(0, 10);
  /** @type {Dalibre.Field[]} */
  const fields = [];
  if (released) fields.push({ label: "Released", value: released });
  const downloads = para("Downloads");
  if (downloads) fields.push({ label: "Downloads", value: downloads.replace(/\B(?=(\d{3})+(?!\d))/g, ",") });
  const rights = text_(tag(entry, "rights") || "");
  if (rights) fields.push({ label: "Rights", value: rights.replace(/\.$/, "") });

  return {
    ...book(item.id, text_(tag(entry, "title") || item.title), authors.length ? authors : item.authors || []),
    description: para("Summary").replace(/\s*\(This is an automatically generated summary\.\)\s*$/, ""),
    language: (tag(entry, "dcterms:language") || "").trim(),
    tags: subjects.slice(0, 8),
    fields,
    formats: formats.length ? formats : item.formats,
  };
}

const DATE = /^(?:[A-Z][a-z]{2} \d{1,2}, \d{4}|[\d,]+ downloads?)$/;

// The formats we offer, best first, by the catalog's file names.
const ORDER = ["EPUB", "AZW3", "MOBI"];

/** @param {string} href @returns {Dalibre.FormatName | ""} */
function formatOf(href) {
  if (href.endsWith(".epub3.images")) return "EPUB";
  if (href.endsWith(".kf8.images")) return "AZW3";
  if (href.endsWith(".kindle.images")) return "MOBI";
  return "";
}

/**
 * A result as search shows it: the files are at known places, so the
 * book's page is only read for the detail panel.
 * @param {string} id @param {string} title @param {string[]} authors
 * @returns {Dalibre.Result}
 */
function book(id, title, authors) {
  return {
    id,
    title,
    authors,
    identifiers: { gutenberg: id },
    url: `${SITE}/ebooks/${id}`,
    cover: `${SITE}/cache/epub/${id}/pg${id}.cover.medium.jpg`,
    formats: [
      { format: "EPUB", url: `${SITE}/ebooks/${id}.epub3.images` },
      { format: "AZW3", url: `${SITE}/ebooks/${id}.kf8.images` },
      { format: "MOBI", url: `${SITE}/ebooks/${id}.kindle.images` },
    ],
  };
}

/** @param {string} url @returns {Promise<string>} */
async function fetchText(url) {
  const res = await fetch(url, { headers: { Accept: "application/atom+xml" } });
  if (res.status === 404) throw dalibre.error("notFound", "Project Gutenberg doesn’t have that page.");
  if (res.status === 429) throw dalibre.error("rateLimited");
  if (!res.ok) throw dalibre.error("unavailable", `Project Gutenberg answered ${res.status} ${res.statusText}.`);
  return res.text();
}

// ---- A little Atom reading. The catalog is generated, well-formed XML,
// so regular expressions over it are enough.

/** @param {string} xml */
function entries(xml) {
  return [...xml.matchAll(/<entry>([\s\S]*?)<\/entry>/g)].map((m) => m[1]);
}

/** The inside of the first <name …>…</name>. @param {string} xml @param {string} name */
function tag(xml, name) {
  const m = new RegExp(`<${name}(?:\\s[^>]*)?>([\\s\\S]*?)</${name}>`).exec(xml);
  return m ? m[1] : null;
}

/** @param {string} xml @returns {Record<string, string>[]} */
function links(xml) {
  return [...xml.matchAll(/<link\b([^>]*?)\/?>/g)].map((m) => {
    /** @type {Record<string, string>} */
    const a = {};
    for (const [, k, v] of m[1].matchAll(/([\w:-]+)="([^"]*)"/g)) a[k] = decode(v);
    return a;
  });
}

/** Markup to plain text. @param {string} s */
function text_(s) {
  return decode(s.replace(/<[^>]+>/g, "")).trim();
}

/** @param {string} s */
function decode(s) {
  return s
    .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCodePoint(parseInt(h, 16)))
    .replace(/&#(\d+);/g, (_, d) => String.fromCodePoint(Number(d)))
    .replace(/&quot;/g, '"').replace(/&apos;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}

/** The catalog writes "Austen, Jane"; Dalibre wants "Jane Austen". @param {string} name */
function person(name) {
  const [last, first] = name.split(", ");
  return first ? `${first} ${last}` : name;
}
