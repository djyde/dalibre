// @ts-check
// Apple Books covers, ported from Jan Larres's calibre plugin
// (https://github.com/janlarres/apple-books-covers, __init__.py), with
// calibre's get_title_tokens and get_author_tokens
// (src/calibre/ebooks/metadata/sources/base.py). GPL-3.0-or-later.
// High-resolution covers from the Apple Books store, through the iTunes
// Search API.

const BASE_URL_LOOKUP = "https://itunes.apple.com/lookup?";
const BASE_URL_SEARCH = "https://itunes.apple.com/search?";

/**
 * A result: its author, title and the full-size artwork.
 * @typedef {{ author: string, title: string, artworkUrl: string, id: string }} Result
 */

/** @param {Dalibre.Book} book @returns {Promise<Dalibre.Candidate[]>} */
export async function identify(book) {
  const maxCovers = Math.max(1, parseInt(dalibre.settings.maxCovers || "5", 10) || 5);
  const country = countryCode(dalibre.settings.country || "US");
  const country2 = dalibre.settings.additionalCountry?.trim() ? countryCode(dalibre.settings.additionalCountry) : null;
  const identifiers = book.identifiers ?? {};

  const titleTokens = [...getTitleTokens(book.title, true)];
  const authorTokens = [...getAuthorTokens(book.authors ?? [], true)];
  const base = { entity: "ebook", limit: String(Math.max(10, maxCovers)), version: "2" };

  /** @type {Result[]} */
  const results = [];
  // The ISBN first.
  if (identifiers.isbn) {
    const params = { isbn: identifiers.isbn, ...base };
    let found = await lookup({ ...params, country });
    if (!found.length && country2) found = await lookup({ ...params, country: country2 });
    results.push(...found);
  }
  // Then a search, in each store.
  if (titleTokens.length) {
    const searches = [await search(authorTokens, titleTokens, { ...base, country })];
    if (country2) searches.push(await search(authorTokens, titleTokens, { ...base, country: country2 }));
    results.push(...roundrobin(searches));
  }
  // Without duplicates, in order; as many as calibre downloads.
  const seen = new Set();
  return results
    .filter((r) => !seen.has(r.artworkUrl) && seen.add(r.artworkUrl))
    .slice(0, maxCovers)
    .map((r) => ({ id: r.id, title: r.title, authors: [r.author], cover: r.artworkUrl }));
}

/** @param {string} s */
function countryCode(s) {
  const c = s.trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(c)) throw dalibre.error("invalid", "Store countries are two-letter codes, such as US or CN.");
  return c;
}

/** @param {Record<string, string>} params */
async function lookup(params) {
  return query(BASE_URL_LOOKUP + new URLSearchParams(params));
}

/**
 * The title alone, then the title and the author, keeping results with
 * every token in their title and author.
 * @param {string[]} authorTokens @param {string[]} titleTokens @param {Record<string, string>} params
 */
async function search(authorTokens, titleTokens, params) {
  const results = await query(BASE_URL_SEARCH + new URLSearchParams({ ...params, term: titleTokens.join(" ") }));
  const filtered = filterResults(results, authorTokens, titleTokens);
  if (filtered.length) return filtered;
  const again = await query(BASE_URL_SEARCH + new URLSearchParams({ ...params, term: `${titleTokens.join(" ")} ${authorTokens.join(" ")}` }));
  return filterResults(again, authorTokens, titleTokens);
}

/** @param {Result[]} results @param {string[]} authorTokens @param {string[]} titleTokens */
function filterResults(results, authorTokens, titleTokens) {
  return results.filter((r) => titleTokens.every((t) => r.title.includes(t)) && authorTokens.every((t) => r.author.includes(t)));
}

/** @param {string} url @returns {Promise<Result[]>} */
async function query(url) {
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (res.status === 403 || res.status === 429) throw dalibre.error("rateLimited", "Apple asks to slow down. Try again in a minute.");
  if (!res.ok) throw dalibre.error("unavailable", `Apple's search answered ${res.status}.`);
  let data;
  try {
    data = await res.json();
  } catch {
    throw dalibre.error("unavailable", "Apple's search sent something that isn't JSON.");
  }
  return (data?.results ?? [])
    .filter((/** @type {any} */ r) => r?.artworkUrl100 && r.trackName)
    .map((/** @type {any} */ r) => ({
      author: String(r.artistName ?? ""),
      title: String(r.trackName),
      // urljoin(artworkUrl100, "10000x10000bb.jpg"): the largest the store has.
      artworkUrl: String(r.artworkUrl100).replace(/[^/]*$/, "10000x10000bb.jpg"),
      id: String(r.trackId ?? r.artworkUrl100),
    }));
}

/** @template T @param {T[][]} lists @returns {T[]} roundrobin("ABC", "D", "EF") → A D E B F C */
function roundrobin(lists) {
  const out = [];
  for (let i = 0; lists.some((l) => i < l.length); i++) for (const l of lists) if (i < l.length) out.push(l[i]);
  return out;
}

/**
 * calibre's Source.get_title_tokens.
 * @param {string | undefined} title @param {boolean} stripSubtitle @param {boolean} [stripJoiners]
 */
export function* getTitleTokens(title, stripSubtitle, stripJoiners = true) {
  if (!title) return;
  if (stripSubtitle) {
    const subtitle = /([([{].*?[)\]}]|[/:\\].*$)/g;
    if (title.replace(subtitle, "").length > 1) title = title.replace(subtitle, "");
  }
  /** @type {[RegExp, string][]} */
  const patterns = [
    // Things like (2010) (Omnibus).
    [/[({[](\d{4}|omnibus|anthology|hardcover|audiobook|audio\scd|paperback|turtleback|mass\s*market|edition|ed\.)[\])}]/gi, ""],
    // Anything with "edition" in parentheses.
    [/[({[].*?(edition|ed.).*?[\]})]/gi, ""],
    // Commas in numbers.
    [/(\d+),(\d+)/gi, "$1$2"],
    // Hyphens after whitespace.
    [/(\s-)/gi, " "],
    // Other special characters. (calibre's pattern puts 《》「」“” after
    // the class by mistake, so it never matches them; they're in it here.)
    [/[:,;!@$%^&*(){}.`~"\s[\]/《》「」“”]/gi, " "],
  ];
  for (const [re, repl] of patterns) title = title.replace(re, repl);
  for (let token of title.split(/\s+/)) {
    token = token.trim().replace(/^"+|"+$/g, "").replace(/^'+|'+$/g, "");
    if (token && (!stripJoiners || !["a", "and", "the", "&"].includes(token.toLowerCase()))) yield token;
  }
}

/**
 * calibre's Source.get_author_tokens: first names first, as "Last, First"
 * is turned around.
 * @param {string[]} authors @param {boolean} onlyFirstAuthor
 */
export function* getAuthorTokens(authors, onlyFirstAuthor) {
  const remove = /[!@#$%^&*()（）「」{}`~"\s[\]/]/g;
  const replace = /[-+.:;,，。；：]/g;
  for (let au of onlyFirstAuthor ? authors.slice(0, 1) : authors) {
    const hasComma = au.includes(",");
    au = au.replace(replace, " ");
    let parts = au.split(/\s+/).filter(Boolean);
    if (hasComma) parts = [...parts.slice(1), ...parts.slice(0, 1)];
    for (let tok of parts) {
      tok = tok.replace(remove, "").trim();
      if ([...tok].length > 2 && !["von", "van", "unknown"].includes(tok.toLowerCase())) yield tok;
    }
  }
}
