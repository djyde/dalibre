// @ts-check
// Ported from Find Duplicates' matching.py (calibre plugin by Grant Drake,
// GPL v3): https://github.com/kiwidude68/calibre_plugins/tree/main/find_duplicates
//
// calibre's get_udc().decode (Unidecode transliteration) becomes
// decode(): accents are stripped, other scripts are kept as they are.

const IGNORE_AUTHOR_WORDS = new Set(["von", "van", "jr", "sr", "i", "ii", "iii", "second", "third", "md", "phd"]);

/** @param {string} s */
export function decode(s) {
  return s.normalize("NFKD").replace(/\p{M}/gu, "").normalize("NFC");
}

/** @param {string} text */
function fuzzyIt(text) {
  text = text.trim().toLowerCase();
  text = text.replace(/[\[\](){}<>'";,:#]/gi, "");
  text = text.replace(/^(a|the|an)\s+/i, ""); // calibre's title_sort_articles tweak
  text = text.replace(/[-._]/g, " ");
  text = text.replace(/\s+/g, " ");
  return text.trim();
}

/** Knuth's soundex, as matching.py has it. @param {string} name @param {number} length */
export function soundex(name, length = 4) {
  const digits = "01230120022455012623010202";
  let sndx = "";
  let fc = "";
  for (const c of name.toUpperCase()) {
    const code = c.charCodeAt(0);
    if (c.length === 1 && code >= 65 && code <= 90) {
      if (!fc) fc = c;
      const d = digits[code - 65];
      if (!sndx || d !== sndx[sndx.length - 1]) sndx += d;
    }
  }
  sndx = fc + sndx.slice(1);
  sndx = sndx.replace(/0/g, "");
  return (sndx + "0".repeat(length)).slice(0, length);
}

// ---------------------------------------------------------------- titles

/** @param {string} title */
function* titleTokens(title, stripSubtitle = true) {
  if (!title) return;
  if (stripSubtitle) {
    const subtitle = /([\(\[\{].*?[\)\]\}]|[/:\\].*$)/g;
    if (title.replace(subtitle, "").length > 1) title = title.replace(subtitle, "");
  }
  const patterns = /** @type {[RegExp, string][]} */ ([
    [/[({\[](\d{4}|omnibus|anthology|hardcover|paperback|mass\s*market|edition|ed\.)[\])}]/gi, ""],
    [/[({\[].*?(edition|ed.).*?[\]})]/gi, ""],
    [/(\d+),(\d+)/g, "$1$2"],
    [/(\s-)/g, " "],
    [/'(?!s)/g, ""],
    [/[:,;+!@#$%^&*(){}.`~"\s\[\]/]/g, " "],
  ]);
  for (const [re, repl] of patterns) title = title.replace(re, repl);
  title = decode(title);
  for (let token of title.split(/\s+/)) {
    token = token.trim();
    if (token && token.toLowerCase() !== "a" && token.toLowerCase() !== "the") yield token.toLowerCase();
  }
}

/** @typedef {(title: string, lang?: string) => string} TitleFn */

/** @type {Record<string, TitleFn>} */
export const TITLE = {
  identical: (title, lang) => (lang ?? "") + title.toLowerCase(),
  similar: (title, lang) => (lang ?? "") + fuzzyIt(decode(title)),
  soundex: (title, lang) => (lang ?? "") + soundex(TITLE.similar(title), lengths.title),
  fuzzy: (title, lang) => {
    let tokens = [...titleTokens(title)];
    const i = tokens.findIndex((t, i) => i > 0 && ["&", "and", "or", "aka"].includes(t));
    if (i > 0) tokens = tokens.slice(0, i);
    return (lang ?? "") + tokens.join("");
  },
};

export const lengths = { title: 6, author: 8 };

// ---------------------------------------------------------------- authors

/** First names first: "Last, First" is turned round. @param {string} author */
function* authorTokens(author, stripInitials = false) {
  if (!author) return;
  author = author.replace(/,([^\s])/g, ", $1");
  let au = author.replace(/[-+.:;]/g, " ");
  au = decode(au);
  let parts = au.split(/\s+/).filter(Boolean);
  if (au.includes(",")) parts = [...parts.slice(1), ...parts.slice(0, 1)];
  const minLength = stripInitials ? 1 : 0;
  for (let tok of parts) {
    tok = tok.replace(/[,!@#$%^&*(){}`~"\s\[\]/]/g, "").trim();
    if (tok.length > minLength && !IGNORE_AUTHOR_WORDS.has(tok.toLowerCase())) yield tok.toLowerCase();
  }
}

/** @typedef {(author: string) => [string, string | null]} AuthorFn */

/** Each returns the name's hash and, when there is one, the hash with the names swapped. @type {Record<string, AuthorFn>} */
export const AUTHOR = {
  identical: (author) => [author.toLowerCase(), null],
  similar: (author) => {
    let tokens = [...authorTokens(author, true)];
    const hash = tokens.join(" ");
    let rev = null;
    if (tokens.length > 1) {
      tokens = [...tokens.slice(1), ...tokens.slice(0, 1)];
      rev = tokens.join(" ");
    }
    return [hash, rev];
  },
  soundex: (author) => {
    const tokens = [...authorTokens(author)];
    if (tokens.length <= 1) return [soundex(tokens.join("")), null];
    const surnameFirst = [tokens[tokens.length - 1], ...tokens.slice(0, -1)];
    return [soundex(surnameFirst.join(""), lengths.author), soundex(tokens.join(""), lengths.author)];
  },
  fuzzy: (author) => {
    const tokens = [...authorTokens(author)];
    if (!tokens.length) return ["", null];
    if (tokens.length === 1) return [tokens[0], null];
    return [tokens[0][0] + tokens[tokens.length - 1], null];
  },
};

/** @param {string[]} authors */
export function authorsToList(authors) {
  return (authors ?? []).map((a) => a.trim()).filter(Boolean);
}
