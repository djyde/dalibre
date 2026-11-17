// @ts-check
// Convert to KePub: ported from Patrick Gaskin's kepubify
// (https://github.com/pgaskin/kepubify, MIT; kepub/kobo.go, content.go,
// convert.go, opf.go, toc.go, titlepage.go, smartypants.go), the converter
// calibre's KoboTouchExtended driver and KePub Output plugin follow.
//
// Each content document gets Kobo's wrappers (div#book-columns >
// div#book-inner), its style tweaks, and a <span class="koboSpan"
// id="kobo.P.S"> around every sentence, which Kobo's highlights, bookmarks
// and reading stats need.

import { escape, openEpub, save, strFromU8, strToU8 } from "../shared/epub.js";
import { decode, tokenize } from "../shared/markup.js";
import { smartypants } from "../shared/smartypants.js";
import { attr } from "../shared/epub.js";
import { outFile } from "../shared/epubkit.js";

const CSS_KOBO = "div#book-inner { margin-top: 0; margin-bottom: 0;}";
const CSS_HYPHENATE = `* {
    -webkit-hyphens: auto;
    -moz-hyphens: auto;
    hyphens: auto;

    -webkit-hyphenate-limit-after: 3;
    -webkit-hyphenate-limit-before: 3;
    -webkit-hyphenate-limit-lines: 2;
}

h1, h2, h3, h4, h5, h6, td,
h1 *, h2 *, h3 *, h4 *, h5 *, h6 *, td * {
    -moz-hyphens: none !important;
    -webkit-hyphens: none !important;
    hyphens: none !important;
}`;
const CSS_NO_HYPHENATE = `* {
    -moz-hyphens: none !important;
    -webkit-hyphens: none !important;
    hyphens: none !important;
}`;
const CSS_FULLSCREEN = `body {
    margin: 0 !important;
    padding: 0 !important;
}

body>div {
    padding-left: 0.2em !important;
    padding-right: 0.2em !important;
}`;
const WRAP_START = '<div id="book-columns"><div id="book-inner">';
const WRAP_END = "</div></div>";
const DUMMY_ID = "kepubify-titlepage-dummy";
const DUMMY_HREF = `${DUMMY_ID}.xhtml`;
const DUMMY_PAGE =
  '<!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml" lang="en"><head><title></title></head><body><p style="text-align: center; margin: 4em 0; font-size: .7em; font-style: italic;">Page intentionally left blank by kepubify.</p></body></html>';
const VOID = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"]);

/**
 * @typedef {{ smartypants: boolean, css: string[], hyphenate: boolean | null, fullScreenFixes: boolean,
 *   dummyTitlepage: boolean | null }} Options
 */

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  if (action !== "kepub") throw dalibre.error("invalid", `Unknown action ${action}.`);
  const books = input.books.filter((b) => input.files?.[b.id]);
  if (!books.length) throw dalibre.error("invalid", "Converting needs a book with an EPUB.");

  // kepubify's conversion options.
  const a = await dalibre.form({
    submit: "Convert",
    // kepubify's options, kept between runs as KoboTouchExtended keeps its own.
    remember: true,
    fields: [
      { key: "smarten_punctuation", title: "Smarten punctuation", type: "toggle", value: false },
      {
        key: "hyphenate", title: "Hyphenation", type: "select", value: "auto",
        options: [{ value: "auto", title: "As the book says" }, { value: "on", title: "Force enable" }, { value: "off", title: "Force disable" }],
      },
      { key: "fullscreen_reading_fixes", title: "Fullscreen reading fixes", type: "toggle", value: false },
      {
        key: "dummy_titlepage", title: "Dummy titlepage", type: "select", value: "auto",
        options: [{ value: "auto", title: "When needed" }, { value: "add", title: "Always add" }, { value: "no", title: "Never add" }],
      },
      { key: "css", title: "Extra CSS", type: "text", value: "" },
    ],
  });
  const on = (/** @type {string} */ k) => a[k] === true || a[k] === "true";
  /** @type {Options} */
  const opt = {
    smartypants: on("smarten_punctuation"),
    css: String(a.css ?? "").trim() ? [String(a.css).trim()] : [],
    hyphenate: a.hyphenate === "on" ? true : a.hyphenate === "off" ? false : null,
    fullScreenFixes: on("fullscreen_reading_fixes"),
    dummyTitlepage: a.dummy_titlepage === "add" ? true : a.dummy_titlepage === "no" ? false : null,
  };

  /** @type {{ book: number, path: string }[]} */
  const formats = [];
  /** @type {string[]} */
  const already = [];
  for (const [n, book] of books.entries()) {
    dalibre.progress(n, books.length);
    const epub = await openEpub(/** @type {Dalibre.HookFile} */ (input.files?.[book.id]).path);
    if (!convert(epub, opt)) {
      already.push(book.title);
      continue;
    }
    const path = await outFile(`kepub-${book.id}`, book.title, "kepub");
    await save(epub, path);
    formats.push({ book: book.id, path });
  }
  dalibre.progress(books.length, books.length);
  if (!formats.length) return { message: books.length === 1 ? "This book is already a KePub." : "These books are already KePubs." };
  const msg = formats.length === 1 ? "Converted to KePub." : `Converted ${formats.length} books to KePub.`;
  return { formats, message: already.length ? `${msg} ${already.length} already KePub.` : msg };
}

/**
 * Convert of convert.go, on the EPUB in memory. Returns false, leaving it
 * alone, when it is already a KePub.
 * @param {import("../shared/epub.js").Epub} epub @param {Options} opt
 */
export function convert(epub, opt) {
  const m = new ManifestHrefs(epub);
  const cd = contentDocuments(epub);
  if (cd.some((name) => epub.files[name] && hasKoboSpan(strFromU8(epub.files[name])))) return false;

  for (const name of Object.keys(epub.files)) if (fileFilter(name)) delete epub.files[name];

  // OPF, then the dummy titlepage, then the NCX, then the content.
  let opf = transformOPF(epub.opf, m);
  const tp = dummyTitlepage(epub, opf, opt);
  if (tp) {
    opf = tp.opf;
    epub.files[tp.name] = strToU8(DUMMY_PAGE);
  }
  epub.opf = opf;
  epub.files[epub.opfPath] = strToU8(opf);
  if (m.ncx && epub.files[m.ncx]) epub.files[m.ncx] = strToU8(transformNCX(strFromU8(epub.files[m.ncx]), m.ncx, m));

  const kopt = { ...opt };
  for (const name of cd) {
    const data = epub.files[name];
    if (!data) continue;
    let html = strFromU8(data);
    if (html.charCodeAt(0) === 0xfeff) html = html.slice(1);
    epub.files[name] = strToU8(transformContent(html, kopt, (href) => m.fix(name, href)[0]));
  }
  return true;
}

// ---- content (content.go, kobo.go) ----

/**
 * TransformContent: filter, then Kobo's styles, wrappers and spans.
 * @param {string} html @param {Options} opt @param {(href: string) => string} [fixHref]
 */
export function transformContent(html, opt, fixHref) {
  return kobo(filter(tokenize(html)), opt, fixHref);
}

/**
 * filter of content.go: drops Adobe Adept meta elements and o:p / st1:*
 * elements with nothing but whitespace or comments in them, and the style
 * elements Kobo or an earlier conversion added.
 * @param {import("../shared/markup.js").Token[]} tokens
 */
function filter(tokens) {
  tokens = joinRawText(tokens);
  /** @type {import("../shared/markup.js").Token[]} */
  const out = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.kind !== "open") {
      out.push(t);
      continue;
    }
    const name = t.name.toLowerCase();
    const adept = name === "meta" && ["Adept.expected.resource", "Adept.resource"].includes(attr(t.raw, "name"));
    const mso = name === "o:p" || name.startsWith("st1:");
    if (adept || mso) {
      // a void meta or a self-closing tag: nothing left to hold
      if (t.self || (adept && !hasCloseAhead(tokens, i, name))) continue;
      // held: dropped when only whitespace or comments come before its end
      let j = i + 1;
      while (j < tokens.length && (tokens[j].kind === "other" ? tokens[j].raw.startsWith("<!--") : tokens[j].kind === "text" && !/\S/.test(decode(tokens[j].raw)))) j++;
      if (j < tokens.length && tokens[j].kind === "close" && tokens[j].name.toLowerCase() === name) {
        i = j;
        continue;
      }
      out.push(t);
      continue;
    }
    if (name === "style") {
      const id = attr(t.raw, "id");
      const classes = attr(t.raw, "class").split(/\s+/);
      if (id === "kobostylehacks" || classes.includes("kobostylehacks") || classes.some((c) => c.startsWith("kepubify-"))) {
        if (!t.self) {
          let j = i + 1;
          while (j < tokens.length && !(tokens[j].kind === "close" && tokens[j].name.toLowerCase() === "style")) j++;
          i = j;
        }
        continue;
      }
    }
    out.push(t);
  }
  return out;
}

/** @param {import("../shared/markup.js").Token[]} tokens @param {number} i @param {string} name */
function hasCloseAhead(tokens, i, name) {
  for (let j = i + 1; j < tokens.length; j++) {
    const t = tokens[j];
    if (t.kind === "close" && t.name.toLowerCase() === name) return true;
    if (t.kind === "open" || t.kind === "close") return false;
    if (t.kind === "text" && /\S/.test(t.raw)) return false;
  }
  return false;
}

/** Script and style content as one raw text token. @param {import("../shared/markup.js").Token[]} tokens */
function joinRawText(tokens) {
  /** @type {import("../shared/markup.js").Token[]} */
  const out = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    out.push(t);
    const name = t.name.toLowerCase();
    if (t.kind === "open" && !t.self && (name === "script" || name === "style")) {
      let raw = "";
      let j = i + 1;
      while (j < tokens.length && !(tokens[j].kind === "close" && tokens[j].name.toLowerCase() === name)) raw += tokens[j++].raw;
      if (raw) out.push({ kind: "text", raw, name: "#raw", self: false });
      i = j - 1;
    }
  }
  return out;
}

/**
 * The kobo transform of kobo.go.
 * @param {import("../shared/markup.js").Token[]} tokens @param {Options} opt @param {(href: string) => string} [fixHref]
 */
function kobo(tokens, opt, fixHref) {
  let styles = style("kobostylehacks", CSS_KOBO);
  for (const css of opt.css) styles += style("kepubify-extracss", css);
  if (opt.hyphenate === true) styles += style("kepubify-hyphenate", CSS_HYPHENATE);
  else if (opt.hyphenate === false) styles += style("kepubify-nohyphenate", CSS_NO_HYPHENATE);
  if (opt.fullScreenFixes) styles += style("kepubify-fullscreenfixes", CSS_FULLSCREEN);

  /** @typedef {{ name: string, p: boolean, noSpans: boolean, noSmarty: boolean, wrap: boolean, foreign: boolean }} Open */
  /** @type {Open[]} */
  const open = [];
  const st = { para: 0, seg: 0, newPara: false, spans: 0 };
  let inBody = false;
  let headEnd = false;
  let wrapped = false;
  let out = "";

  const spanStart = () => {
    st.spans = 1;
    if (st.newPara) {
      st.newPara = false;
      st.para++;
      st.seg = 0;
    }
    st.seg++;
    return `<span class="koboSpan" id="kobo.${st.para}.${st.seg}">`;
  };

  /** @param {string} name */
  const endTag = (name) => {
    const k = open.map((o) => o.name).lastIndexOf(name);
    let s = "";
    // close anything left open inside, as an HTML parser would
    if (k >= 0) for (const o of open.splice(k).reverse()) if (o.wrap) s += "</span>";
    const l = name.toLowerCase();
    if (l === "head" && !headEnd) {
      headEnd = true;
      s += styles;
    } else if (l === "body" && inBody) {
      inBody = false;
      if (wrapped) s += WRAP_END;
    }
    return s;
  };

  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.kind === "other") {
      out += t.raw;
      continue;
    }
    if (t.kind === "close") {
      out += endTag(t.name) + t.raw;
      continue;
    }
    if (t.kind === "text") {
      const parent = open[open.length - 1];
      if (t.name === "#raw" || !inBody) {
        out += t.raw;
        continue;
      }
      const spans = st.spans >= 0 && !(parent?.noSpans ?? false);
      const smarty = opt.smartypants && !(parent?.noSmarty ?? false);
      if (!spans && !smarty) {
        out += t.raw.replaceAll("�", "");
        continue;
      }
      out += textNode(decode(t.raw), spans, parent?.p ?? false, smarty, spanStart);
      continue;
    }
    // start tag
    const name = t.name.toLowerCase();
    const parent = open[open.length - 1];
    const foreign = (parent?.foreign ?? false) || name === "svg" || name === "math";
    const html = !name.includes(":") && !(parent?.foreign ?? false);
    /** @type {Open} */
    const f = { name: t.name, p: false, noSpans: parent?.noSpans ?? false, noSmarty: parent?.noSmarty ?? false, wrap: false, foreign };
    if (["pre", "code", "script", "style"].includes(name)) f.noSmarty = true;
    let before = "";
    let raw = t.raw;
    if (fixHref && (name === "a" || name === "area")) {
      const href = attr(raw, "href");
      if (href) {
        const fixed = fixHref(href);
        if (fixed !== href) raw = raw.replace(/(\shref\s*=\s*)("[^"]*"|'[^']*')/i, `$1"${escape(fixed)}"`);
      }
    }
    if (inBody) {
      const classes = attr(raw, "class").split(/\s+/);
      if (classes.includes("koboSpan") && st.spans === 0) st.spans = -1;
      if (!(parent?.noSpans ?? false) && st.spans >= 0) {
        if (["script", "style", "pre", "audio", "video", "svg", "math"].includes(name)) f.noSpans = true;
        if (html) {
          if (name === "img") {
            st.newPara = true;
            before = spanStart();
            f.wrap = true;
          } else if (["p", "ol", "ul", "table", "h1", "h2", "h3", "h4", "h5", "h6"].includes(name)) st.newPara = true;
          else if (name === "textarea" || name === "title") f.noSpans = true;
        }
      }
      f.p = html && name === "p";
    } else if (html && name === "body") {
      inBody = true;
      // Wrap the body in Kobo's divs unless it already starts with them.
      let j = i + 1;
      const skipWs = () => {
        while (j < tokens.length && ((tokens[j].kind === "text" && !/\S/.test(decode(tokens[j].raw))) || (tokens[j].kind === "other" && tokens[j].raw.startsWith("<!--")))) j++;
      };
      skipWs();
      const cols = tokens[j];
      let hasWrap = false;
      if (cols && cols.kind === "open" && attr(cols.raw, "id") === "book-columns") {
        j++;
        skipWs();
        const inner = tokens[j];
        hasWrap = !!inner && inner.kind === "open" && attr(inner.raw, "id") === "book-inner";
      }
      out += before + raw;
      if (!hasWrap) {
        out += WRAP_START;
        wrapped = true;
      }
      if (!t.self) open.push(f);
      continue;
    }
    out += before + raw;
    if (t.self || (html && VOID.has(name))) {
      if (f.wrap) out += "</span>";
    } else open.push(f);
  }
  if (inBody && wrapped) out += WRAP_END;
  return out;
}

/** @param {string} cls @param {string} css */
function style(cls, css) {
  const body = /[<&]/.test(css) ? `/*<![CDATA[*/\n${css.replaceAll("]]>", "]]]]><![CDATA[>")}\n/*]]>*/` : css;
  return `<style type="text/css" class="${cls}">${body}</style>`;
}

/**
 * textState of kobo.go: splits a text node into sentences, wraps each in a
 * span, and smartens its punctuation.
 * @param {string} text decoded @param {boolean} spans @param {boolean} p @param {boolean} smarty @param {() => string} spanStart
 */
function textNode(text, spans, p, smarty, spanStart) {
  const emit = (/** @type {string} */ s) => escapeText((smarty ? smartypants(s) : s).replaceAll("�", ""));
  if (!spans) return emit(text);
  let out = "";
  for (const sentence of splitSentences(text)) {
    let span = p;
    if (span) out += spanStart();
    else if (!isOnlyUnicodeSpace(sentence)) {
      span = true;
      out += spanStart();
    }
    out += emit(sentence);
    if (span) out += "</span>";
  }
  return out;
}

/** @param {string} s */
function escapeText(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

/** @param {string} s */
function isOnlyUnicodeSpace(s) {
  return !/[^\s\u0085]/u.test(s);
}

/**
 * sentenceSplitter of kobo.go, the state machine for
 * ((?ms).*?[\.\!\?]['"”’“…]?\s+): a sentence ends
 * after punctuation, maybe a quote or ellipsis, and ASCII whitespace.
 * @param {string} s @returns {string[]}
 */
export function splitSentences(s) {
  const DEFAULT = 0, PUNCT = 1, EXTRA = 2, AFTER = 3;
  let state = DEFAULT;
  /** @type {string[]} */
  const out = [];
  let start = 0;
  for (let i = 0; i < s.length; ) {
    const cp = /** @type {number} */ (s.codePointAt(i));
    const r = String.fromCodePoint(cp);
    const punct = r === "." || r === "!" || r === "?";
    const extra = r === "'" || r === '"' || r === "”" || r === "’" || r === "“" || r === "…";
    const space = r === "\t" || r === "\n" || r === "\f" || r === "\r" || r === " ";
    if (state === AFTER && !space) {
      out.push(s.slice(start, i));
      start = i;
      state = DEFAULT;
    }
    if (punct) state = PUNCT;
    else if (extra && state === PUNCT) state = EXTRA;
    else if (space && state !== DEFAULT) state = AFTER;
    else state = DEFAULT;
    i += r.length;
  }
  if (start < s.length) out.push(s.slice(start));
  return out;
}

/** hasKoboSpan: a span.koboSpan with a kobo.N.M id in the body. @param {string} html */
function hasKoboSpan(html) {
  if (!html.includes("koboSpan")) return false;
  let body = false;
  for (const t of joinRawText(tokenize(html))) {
    const name = t.name.toLowerCase();
    if (name === "body") body = t.kind === "open";
    else if (body && t.kind === "open" && attr(t.raw, "class").split(/\s+/).includes("koboSpan") && /^kobo\.\d+\.\d+$/.test(attr(t.raw, "id"))) return true;
  }
  return false;
}

// ---- package (convert.go, opf.go, toc.go, titlepage.go) ----

/** transformFileFilter. @param {string} fn */
function fileFilter(fn) {
  const base = fn.slice(fn.lastIndexOf("/") + 1);
  if (["calibre_bookmarks.txt", "iTunesMetadata.plist", "iTunesArtwork.plist", ".DS_Store", ".DS_STORE", "thumbs.db", "Thumbs.db"].includes(base)) return true;
  return fn.startsWith("__MACOSX/");
}

/** epubContentDocuments: XHTML/HTML manifest items, by media type or extension. @param {import("../shared/epub.js").Epub} epub */
function contentDocuments(epub) {
  return epub.manifest
    .filter((i) => i.type === "application/xhtml+xml" || i.type === "text/html" || /\.(htm|html|xhtml)$/i.test(i.href.split("#")[0]))
    .map((i) => epubHref(epub, i.href));
}

/** epubHref: an href's file, decoded unless only the undecoded name exists. @param {import("../shared/epub.js").Epub} epub @param {string} href */
function epubHref(epub, href) {
  const name = pathJoin(epub.opfDir, href);
  let d;
  try {
    d = decodeURIComponent(href);
  } catch {
    return name;
  }
  if (d === href) return name;
  const decoded = pathJoin(epub.opfDir, d);
  if (epub.files[decoded] || !epub.files[name]) return decoded;
  return name;
}

/** path.Join of Go. @param {string} dir @param {string} p */
function pathJoin(dir, p) {
  /** @type {string[]} */
  const out = [];
  for (const s of (dir + "/" + p).split("/")) {
    if (s === "..") out.pop();
    else if (s && s !== ".") out.push(s);
  }
  return out.join("/");
}

/** escapeHref: percent-encodes what a URL can't hold, keeping the rest. @param {string} href */
export function escapeHref(href) {
  const bytes = new TextEncoder().encode(href);
  let out = "";
  let changed = false;
  const isHex = (/** @type {number} */ c) => (c >= 48 && c <= 57) || (c >= 97 && c <= 102) || (c >= 65 && c <= 70);
  for (let i = 0; i < bytes.length; i++) {
    const c = bytes[i];
    const ch = String.fromCharCode(c);
    if (/[a-zA-Z0-9]/.test(ch) || "-._~!$&'()*+,;=:@/?#".includes(ch) || c >= 0x80 || (c === 37 && i + 2 < bytes.length && isHex(bytes[i + 1]) && isHex(bytes[i + 2]))) continue;
    changed = true;
  }
  if (!changed) return href;
  for (const ch of href) {
    const c = ch.codePointAt(0) ?? 0;
    if (c >= 0x80 || /[a-zA-Z0-9]/.test(ch) || "-._~!$&'()*+,;=:@/?#%".includes(ch)) out += ch;
    else out += `%${c.toString(16).toUpperCase().padStart(2, "0")}`;
  }
  // a % that doesn't start an escape is encoded too
  return out.replace(/%(?![0-9A-Fa-f]{2})/g, "%25");
}

/** escapeSegment. @param {string} name */
function escapeSegment(name) {
  return escapeHref(name.replace(/%/g, "%25").replace(/\?/g, "%3F").replace(/#/g, "%23").replace(/\//g, "%2F"));
}

/** hasScheme. @param {string} href */
function hasScheme(href) {
  return /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(href);
}

/** manifestHrefs of toc.go: each manifest file's href, as the manifest encodes it. */
class ManifestHrefs {
  /** @param {import("../shared/epub.js").Epub} epub */
  constructor(epub) {
    this.opf = epub.opfPath;
    /** @type {Map<string, string>} */
    this.hrefs = new Map();
    /** @type {Map<string, string>} */
    this.lower = new Map();
    /** @type {Record<string, string>} */
    const ids = {};
    let ncx = "";
    this.nav = "";
    for (const tag of epub.opf.match(/<(?:\w+:)?item\b[^>]*>/g) ?? []) {
      const raw = attr(tag, "href");
      if (!raw) continue;
      const href = escapeHref(raw);
      const name = epubHref(epub, href);
      if (!this.hrefs.has(name)) {
        this.hrefs.set(name, href);
        const l = name.toLowerCase();
        this.lower.set(l, this.lower.has(l) ? "" : name);
      }
      const id = attr(tag, "id");
      if (id) ids[id] = name;
      if (!ncx && attr(tag, "media-type") === "application/x-dtbncx+xml") ncx = name;
      if (!this.nav && attr(tag, "properties").split(/\s+/).includes("nav")) this.nav = name;
    }
    const toc = attr(/<(?:\w+:)?spine\b[^>]*>/.exec(epub.opf)?.[0] ?? "", "toc");
    this.ncx = ids[toc] || ncx;
  }

  /** Fix: href in the file base, encoded like the manifest. @param {string} base @param {string} href @returns {[string, boolean]} */
  fix(base, href) {
    const hash = href.indexOf("#");
    const p = hash < 0 ? href : href.slice(0, hash);
    if (p === "" || p.startsWith("/") || p.includes("?") || hasScheme(p)) return [href, true];
    const dir = base.includes("/") ? base.slice(0, base.lastIndexOf("/")) : ".";
    const names = [pathJoin(dir === "." ? "" : dir, p)];
    try {
      const d = decodeURIComponent(p);
      if (d !== p) names.unshift(pathJoin(dir === "." ? "" : dir, d));
    } catch {
      // keep it as it is
    }
    let fixed = "";
    for (const lower of [false, true]) {
      for (let name of names) {
        if (lower) name = this.lower.get(name.toLowerCase()) ?? "";
        const h = name && this.hrefs.get(name);
        if (h) {
          fixed = h;
          break;
        }
      }
      if (fixed) break;
    }
    if (!fixed) return [href, false];
    const opfDir = this.opf.includes("/") ? this.opf.slice(0, this.opf.lastIndexOf("/")) : ".";
    if (dir !== opfDir) fixed = rebaseHref(opfDir, fixed, dir);
    if (hash >= 0) fixed += href.slice(hash);
    return [fixed, true];
  }
}

/** rebaseHref: an href relative to from, made relative to to, keeping its encoding. @param {string} from @param {string} href @param {string} to */
function rebaseHref(from, href, to) {
  /** @param {string} dir */
  const split = (dir) => (dir === "." || dir === "" ? [] : dir.split("/").filter(Boolean).map((name) => ({ name, raw: escapeSegment(name) })));
  const abs = split(from);
  for (const raw of href.split("/")) {
    if (raw === "" || raw === ".") continue;
    if (raw === "..") {
      abs.pop();
      continue;
    }
    let name = raw;
    try {
      name = decodeURIComponent(raw);
    } catch {
      // keep it
    }
    abs.push({ name, raw });
  }
  const dir = split(to);
  let i = 0;
  while (i < dir.length && i < abs.length - 1 && dir[i].name === abs[i].name) i++;
  let b = "../".repeat(dir.length - i);
  if (!b && i < abs.length && abs[i].raw.includes(":")) b = "./";
  return b + abs.slice(i).map((s) => s.raw).join("/");
}

/**
 * transformOPF: the cover gets cover-image, calibre's timestamp and its
 * bkp contributor go, manifest hrefs are percent-encoded, guide hrefs
 * follow the manifest.
 * @param {string} opf @param {ManifestHrefs} m
 */
function transformOPF(opf, m) {
  let coverID = "cover";
  const coverMeta = /<(?:\w+:)?meta\b[^>]*\bname\s*=\s*["']cover["'][^>]*>/.exec(opf);
  if (coverMeta) coverID = attr(coverMeta[0], "content") || "cover";
  let coverDone = false;
  // calibre leftovers, with the whitespace before them
  opf = opf.replace(/[ \t]*\r?\n?[ \t]*<(?:\w+:)?meta\b[^>]*\bname\s*=\s*["']calibre:timestamp["'][^>]*?(?:\/>|>[\s\S]*?<\/(?:\w+:)?meta>)/g, "");
  opf = opf.replace(/[ \t]*\r?\n?[ \t]*<dc:contributor\b([^>]*)>[\s\S]*?<\/dc:contributor>/g, (all, attrs) => (/\b(?:opf:)?role\s*=\s*["']bkp["']/.test(attrs) ? "" : all));
  opf = opf.replace(/<((?:\w+:)?item)\b[^>]*>/g, (tag) => {
    let out = tag;
    if (!coverDone && attr(tag, "id") === coverID) {
      coverDone = true;
      const props = attr(tag, "properties");
      if (!props.split(/\s+/).includes("cover-image")) {
        out = props ? out.replace(/(\sproperties\s*=\s*)(["'])([^"']*)\2/, (_, a, q, v) => `${a}${q}${v} cover-image${q}`) : out.replace(/\s*(\/?)>$/, ' properties="cover-image"$1>');
      }
    }
    const href = attr(out, "href");
    const esc = escapeHref(href);
    if (href && esc !== href) out = out.replace(/(\shref\s*=\s*)("[^"]*"|'[^']*')/, `$1"${escape(esc)}"`);
    return out;
  });
  opf = opf.replace(/<((?:\w+:)?reference)\b[^>]*>/g, (tag) => {
    const href = attr(tag, "href");
    if (!href) return tag;
    const [fixed] = m.fix(m.opf, href);
    return fixed === href ? tag : tag.replace(/(\shref\s*=\s*)("[^"]*"|'[^']*')/, `$1"${escape(fixed)}"`);
  });
  return opf;
}

/** transformNCX: content srcs follow the manifest's encoding. @param {string} ncx @param {string} name @param {ManifestHrefs} m */
function transformNCX(ncx, name, m) {
  return ncx.replace(/<((?:\w+:)?content)\b[^>]*>/g, (tag) => {
    const src = attr(tag, "src");
    if (!src) return tag;
    const [fixed] = m.fix(name, src);
    return fixed === src ? tag : tag.replace(/(\ssrc\s*=\s*)("[^"]*"|'[^']*')/, `$1"${escape(fixed)}"`);
  });
}

/**
 * transformDummyTitlepage: Kobo treats the first spine item specially
 * (no margins, for full-screen covers), so a blank page goes first when
 * the first one isn't a cover or title page.
 * @param {import("../shared/epub.js").Epub} epub @param {string} opf @param {Options} opt
 */
function dummyTitlepage(epub, opf, opt) {
  if (opt.dummyTitlepage === false) return null;
  if (opt.dummyTitlepage === null && !titlepageRequired(epub, opf)) return null;
  const item = `<item id="${DUMMY_ID}" href="${DUMMY_HREF}" media-type="application/xhtml+xml"/>`;
  const itemref = `<itemref idref="${DUMMY_ID}"/>`;
  let out = opf.replace(/(\s*)(<\/(?:\w+:)?manifest>)/, (_, ws, end) => `${ws.replace(/^\r?\n?/, "\n") || "\n"}${item}${ws}${end}`);
  out = out.replace(/(<(?:\w+:)?spine\b[^>]*?(?<!\/)>)(\s*)/, (_, start, ws) => `${start}${ws}${itemref}${ws}`);
  if (out === opf) return null;
  return { opf: out, name: pathJoin(epub.opfDir, DUMMY_HREF) };
}

/** transformDummyTitlepageRequired. @param {import("../shared/epub.js").Epub} epub @param {string} opf */
function titlepageRequired(epub, opf) {
  let idref = "";
  for (const tag of opf.match(/<(?:\w+:)?itemref\b[^>]*>/g) ?? []) {
    if (attr(tag, "linear") !== "no") {
      idref = attr(tag, "idref");
      break;
    }
  }
  if (!idref) return false;
  let href = "";
  for (const tag of opf.match(/<(?:\w+:)?item\b[^>]*>/g) ?? []) {
    if (attr(tag, "id") !== idref) continue;
    const h = attr(tag, "href");
    if (/\.(htm|html|xhtml)$/i.test(h)) href = h;
    else return false;
  }
  if (!href) return false;
  const name = epubHref(epub, href);
  const base = name.slice(name.lastIndexOf("/") + 1).toLowerCase();
  if (base.includes("cover") || base.includes("title")) return false;
  const data = epub.files[name];
  if (!data) return false;
  return !isTitlepage(strFromU8(data));
}

/** isTitlepage: few paragraphs and words, and an image or two. @param {string} html */
function isTitlepage(html) {
  let paras = 0, images = 0, words = 0, word = 0;
  let body = false;
  let skip = "";
  let depth = 0;
  for (const t of joinRawText(tokenize(html))) {
    const name = t.name.toLowerCase();
    if (skip) {
      if (t.kind === "open" && !t.self && name === skip) depth++;
      if (t.kind === "close" && name === skip && --depth === 0) skip = "";
      continue;
    }
    if (t.kind === "open" || t.kind === "close") {
      if (name === "body") {
        body = t.kind === "open";
        continue;
      }
      if (!body || t.kind !== "open") continue;
      const voidish = t.self || VOID.has(name);
      if (name === "p") {
        if (++paras > 4) return false;
        if (!voidish) [skip, depth] = [name, 1];
      } else if (name === "img" || name === "image" || name === "svg") {
        images++;
        if (!voidish) [skip, depth] = [name, 1];
      } else if (["script", "style", "pre", "audio", "video", "math"].includes(name) && !voidish) [skip, depth] = [name, 1];
      continue;
    }
    if (t.kind !== "text" || !body || t.name === "#raw") continue;
    for (const ch of decode(t.raw)) {
      if (/\s/u.test(ch)) {
        if (word > 3) words++;
        word = 0;
      } else word += new TextEncoder().encode(ch).length;
    }
    if (word > 3) words++;
    word = 0;
    if (words > 20) return false;
  }
  return !((images === 0 && words < 5) || images > 4);
}
