// @ts-check
// Quality Check, ported from the calibre plugin by Grant Drake (GPL v3):
// https://github.com/kiwidude68/calibre_plugins/tree/main/quality_check
// (config.py's menu of checks, check_missing.py, check_metadata.py,
// helpers.py). The checks of the books' files (EPUB, MOBI, cover sizes)
// are left out: a library action doesn't get the files.

import { authorsToList, authorsToSortString, checkIsbn, decode, formattedInitials, titleSort, titlecase } from "../shared/calibre.js";

/**
 * @typedef {{ id: string, title: string, missing?: boolean }} CheckInfo
 * @typedef {{ max: number, exclude: Set<string>, initials: string }} Options
 */

/** In the plugin's menu order. @type {CheckInfo[]} */
const CHECKS = [
  { id: "missing_title", title: "Missing title", missing: true },
  { id: "missing_author", title: "Missing author", missing: true },
  { id: "missing_isbn", title: "Missing ISBN", missing: true },
  { id: "missing_pubdate", title: "Missing pubdate", missing: true },
  { id: "missing_publisher", title: "Missing publisher", missing: true },
  { id: "missing_tags", title: "Missing tags", missing: true },
  { id: "missing_rating", title: "Missing rating", missing: true },
  { id: "missing_languages", title: "Missing languages", missing: true },
  { id: "missing_comments", title: "Missing comments", missing: true },
  { id: "missing_cover", title: "Missing cover", missing: true },
  { id: "missing_formats", title: "Missing formats", missing: true },
  { id: "title_sort", title: "Invalid title sort" },
  { id: "author_sort", title: "Invalid author sort" },
  { id: "isbn", title: "Invalid ISBN" },
  { id: "pubdate", title: "Pubdate set to date added" },
  { id: "dup_isbn", title: "Duplicate ISBN" },
  { id: "dup_series", title: "Duplicate series" },
  { id: "series_gaps", title: "Series gaps" },
  { id: "series_pubdate", title: "Series pubdate order" },
  { id: "excess_tags", title: "Excess tags" },
  { id: "html_comments", title: "HTML comments" },
  { id: "no_html_comments", title: "No HTML comments" },
  { id: "authors_commas", title: "Authors with commas" },
  { id: "authors_no_commas", title: "Authors missing commas" },
  { id: "authors_case", title: "Authors for case" },
  { id: "authors_non_alpha", title: "Authors non alphabetic" },
  { id: "authors_non_ascii", title: "Authors non ascii" },
  { id: "authors_initials", title: "Authors initials" },
  { id: "titles_series", title: "Titles with series" },
  { id: "title_case", title: "Titles for title case" },
];

const DEFAULT = ["missing_title", "missing_author", "missing_isbn", "missing_pubdate", "missing_publisher",
  "missing_tags", "missing_languages", "missing_formats", "isbn", "dup_isbn", "series_gaps", "title_case"];

const INITIALS_MODES = ["A.B.", "A. B.", "A B", "AB"];

/** @param {string} _action @param {Dalibre.ActionInput} _input @returns {Promise<Dalibre.ActionResult>} */
export async function run(_action, _input) {
  const a = await dalibre.form({
    submit: "Check",
    remember: true,
    fields: [
      { key: "checks", title: "Checks", type: "multiselect", value: DEFAULT, options: CHECKS.map((c) => ({ value: c.id, title: c.title })) },
      { key: "maxTags", title: "Most tags", type: "text", value: "5" },
      { key: "excludeTags", title: "Tags not counted", type: "text", value: "" },
      { key: "initials", title: "Initials", type: "select", value: "A.B.", options: INITIALS_MODES.map((m) => ({ value: m })) },
    ],
  });
  const chosen = new Set(/** @type {string[]} */ (a.checks ?? []));
  const checks = CHECKS.filter((c) => chosen.has(c.id));
  if (!checks.length) throw dalibre.error("invalid", "Choose at least one check.");
  const max = parseInt(String(a.maxTags), 10);
  /** @type {Options} */
  const options = {
    max: Number.isFinite(max) && max >= 0 ? max : 5,
    exclude: new Set(String(a.excludeTags ?? "").split(",").map((t) => t.trim()).filter(Boolean)),
    initials: INITIALS_MODES.includes(String(a.initials)) ? String(a.initials) : "A.B.",
  };

  /** @type {Dalibre.BookInfo[]} */
  const books = [];
  let n = 0;
  const comments = ["missing_comments", "html_comments", "no_html_comments"].some((c) => chosen.has(c));
  for await (const book of dalibre.library.books({ comments })) {
    books.push(book);
    if (++n % 100 === 0) dalibre.progress(n, 0);
  }
  dalibre.progress(n, n);

  const groups = [];
  for (const c of checks) {
    const found = CHECK_FNS[c.id](books, options);
    if (found.length) groups.push({ title: `${c.title} (${found.length})`, books: found });
  }
  if (!groups.length) return { message: "No problems" };
  const count = new Set(groups.flatMap((g) => g.books.map((b) => b.id))).size;
  return { message: `${count} of ${n} books have problems`, groups };
}

/** @typedef {{ id: number, note?: string }} Found */

const HTML = ["</b>", "</i>", "</s>", "</u>", "</a>", "</h\\d+>", "</sub>", "</sup>", "</ol>", "</ul>", "</li>"].map((p) => new RegExp(p, "i"));
// "</div>" "</p>" run together as in the plugin's list, which lacks a comma there.
const NO_HTML = ["</div></p>", "</a>", "</h\\d+>", "</b>", "</i>", "</s>", "</u>", "</sub>", "</sup>", "</ol>", "</ul>", "</li>"].map((p) => new RegExp(p, "i"));

/** A check over each book: its note when it fails, false when it passes. @param {(b: Dalibre.BookInfo, o: Options) => string | boolean} test */
const each = (test) => (/** @type {Dalibre.BookInfo[]} */ books, /** @type {Options} */ o) => {
  /** @type {Found[]} */
  const out = [];
  for (const b of books) {
    const r = test(b, o);
    if (r === false) continue;
    out.push(typeof r === "string" && r ? { id: b.id, note: r } : { id: b.id });
  }
  return out;
};

/** The first author that fails, as the note. @param {(a: string, o: Options) => boolean} test */
const eachAuthor = (test) => each((b, o) => authorsToList(b.authors).find((a) => test(a, o)) ?? false);

/** calibre keeps an unset date as the year 101. @param {string | undefined} d */
const hasDate = (d) => !!d && parseInt(d, 10) > 101;

const isbnOf = (/** @type {Dalibre.BookInfo} */ b) => b.identifiers?.isbn ?? "";

/** @type {Record<string, (books: Dalibre.BookInfo[], o: Options) => Found[]>} */
const CHECK_FNS = {
  // check_missing.py: calibre searches.
  missing_title: each((b) => !b.title || b.title === "Unknown"),
  missing_author: each((b) => !b.authors?.length || (b.authors.length === 1 && b.authors[0] === "Unknown")),
  missing_isbn: each((b) => !isbnOf(b)),
  missing_pubdate: each((b) => !hasDate(b.pubdate)),
  missing_publisher: each((b) => !b.publisher),
  missing_tags: each((b) => !b.tags?.length),
  missing_rating: each((b) => !b.rating),
  missing_languages: each((b) => !b.languages?.length),
  missing_comments: each((b) => !b.comments),
  missing_cover: each((b) => !b.hasCover),
  missing_formats: each((b) => !b.formats?.length),

  // check_metadata.py
  title_sort: each((b) => {
    const expected = titleSort(b.title ?? "", b.languages?.[0]);
    return b.titleSort !== expected && `${b.titleSort ?? ""} → ${expected}`;
  }),
  // calibre compares with the sort values in Manage authors; Dalibre has
  // no per-author sort, so each author's computed sort stands in for it.
  author_sort: each((b) => {
    const expected = authorsToSortString(authorsToList(b.authors));
    return (!b.authorSort || b.authorSort !== expected) && `${b.authorSort ?? ""} → ${expected}`;
  }),
  isbn: each((b) => !!isbnOf(b) && !checkIsbn(isbnOf(b)) && isbnOf(b)),
  // The plugin compares the two times; a book's pubdate here is a day.
  pubdate: each((b) => !!b.pubdate && !!b.added && b.pubdate.slice(0, 10) === b.added.slice(0, 10) && b.pubdate.slice(0, 10)),
  dup_isbn: (books) => duplicates(books, (b) => isbnOf(b), (b) => isbnOf(b)),
  dup_series: (books) =>
    duplicates(books, (b) => (b.series ? b.series + (b.seriesIndex ?? 1).toFixed(4) : ""), (b) => `${b.series} #${b.seriesIndex ?? 1}`),
  series_gaps: seriesGaps,
  series_pubdate: seriesPubdate,
  excess_tags: each((b, o) => {
    const tags = new Set((b.tags ?? []).map((t) => t.trim()).filter((t) => !o.exclude.has(t)));
    return tags.size > o.max && `${tags.size} tags`;
  }),
  html_comments: each((b) => !!b.comments && HTML.some((re) => re.test(String(b.comments)))),
  no_html_comments: each((b) => !!b.comments && !NO_HTML.some((re) => re.test(String(b.comments)))),
  authors_commas: eachAuthor((a) => a.includes(",")),
  authors_no_commas: eachAuthor((a) => !a.includes(",")),
  authors_case: eachAuthor((a) => a === a.toUpperCase() || a === a.toLowerCase()),
  authors_non_alpha: eachAuthor((a) => /[^A-Za-z'.,\- ]/.test(decode(a))),
  authors_non_ascii: eachAuthor((a) => decode(a) !== a),
  authors_initials: each((b, o) => {
    for (const a of authorsToList(b.authors)) {
      const expected = formattedInitials(o.initials, a);
      if (expected !== a) return `${a} → ${expected}`;
    }
    return false;
  }),
  titles_series: each((b) => b.title.includes("-") || /^[0-9]/.test(b.title)),
  title_case: each((b) => {
    const t = titlecase(b.title);
    return t !== b.title && `→ ${t}`;
  }),
};

/** Books sharing a key, with note(book). @param {Dalibre.BookInfo[]} books @param {(b: Dalibre.BookInfo) => string} key @param {(b: Dalibre.BookInfo) => string} note */
function duplicates(books, key, note) {
  /** @type {Map<string, Dalibre.BookInfo[]>} */
  const map = new Map();
  for (const b of books) {
    const k = key(b);
    if (k) map.set(k, [...(map.get(k) ?? []), b]);
  }
  return [...map.values()].filter((g) => g.length > 1).flat().map((b) => ({ id: b.id, note: note(b) }));
}

/** check_series_gaps: series whose whole-number indexes skip one, below 1000. @param {Dalibre.BookInfo[]} books */
function seriesGaps(books) {
  /** @type {Map<string, Dalibre.BookInfo[]>} */
  const bySeries = new Map();
  /** @type {Map<string, number[]>} */
  const indexes = new Map();
  for (const b of books) {
    if (!b.series) continue;
    const idx = b.seriesIndex ?? 1;
    bySeries.set(b.series, [...(bySeries.get(b.series) ?? []), b]);
    if (Math.round(idx) === idx && idx > 0) indexes.set(b.series, [...(indexes.get(b.series) ?? []), idx]);
  }
  /** @type {Found[]} */
  const out = [];
  for (const series of [...indexes.keys()].sort((x, y) => x.toLowerCase().localeCompare(y.toLowerCase()))) {
    const list = (indexes.get(series) ?? []).sort((x, y) => x - y);
    const max = list[list.length - 1];
    if (max >= 1000) continue;
    const have = new Set(list);
    const missing = [];
    for (let i = 1; i < max; i++) if (!have.has(i)) missing.push(i);
    if (!missing.length) continue;
    const note = `Missing #${missing.join(", ")}`;
    for (const b of bySeries.get(series) ?? []) out.push({ id: b.id, note });
  }
  return out;
}

/** check_series_pubdate: series whose pubdates go back as the index goes up. @param {Dalibre.BookInfo[]} books */
function seriesPubdate(books) {
  /** @type {Map<string, Dalibre.BookInfo[]>} */
  const bySeries = new Map();
  for (const b of books) if (b.series) bySeries.set(b.series, [...(bySeries.get(b.series) ?? []), b]);
  /** @type {Found[]} */
  const out = [];
  for (const series of [...bySeries.keys()].sort()) {
    const all = bySeries.get(series) ?? [];
    // Books numbered below 1 are taken for anthologies, as the plugin does.
    const dated = all
      .filter((b) => (b.seriesIndex ?? 1) >= 1)
      .map((b) => ({ idx: b.seriesIndex ?? 1, date: hasDate(b.pubdate) ? String(b.pubdate) : "0101" }))
      .sort((x, y) => x.idx - y.idx || (x.date < y.date ? -1 : x.date > y.date ? 1 : 0));
    let last = "0101";
    let wrong = false;
    for (const { date } of dated) {
      if (date < last) wrong = true;
      last = date;
    }
    if (wrong) for (const b of all) out.push({ id: b.id, note: `#${b.seriesIndex ?? 1} · ${hasDate(b.pubdate) ? b.pubdate : "no date"}` });
  }
  return out;
}
