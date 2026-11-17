// @ts-check
// Find Duplicates, ported from the calibre plugin by Grant Drake (GPL v3):
// https://github.com/kiwidude68/calibre_plugins/tree/main/find_duplicates
// (book_algorithms.py, matching.py, duplicates.py).

import { readFile, writeFile } from "node:fs/promises";
import { AUTHOR, TITLE, authorsToList, lengths } from "./matching.js";

const STATE = "/data/exemptions.json";

/**
 * @typedef {{ books: number[][], authors: string[][], mode?: "book" | "author" }} State
 * books and authors are lists of exemption groups, as the plugin's
 * bookExemptions and authorExemptions; mode is the last search's.
 */

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult | null>} */
export async function run(action, input) {
  switch (action) {
    case "find": return find();
    case "exempt": return exempt(input.books);
    case "show-exempt": return showExempt();
    case "clear-exempt": {
      const state = await load();
      const n = state.books.length + state.authors.length;
      if (!n) return { message: "No books are marked not duplicates" };
      await save({ books: [], authors: [], mode: state.mode });
      return { message: `Forgot ${n} not-duplicate ${n === 1 ? "mark" : "marks"}` };
    }
  }
  throw dalibre.error("invalid", `Unknown action ${action}.`);
}

const MATCH = [
  { value: "identical", title: "Identical" },
  { value: "similar", title: "Similar" },
  { value: "soundex", title: "Soundex" },
  { value: "fuzzy", title: "Fuzzy" },
  { value: "ignore", title: "Ignore" },
];

async function find() {
  const a = await dalibre.form({
    submit: "Find duplicates",
    remember: true,
    fields: [
      {
        key: "type", title: "Search", type: "select", value: "titleauthor",
        options: [{ value: "titleauthor", title: "Title and author" }, { value: "identifier", title: "Identifier" }],
      },
      { key: "identifier", title: "Identifier", type: "text", value: "isbn" },
      { key: "title", title: "Title", type: "select", value: "identical", options: MATCH },
      { key: "author", title: "Author", type: "select", value: "identical", options: MATCH },
      { key: "titleSoundex", title: "Title soundex length", type: "text", value: "6" },
      { key: "authorSoundex", title: "Author soundex length", type: "text", value: "8" },
      { key: "languages", title: "Compare languages with titles", type: "toggle", value: false },
      {
        key: "sort", title: "Sort groups", type: "select", value: "title",
        options: [{ value: "title", title: "By title" }, { value: "size", title: "By size" }],
      },
    ],
  });
  const type = String(a.type);
  const titleMatch = String(a.title);
  const authorMatch = String(a.author);
  lengths.title = clampLength(a.titleSoundex, 6);
  lengths.author = clampLength(a.authorSoundex, 8);
  const identifier = String(a.identifier ?? "isbn").trim().toLowerCase() || "isbn";
  if (type === "titleauthor" && titleMatch === "ignore" && authorMatch === "ignore") {
    throw dalibre.error("invalid", "Title and author can't both be ignored.");
  }

  /** @type {Map<number, Dalibre.BookInfo>} */
  const books = new Map();
  let n = 0;
  for await (const book of dalibre.library.books()) {
    books.set(book.id, book);
    if (++n % 100 === 0) dalibre.progress(n, 0);
  }

  const state = await load();
  const authorMode = type === "titleauthor" && titleMatch === "ignore";
  /** @type {Map<string, Set<any>>} candidates by hash: book ids, or author names in author mode */
  const candidates = new Map();
  /** @param {string} key @param {any} v */
  const add = (key, v) => {
    const set = candidates.get(key);
    if (set) set.add(v);
    else candidates.set(key, new Set([v]));
  };
  /** @type {Map<string, Set<number>>} author → books, in author mode */
  const authorBooks = new Map();

  for (const b of books.values()) {
    if (type === "identifier") {
      const value = b.identifiers?.[identifier];
      if (value) add(value, b.id);
    } else if (authorMode) {
      const fn = AUTHOR[authorMatch];
      for (const author of authorsToList(b.authors)) {
        const [hash, rev] = fn(author);
        const set = authorBooks.get(author) ?? new Set();
        set.add(b.id);
        authorBooks.set(author, set);
        add(hash, author);
        if (rev && rev !== hash) add(rev, author);
      }
    } else {
      const lang = a.languages ? (b.languages ?? []).join(",") : undefined;
      const titleHash = TITLE[titleMatch](b.title ?? "", lang);
      const authors = authorsToList(b.authors);
      if (authorMatch !== "ignore" && authors.length) {
        for (const author of authors) {
          const [hash, rev] = AUTHOR[authorMatch](author);
          add(titleHash + hash, b.id);
          if (rev && rev !== hash) add(titleHash + rev, b.id);
        }
      } else {
        add(titleHash, b.id);
      }
    }
  }
  dalibre.progress(n, n);

  // shrink_candidates_map, then sort_candidate_groups.
  let keys = [...candidates.keys()].filter((k) => (candidates.get(k)?.size ?? 0) > 1);
  if (a.sort === "size") {
    const key = (/** @type {string} */ k) => String(candidates.get(k)?.size).padStart(4, "0") + k;
    keys.sort((x, y) => (key(x) < key(y) ? 1 : key(x) > key(y) ? -1 : 0));
  } else if (type === "identifier") {
    const t = (/** @type {string} */ k) => TITLE.similar(books.get([...(candidates.get(k) ?? [])][0])?.title ?? "");
    keys.sort((x, y) => (t(x) < t(y) ? -1 : t(x) > t(y) ? 1 : 0));
  } else {
    keys.sort((x, y) => (x < y ? -1 : x > y ? 1 : 0));
  }

  // convert_candidates_to_groups: drop subsets, partition by exemptions.
  const exemptions = exemptionMap(authorMode ? state.authors : state.books);
  /** @type {{ title: string, books: { id: number, note?: string }[] }[]} */
  const groups = [];
  for (const members of cleanDupGroups(keys.map((k) => /** @type {Set<any>} */ (candidates.get(k))))) {
    for (const part of partition(members, exemptions)) {
      if (part.length < 2) continue;
      /** @type {number[]} */
      const ids = authorMode
        ? [...new Set(part.flatMap((au) => [...(authorBooks.get(au) ?? [])]))].sort((x, y) => x - y)
        : part;
      const first = books.get(ids[0]);
      groups.push({
        title: authorMode ? part.join(" · ") : first?.title ?? "",
        books: ids.map((id) => ({ id, note: note(/** @type {Dalibre.BookInfo} */ (books.get(id))) })),
      });
    }
  }
  await save({ ...state, mode: authorMode ? "author" : "book" });
  if (!groups.length) return { message: "No duplicates" };
  const count = new Set(groups.flatMap((g) => g.books.map((b) => b.id))).size;
  return {
    message: `${groups.length} ${groups.length === 1 ? "group" : "groups"}, ${count} books`,
    groups: groups.slice(0, 2000),
  };
}

/** Marks the books chosen in the results as not duplicates of each other. @param {Dalibre.BookInfo[]} books */
async function exempt(books) {
  if (books.length < 2) throw dalibre.error("invalid", "Choose at least two books.");
  const state = await load();
  if (state.mode === "author") {
    const authors = [...new Set(books.flatMap((b) => authorsToList(b.authors)))].sort();
    if (authors.length < 2) throw dalibre.error("invalid", "These books have the same author.");
    state.authors.push(authors);
    await save(state);
    return { message: `Marked ${authors.length} authors not duplicates` };
  }
  state.books.push([...new Set(books.map((b) => b.id))].sort((x, y) => x - y));
  await save(state);
  return { message: `Marked ${books.length} books not duplicates` };
}

/** show_all_exemptions: every book marked, a group for each mark. */
async function showExempt() {
  const state = await load();
  if (!state.books.length && !state.authors.length) return { message: "No books are marked not duplicates" };
  /** @type {Map<number, Dalibre.BookInfo>} */
  const books = new Map();
  for await (const b of dalibre.library.books()) books.set(b.id, b);
  const groups = [];
  for (const ids of state.books) {
    const present = ids.filter((id) => books.has(id));
    if (present.length) groups.push({
      title: books.get(present[0])?.title ?? "",
      books: present.map((id) => ({ id, note: note(/** @type {Dalibre.BookInfo} */ (books.get(id))) })),
    });
  }
  for (const authors of state.authors) {
    const set = new Set(authors);
    const ids = [...books.values()].filter((b) => authorsToList(b.authors).some((au) => set.has(au)));
    if (ids.length) groups.push({ title: authors.join(" · "), books: ids.map((b) => ({ id: b.id, note: note(b) })) });
  }
  return groups.length ? { groups } : { message: "No books are marked not duplicates" };
}

/** ExemptionMap: each member → the union of its groups, less itself. @param {any[][]} list */
function exemptionMap(list) {
  /** @type {Map<any, Set<any>>} */
  const map = new Map();
  for (const group of list) for (const member of group) {
    const set = map.get(member) ?? new Set();
    for (const other of group) if (other !== member) set.add(other);
    map.set(member, set);
  }
  return map;
}

/** clean_dup_groups: drop the groups that are subsets of others. @param {Set<any>[]} sets */
function cleanDupGroups(sets) {
  const res = sets.map((s) => new Set(s));
  // Python's sort is stable, as is Array.prototype.sort.
  res.sort((x, y) => x.size - y.size);
  return res.filter((a, i) => !res.slice(i + 1).some((b) => [...a].every((v) => b.has(v))));
}

/** partition_using_exemptions, as the plugin does it. @param {Set<any>} items @param {Map<any, Set<any>>} map */
function partition(items, map) {
  const data = [...items].sort((x, y) => (x < y ? -1 : x > y ? 1 : 0));
  const results = [new Set(data)];
  /** @type {any[]} */
  const partitioning = [null];
  for (const dup of data) {
    const ndm = map.get(dup);
    if (!ndm) continue;
    for (let i = 0; i < results.length; i++) {
      const res = results[i];
      if (!res.has(dup)) continue;
      const without = new Set([...res].filter((v) => !ndm.has(v)));
      without.add(dup);
      results[i] = without;
      if (dup === partitioning[i]) continue;
      for (const nd of ndm) {
        if (nd > dup && res.has(nd)) {
          const next = new Set([...res].filter((v) => !ndm.has(v) && v !== dup));
          next.add(nd);
          results.push(next);
          partitioning.push(nd);
        }
      }
    }
  }
  return results
    .filter((r) => r.size > 1)
    .map((r) => [...r].sort((x, y) => (x < y ? -1 : x > y ? 1 : 0)))
    .sort((x, y) => (String(x) < String(y) ? -1 : String(x) > String(y) ? 1 : 0));
}

/** @returns {Promise<State>} */
async function load() {
  try {
    const s = JSON.parse(await readFile(STATE, "utf8"));
    return { books: Array.isArray(s.books) ? s.books : [], authors: Array.isArray(s.authors) ? s.authors : [], mode: s.mode };
  } catch {
    return { books: [], authors: [] };
  }
}

/** @param {State} state */
async function save(state) {
  await writeFile(STATE, JSON.stringify(state));
}

/** @param {unknown} v @param {number} fallback */
function clampLength(v, fallback) {
  const n = parseInt(String(v), 10);
  return Number.isFinite(n) && n >= 1 && n <= 99 ? n : fallback;
}

/** @param {Dalibre.BookInfo} b */
function note(b) {
  const parts = [];
  if (b.authors?.length) parts.push(authorsToList(b.authors).join(" & "));
  if (b.formats?.length) parts.push(b.formats.join(", "));
  if (b.size) parts.push(size(b.size));
  return parts.join(" · ");
}

/** @param {number} n */
function size(n) {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  return Math.max(1, Math.round(n / 1024)) + " KB";
}
