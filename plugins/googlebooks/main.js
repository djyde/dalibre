// @ts-check
// Google Books: https://developers.google.com/books/docs/v1/using
// Without a key, requests share Google's anonymous quota, which is often
// used up; the user's own key (the apiKey setting) gives them their own.

const API = "https://www.googleapis.com/books/v1";
const MAX = 12; // candidates returned

/**
 * Candidates for the book, best first: by Google id or ISBN when the book
 * has one, else by title and authors.
 * @param {Dalibre.Book} book
 * @returns {Promise<Dalibre.Result[]>}
 */
export async function identify(book) {
  const ids = book.identifiers;
  if (ids.google && /^[\w-]{6,32}$/.test(ids.google)) {
    const item = await getJSON(`${API}/volumes/${encodeURIComponent(ids.google)}`, true);
    if (item?.volumeInfo) return [candidate(item)];
  }
  const isbn = (ids.isbn || "").replace(/[^0-9Xx]/g, "");
  if (isbn.length === 10 || isbn.length === 13) {
    const found = await search(`isbn:${isbn}`);
    if (found.length > 0) return found.slice(0, MAX).map(candidate);
  }
  const title = book.title.trim();
  if (!title) return [];
  const author = book.authors[0] || "";
  // Fielded first; Google's free-text search when that finds nothing, as
  // titles in the library are often longer or shorter than Google's.
  let found = await search(fielded(title, author));
  if (found.length === 0) found = await search(author ? `${title} ${author}` : title);
  return found
    .map((v, i) => ({ v, score: score(v.volumeInfo, title, book.authors, book.language) - i * 0.01 }))
    .sort((x, y) => y.score - x.score)
    .slice(0, MAX)
    .map(({ v }) => candidate(v));
}

/** intitle: and inauthor: terms for each word. @param {string} title @param {string} author */
function fielded(title, author) {
  const words = (/** @type {string} */ s) => s.replace(/["():[\]]+/g, " ").split(/\s+/).filter(Boolean).slice(0, 8);
  return [...words(title).map((w) => `intitle:${w}`), ...words(author).map((w) => `inauthor:${w}`)].join(" ");
}

/** The volumes matching q. @param {string} q @returns {Promise<any[]>} */
async function search(q) {
  const url = `${API}/volumes?q=${encodeURIComponent(q)}&printType=books&maxResults=20`;
  const data = await getJSON(url, false);
  return (data?.items ?? []).filter((/** @type {any} */ v) => v && v.id && v.volumeInfo?.title);
}

/**
 * GET url as JSON, with the user's key; null for a 404 when missingOK.
 * @param {string} url @param {boolean} missingOK
 */
async function getJSON(url, missingOK) {
  const key = (dalibre.settings.apiKey || "").trim();
  /** @type {Record<string, string>} */
  const headers = { Accept: "application/json" };
  if (key) headers["X-Goog-Api-Key"] = key;
  const res = await fetch(url, { headers });
  if (res.status === 404 && missingOK) return null;
  if (!res.ok) throw await failure(res, key !== "");
  try {
    return await res.json();
  } catch {
    throw dalibre.error("unavailable", "Google Books sent something that isn’t JSON.");
  }
}

/**
 * The error for a failed response.
 * @param {Response} res @param {boolean} hasKey
 */
async function failure(res, hasKey) {
  let reason = "";
  try {
    const err = (await res.json())?.error;
    reason = [err?.status, ...(err?.errors ?? []).map((/** @type {any} */ e) => e.reason),
      ...(err?.details ?? []).map((/** @type {any} */ d) => d.reason)].filter(Boolean).join(" ");
  } catch {}
  if (/API_KEY_INVALID|keyInvalid|API_KEY_/.test(reason)) {
    return dalibre.error("auth", "Google refused the API key.");
  }
  if (/SERVICE_DISABLED|accessNotConfigured/.test(reason)) {
    return dalibre.error("auth", "Turn on the Books API for this key in Google Cloud.");
  }
  if (res.status === 429 || /RESOURCE_EXHAUSTED|rateLimitExceeded/.test(reason)) {
    if (!hasKey) return dalibre.error("auth", "Google’s shared quota is used up. Add your own API key.");
    return dalibre.error("rateLimited", "Google Books asks to slow down.");
  }
  if (res.status >= 500) return dalibre.error("unavailable", `Google Books answered ${res.status}.`);
  return new Error(`Google Books answered ${res.status} ${res.statusText}`);
}

/**
 * How well v matches what the user has: title first, then authors and
 * language.
 * @param {any} v @param {string} title @param {string[]} authors @param {string | undefined} language
 */
function score(v, title, authors, language) {
  const want = norm(title);
  const t = norm(v.title);
  const full = v.subtitle ? norm(`${v.title} ${v.subtitle}`) : t;
  let s = 0;
  if (t === want || full === want) s += 4;
  else if (t.startsWith(want) || want.startsWith(t)) s += 1;
  else if (t.includes(want) || want.includes(t)) s += 0.5;
  const theirs = (v.authors ?? []).map(norm);
  if (authors.some((a) => theirs.some((/** @type {string} */ x) => x.includes(norm(a)) || norm(a).includes(x) || sameName(x, norm(a))))) s += 1.5;
  if (language && v.language && v.language.slice(0, 2).toLowerCase() === language.slice(0, 2).toLowerCase()) s += 0.5;
  if (v.imageLinks) s += 0.2;
  if (v.description) s += 0.1;
  return s;
}

/** Lowercase, without punctuation and spaces. @param {string} s */
function norm(s) {
  return String(s ?? "").toLowerCase().normalize("NFKC").replace(/[\s\p{P}\p{S}]+/gu, "");
}

/** "liucixin" and "cixinliu" are the same person. @param {string} a @param {string} b */
function sameName(a, b) {
  return a.length > 3 && a.length === b.length && [...a].sort().join("") === [...b].sort().join("");
}

/**
 * A Google volume as a candidate.
 * @param {any} item
 * @returns {Dalibre.Result}
 */
function candidate(item) {
  const v = item.volumeInfo;
  /** @type {Record<string, string>} */
  const identifiers = { google: item.id };
  const isbns = Object.fromEntries((v.industryIdentifiers ?? []).map((/** @type {any} */ i) => [i.type, i.identifier]));
  const isbn = isbns.ISBN_13 || isbns.ISBN_10;
  if (isbn) identifiers.isbn = String(isbn);

  /** @type {Dalibre.Field[]} */
  const fields = [];
  if (v.subtitle) fields.push({ label: "Subtitle", value: String(v.subtitle) });
  if (v.averageRating) fields.push({ label: "Rating", value: `${v.averageRating} / 5 (${v.ratingsCount ?? 0} ratings)` });
  if (v.pageCount) fields.push({ label: "Pages", value: String(v.pageCount) });

  return {
    id: item.id,
    title: v.title,
    authors: (v.authors ?? []).map((/** @type {string} */ a) => a.trim()).filter(Boolean),
    description: (v.description || "").trim() || undefined,
    publisher: v.publisher || undefined,
    language: v.language || undefined,
    pubdate: /^\d{4}(-\d{2}(-\d{2})?)?$/.test(v.publishedDate ?? "") ? v.publishedDate : undefined,
    tags: tags(v.categories),
    identifiers,
    cover: v.imageLinks ? cover(item.id) : undefined,
    url: `https://books.google.com/books?id=${encodeURIComponent(item.id)}`,
    fields,
  };
}

/**
 * Google's categories as tags: "Fiction / Science Fiction / General"
 * gives "Fiction" and "Science Fiction".
 * @param {string[] | undefined} categories
 */
function tags(categories) {
  /** @type {Set<string>} */
  const out = new Set();
  for (const c of categories ?? []) {
    for (const part of c.split("/").map((p) => p.trim())) {
      if (part && part !== "General") out.add(part);
    }
  }
  return [...out].slice(0, 10);
}

/**
 * The largest front cover Google has. Only asked for when the volume has
 * imageLinks, as otherwise the answer is an "image not available" picture.
 * @param {string} id
 */
function cover(id) {
  return `https://books.google.com/books/content?id=${encodeURIComponent(id)}&printsec=frontcover&img=1`;
}
