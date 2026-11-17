// @ts-check
// Smart punctuation, ported from kepubify's kepub/smartypants.go
// (https://github.com/pgaskin/kepubify, MIT), itself after blackfriday's
// smartypants. Works on decoded text, one text node at a time.

const PUNCT = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~";

/** @param {string} c */
const isSpace = (c) => c === " " || c === "\t" || c === "\n" || c === "\r" || c === "\f" || c === "\v";
/** @param {string} c */
const isPunct = (c) => c !== "" && PUNCT.includes(c);
/** @param {string} c */
const isWordBoundary = (c) => c === "" || isSpace(c) || isPunct(c);
/** @param {string} c */
const lower = (c) => (c >= "A" && c <= "Z" ? c.toLowerCase() : c);

/**
 * Converts a text node's decoded text: quotes, dashes, ellipses, (c), (r),
 * (tm) and fractions. The result is decoded text too.
 * @param {string} text
 */
export function smartypants(text) {
  // As kepubify, the text is escaped first ("&<> as entities) so the
  // rules see what the original implementation saw, and unescaped as it
  // is written.
  const p = text.replace(/[&"<>]/g, (c) => (c === "&" ? "&amp;" : c === '"' ? "&quot;" : c === "<" ? "&lt;" : "&gt;"));
  const state = { single: false, double: false };
  let out = "";
  let i = 0;
  while (i < p.length) {
    const c = p[i];
    if (!"&'(-.13`".includes(c)) {
      out += c;
      i++;
      continue;
    }
    const prev = i > 0 ? p[i - 1] : "";
    const [s, n] = convert(state, prev, p.slice(i, i + 8));
    out += s;
    i += n + 1;
  }
  return out;
}

/**
 * @param {{ single: boolean, double: boolean }} st @param {string} prev @param {string} t
 * @returns {[string, number]}
 */
function convert(st, prev, t) {
  switch (t[0]) {
    case "&":
      if (t.startsWith("&quot;")) return [quote(st, prev, t[6] ?? "", "d"), 5];
      if (t.startsWith("&amp;")) return ["&", 4];
      if (t.startsWith("&lt;")) return ["<", 3];
      if (t.startsWith("&gt;")) return [">", 3];
      return ["&", 0];
    case "'": {
      if (t.length >= 2) {
        const t1 = lower(t[1]);
        if (t1 === "'") return [quote(st, prev, t[2] ?? "", "d"), 1];
        if ((t1 === "s" || t1 === "t" || t1 === "m" || t1 === "d") && (t.length < 3 || isWordBoundary(t[2]))) return ["’", 0];
        if (t.length >= 3) {
          const t2 = lower(t[2]);
          if (((t1 === "r" && t2 === "e") || (t1 === "l" && t2 === "l") || (t1 === "v" && t2 === "e")) && (t.length < 4 || isWordBoundary(t[3]))) return ["’", 0];
        }
      }
      return [quote(st, prev, t[1] ?? "", "s"), 0];
    }
    case "(":
      if (t.length >= 3) {
        const t1 = lower(t[1]);
        const t2 = lower(t[2]);
        if (t1 === "c" && t2 === ")") return ["©", 2];
        if (t1 === "r" && t2 === ")") return ["®", 2];
        if (t.length >= 4 && t1 === "t" && t2 === "m" && t[3] === ")") return ["™", 3];
      }
      return ["(", 0];
    case "-":
      if (t[1] === "-" && t[2] === "-") return ["—", 2];
      if (t[1] === "-") return ["–", 1];
      return ["-", 0];
    case ".":
      if (t[1] === "." && t[2] === ".") return ["…", 2];
      if (t.startsWith(". . .")) return ["…", 4];
      return [".", 0];
    case "1":
    case "3":
      if (isWordBoundary(prev) && t.length >= 3) {
        if (t.startsWith("1/4") && (t.length < 4 || isWordBoundary(t[3]) || (t.length >= 5 && lower(t[3]) === "t" && lower(t[4]) === "h"))) return ["¼", 2];
        if (t.startsWith("1/2") && (t.length < 4 || isWordBoundary(t[3]))) return ["½", 2];
        if (t.startsWith("3/4") && (t.length < 4 || isWordBoundary(t[3]) || (t.length >= 6 && lower(t[3]) === "t" && lower(t[4]) === "h" && lower(t[5]) === "s"))) return ["¾", 2];
      }
      return [t[0], 0];
    case "`":
      if (t[1] === "`") return [quote(st, prev, t[2] ?? "", "d"), 1];
      return ["`", 0];
  }
  return [t[0], 0];
}

/** @param {{ single: boolean, double: boolean }} st @param {string} prev @param {string} next @param {"s" | "d"} q */
function quote(st, prev, next, q) {
  const key = q === "s" ? "single" : "double";
  let open;
  if (prev === "" && next === "") open = !st[key];
  else if (isSpace(prev) && next === "") open = true;
  else if (isPunct(prev) && next === "") open = false;
  else if (next === "") open = false;
  else if (prev === "" && isSpace(next)) open = false;
  else if (isSpace(prev) && isSpace(next)) open = !st[key];
  else if (isPunct(prev) && isSpace(next)) open = false;
  else if (isSpace(next)) open = false;
  else if (prev === "" && isPunct(next)) open = false;
  else if (isSpace(prev) && isPunct(next)) open = true;
  else if (isPunct(prev) && isPunct(next)) open = !st[key];
  else if (isPunct(next)) open = false;
  else if (prev === "") open = true;
  else if (isSpace(prev)) open = true;
  else if (isPunct(prev)) open = true;
  else open = false;
  st[key] = open;
  if (q === "s") return open ? "‘" : "’";
  return open ? "“" : "”";
}
