// @ts-check
// Count Pages, ported from kiwidude68's calibre plugin
// (https://github.com/kiwidude68/calibre_plugins/tree/main/count_pages,
// statistics.py) and calibre's word count (src/calibre/utils/wordcount.py).
// GPL-3.0-or-later.

import { bookDocs, bodyText } from "../shared/booktext.js";

const ALGORITHMS = [
  { value: "0", title: "Paragraphs (APNX accurate)" },
  { value: "1", title: "E-book Viewer (calibre)" },
  { value: "2", title: "Adobe Digital Editions (ADE)" },
  { value: "3", title: "Custom (chars per page)" },
];

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  const answers = await dalibre.form({
    submit: "Count",
    remember: true,
    fields: [
      { key: "algorithm", title: "Page count algorithm", type: "select", value: "0", options: ALGORITHMS },
      { key: "chars", title: "Chars per page (custom)", type: "text", value: "1500" },
      { key: "tag", title: "Add a pages:N tag", type: "toggle", value: false },
    ],
  });
  const algorithm = Number(answers.algorithm) || 0;
  const perPage = parseInt(String(answers.chars), 10);
  if (algorithm === 3 && !(perPage > 0)) throw dalibre.error("invalid", "Chars per page must be a number above 0.");

  /** @type {{ id: number, note?: string }[]} */
  const notes = [];
  /** @type {NonNullable<Dalibre.ActionResult["changes"]>} */
  const changes = [];
  let done = 0;
  for (const book of input.books) {
    dalibre.progress(done++, input.books.length);
    const file = input.files?.[book.id];
    if (!file) continue;
    let docs;
    try {
      docs = await bookDocs(file);
    } catch (e) {
      notes.push({ id: book.id, note: `Can't read the ${file.format}: ${e instanceof Error ? e.message : e}` });
      continue;
    }
    const pages = pageCount(docs, algorithm, perPage);
    const words = wordCount(docs.map((d) => bodyText(d.html)).join(" "));
    notes.push({ id: book.id, note: `${fmt(pages)} ${pages === 1 ? "page" : "pages"} · ${fmt(words)} ${words === 1 ? "word" : "words"}` });
    if (answers.tag) {
      const tags = book.tags.filter((t) => !/^pages:\d+$/i.test(t));
      changes.push({ book: book.id, metadata: { tags: [...tags, `pages:${pages}`] } });
    }
  }
  dalibre.progress(done, input.books.length);
  if (notes.length === 0) return { message: "No book has a file to count." };
  /** @type {Dalibre.ActionResult} */
  const result = { groups: [{ title: "Page counts", books: notes }] };
  if (changes.length) result.changes = changes;
  return result;
}

/**
 * statistics.get_page_count.
 * @param {import("../shared/booktext.js").BookDoc[]} docs @param {number} algorithm @param {number} perPage
 */
export function pageCount(docs, algorithm, perPage) {
  if (algorithm === 1) {
    // _get_page_count_calibre: the viewer's text length of the spine, 1000 a page.
    const length = docs.reduce((n, d) => n + [...bodyText(d.html)].length, 0);
    return Math.ceil(length / 1000);
  }
  if (algorithm === 2) {
    // _get_page_count_adobe: each spine file's compressed size, 1024 bytes a page.
    return docs.reduce((n, d) => n + Math.ceil(d.compressed / 1024), 0);
  }
  if (algorithm === 3) {
    // _get_page_count_custom
    return docs.reduce((n, d) => n + Math.floor([...bodyText(d.html)].length / perPage) + 1, 0);
  }
  return accurate(docs);
}

/**
 * _get_page_count_accurate: lines as calibre's APNX "accurate" makes
 * them (a paragraph's start, then every 70 characters in it), 31 a page;
 * at least the "fast count" of 2400 characters a page.
 * @param {import("../shared/booktext.js").BookDoc[]} docs
 */
function accurate(docs) {
  const html = docs.map((d) => d.html).join(" ");
  const numDivs = html.split("<div").length;
  const numParas = html.split("<p").length;
  const splitChar = numParas > numDivs ? "p" : "d";
  let inTag = false;
  let inP = false;
  let checkP = false;
  let closing = false;
  let pCharCount = 0;
  let lines = 0;
  for (const c of html.toLowerCase()) {
    if (checkP) {
      if (c === "/") {
        closing = true;
        continue;
      } else if (c === splitChar) {
        if (closing) inP = false;
        else {
          inP = true;
          lines++;
        }
      }
      checkP = false;
      closing = false;
      continue;
    }
    if (c === "<") {
      inTag = true;
      checkP = true;
      continue;
    } else if (c === ">") {
      inTag = false;
      checkP = false;
      continue;
    }
    if (inP && !inTag) {
      pCharCount++;
      if (pCharCount === 70) {
        lines++;
        pCharCount = 0;
      }
    }
  }
  const count = Math.floor(lines / 31);
  let fast = Math.floor([...html].length / 2400) + 1;
  if (fast > count) fast = Math.floor([...docs.map((d) => bodyText(d.html)).join(" ")].length / 2400) + 1;
  return Math.max(count, fast);
}

/**
 * calibre's get_wordcount: words between whitespace, with every
 * character above U+3000 (CJK) a word of its own.
 * @param {string} text
 */
export function wordCount(text) {
  let asian = 0;
  let words = 0;
  let inWord = false;
  for (const c of text) {
    const asianChar = /** @type {number} */ (c.codePointAt(0)) > 0x3000;
    if (asianChar) asian++;
    if (asianChar || /\s/u.test(c)) inWord = false;
    else if (!inWord) {
      inWord = true;
      words++;
    }
  }
  return words + asian;
}

/** @param {number} n */
function fmt(n) {
  return new Intl.NumberFormat("en").format(n);
}
