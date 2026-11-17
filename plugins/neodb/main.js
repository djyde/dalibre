// @ts-check
// NeoDB (neodb.social): an open, federated catalogue of books and more,
// linked to Douban, Goodreads, Open Library and others. Its catalog API
// needs no account: https://neodb.social/developer/

const SITE = "https://neodb.social";
const MAX = 12; // candidates returned

/**
 * Candidates for the book, best first: by ISBN or NeoDB id when the book
 * has one, else by title and authors.
 * @param {Dalibre.Book} book
 * @returns {Promise<Dalibre.Result[]>}
 */
export async function identify(book) {
  const ids = book.identifiers;
  if (ids.neodb && /^[A-Za-z0-9]{10,32}$/.test(ids.neodb)) {
    const item = await getJSON(`${SITE}/api/book/${ids.neodb}`, true);
    if (item) return [candidate(item)];
  }
  const isbn = (ids.isbn || "").replace(/[^0-9Xx]/g, "");
  if (isbn.length === 10 || isbn.length === 13) {
    const found = (await searchBooks(isbn)).filter((b) => b.isbn && sameISBN(b.isbn, isbn));
    if (found.length > 0) return found.slice(0, MAX).map(candidate);
  }
  const title = book.title.trim();
  if (!title) return [];
  const author = book.authors[0] || "";
  /** @type {Map<string, any>} */
  const all = new Map();
  // With the author first: NeoDB's search ranks on every word given.
  for (const q of author ? [`${title} ${author}`, title] : [title]) {
    for (const b of await searchBooks(q)) if (!all.has(b.uuid)) all.set(b.uuid, b);
    if (all.size >= MAX) break;
  }
  const ranked = [...all.values()]
    .map((b, i) => ({ b, score: score(b, title, book.authors, book.language) - i * 0.01 }))
    .sort((x, y) => y.score - x.score);
  return ranked.slice(0, MAX).map(({ b }) => candidate(b));
}

/** @param {string} query @returns {Promise<any[]>} */
async function searchBooks(query) {
  const url = `${SITE}/api/catalog/search?category=book&page=1&query=${encodeURIComponent(query)}`;
  const data = await getJSON(url, false);
  return (data?.data ?? []).filter((/** @type {any} */ b) => b && b.uuid && b.category === "book");
}

/**
 * GET url as JSON; null for a 404 when missingOK.
 * @param {string} url @param {boolean} missingOK
 */
async function getJSON(url, missingOK) {
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (res.status === 404 && missingOK) return null;
  if (res.status === 429) throw dalibre.error("rateLimited", "NeoDB asks to slow down.");
  if (res.status >= 500) throw dalibre.error("unavailable", `NeoDB answered ${res.status}.`);
  if (!res.ok) throw new Error(`NeoDB answered ${res.status} ${res.statusText}`);
  try {
    return await res.json();
  } catch {
    throw dalibre.error("unavailable", "NeoDB sent something that isn’t JSON.");
  }
}

/**
 * How well b matches what the user has: title first, then authors and
 * language.
 * @param {any} b @param {string} title @param {string[]} authors @param {string | undefined} language
 */
function score(b, title, authors, language) {
  const want = norm(title);
  const main = [b.title, b.display_title].filter(Boolean).map(norm);
  const other = [b.orig_title, ...(b.localized_title ?? []).map((/** @type {any} */ t) => t.text)].filter(Boolean).map(norm);
  const all = [...main, ...other];
  let s = 0;
  // The edition's own title counts most: a sequel's orig_title is often the first book's.
  if (main.includes(want)) s += 4;
  else if (other.includes(want)) s += 2;
  else if (all.some((t) => t.startsWith(want) || want.startsWith(t))) s += 1;
  else if (all.some((t) => t.includes(want))) s += 0.5;
  const theirs = (b.author ?? []).map(norm);
  if (authors.some((a) => theirs.some((t) => t.includes(norm(a)) || norm(a).includes(t) || sameName(t, norm(a))))) s += 1.5;
  if (language && (b.language ?? []).some((/** @type {string} */ l) => l.toLowerCase().startsWith(language.slice(0, 2).toLowerCase()))) s += 0.5;
  if (b.cover_image_url && !isMissing(b.cover_image_url)) s += 0.2;
  return s;
}

/** Lowercase, without punctuation and spaces. @param {string} s */
function norm(s) {
  return String(s).toLowerCase().normalize("NFKC").replace(/[\s\p{P}\p{S}]+/gu, "");
}

/** "liucixin" and "cixinliu" are the same person. @param {string} a @param {string} b */
function sameName(a, b) {
  return a.length > 3 && a.length === b.length && [...a].sort().join("") === [...b].sort().join("");
}

/** @param {string} a @param {string} b */
function sameISBN(a, b) {
  const clean = (/** @type {string} */ s) => s.replace(/[^0-9Xx]/g, "").toUpperCase();
  const [x, y] = [clean(a), clean(b)];
  return x === y || (x.length === 13 && y.length === 10 && x.slice(3, 12) === y.slice(0, 9)) ||
    (y.length === 13 && x.length === 10 && y.slice(3, 12) === x.slice(0, 9));
}

/** @param {string} url */
function isMissing(url) {
  return /\/missing\.|\/default\./.test(url);
}

/**
 * A NeoDB book as a candidate.
 * @param {any} b
 * @returns {Dalibre.Result}
 */
function candidate(b) {
  /** @type {Record<string, string>} */
  const identifiers = { neodb: b.uuid };
  if (b.isbn) identifiers.isbn = String(b.isbn);
  for (const { url } of b.external_resources ?? []) {
    let m;
    if ((m = /book\.douban\.com\/subject\/(\d+)/.exec(url))) identifiers.douban ??= m[1];
    else if ((m = /goodreads\.com\/book\/show\/(\d+)/.exec(url))) identifiers.goodreads ??= m[1];
    else if ((m = /openlibrary\.org\/(?:books|works)\/(OL\w+)/.exec(url))) identifiers.openlibrary ??= m[1];
    else if ((m = /books\.google\.[a-z.]+\/books\?id=([\w-]+)/.exec(url))) identifiers.google ??= m[1];
  }
  let pubdate;
  if (b.pub_year) {
    pubdate = String(b.pub_year).padStart(4, "0");
    if (b.pub_month >= 1 && b.pub_month <= 12) pubdate += "-" + String(b.pub_month).padStart(2, "0");
  }
  let cover = b.cover_image_url || undefined;
  if (cover && cover.startsWith("/")) cover = SITE + cover;
  if (cover && (isMissing(cover) || !cover.startsWith(SITE + "/"))) cover = undefined;

  /** @type {Dalibre.Field[]} */
  const fields = [];
  if (b.rating) fields.push({ label: "Rating", value: `${b.rating} / 10 (${b.rating_count} ratings)` });
  if (b.translator?.length) fields.push({ label: "Translators", value: b.translator.join(", ") });
  if (b.pages) fields.push({ label: "Pages", value: String(b.pages) });
  if (b.binding || b.format) fields.push({ label: "Binding", value: String(b.binding || b.format) });
  if (b.subtitle) fields.push({ label: "Subtitle", value: String(b.subtitle) });

  return {
    id: b.uuid,
    title: b.title || b.display_title,
    authors: (b.author ?? []).map((/** @type {string} */ a) => a.trim()).filter(Boolean),
    description: (b.description || b.brief || "").replace(/\r\n?/g, "\n").replace(/^[　 ]+/gm, "").trim() || undefined,
    publisher: b.pub_house || b.publisher?.[0] || undefined,
    language: b.language?.[0] || undefined,
    pubdate,
    series: b.series || undefined,
    tags: (b.tags ?? []).slice(0, 10),
    identifiers,
    cover,
    url: `${SITE}/book/${b.uuid}`,
    fields,
  };
}
