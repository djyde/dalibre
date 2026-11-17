// @ts-check
// Chinese Text Conversion, ported from Hopkins's calibre plugin
// TradSimpChinese (https://github.com/Hopkins1/TradSimpChinese: main.py).
// GPL-3.0-or-later. The conversion itself is opencc-js
// (https://github.com/nk2028/opencc-js, Apache-2.0) with OpenCC's
// dictionaries, in place of the plugin's opencc-python.

import { ConverterFactory } from "opencc-js/core";
import fromCn from "opencc-js/from/cn";
import fromHk from "opencc-js/from/hk";
import fromTw from "opencc-js/from/tw";
import fromTwp from "opencc-js/from/twp";
import fromJp from "opencc-js/from/jp";
import toCn from "opencc-js/to/cn";
import toHk from "opencc-js/to/hk";
import toTw from "opencc-js/to/tw";
import toTwp from "opencc-js/to/twp";
import toJp from "opencc-js/to/jp";
import { openEpub, save, text, setText, saveOpf, escape, unescape } from "../shared/epub.js";

const LOCALES = [
  { value: "0", title: "Mainland" },
  { value: "1", title: "Hong Kong" },
  { value: "2", title: "Taiwan" },
  { value: "3", title: "Japan" },
];

// main.py: punctuation and its vertical presentation forms, less the
// marks not used in vertical text (PUNC_OMITS).
const H2V_MASTER = { "。": "︒", "、": "︑", "；": "︔", "：": "︓", "！": "︕", "？": "︖", "「": "﹁", "」": "﹂", "〈": "︿", "〉": "﹀",
  "『": "﹃", "』": "﹄", "《": "︽", "》": "︾", "【": "︻", "（": "︵", "】": "︼", "）": "︶", "〖": "︗", "〗": "︘",
  "〔": "︹", "｛": "︷", "〕": "︺", "｝": "︸", "［": "﹇", "］": "﹈", "…": "︙", "‥": "︰", "—": "︱", "＿": "︳",
  "﹏": "︴", "，": "︐" };
const PUNC_OMITS = "。、；：！？…‥＿﹏，";
const TRAD_TO_SIMP_QUOTES = { "「": "“", "」": "”", "『": "‘", "』": "’" };
const SIMP_TO_TRAD_QUOTES = { "“": "「", "”": "」", "‘": "『", "’": "』" };

/**
 * @typedef {{ conversionType: number, inputLocale: number, outputLocale: number, phrases: boolean,
 *   quotationType: number, orientation: number, punctuation: Record<string, string> | null }} Criteria
 */

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  const a = await dalibre.form({
    submit: "Convert",
    remember: true,
    fields: [
      { key: "conversion", title: "Conversion", type: "select", value: "2", options: [
        { value: "0", title: "No change" },
        { value: "1", title: "Traditional → Simplified" },
        { value: "2", title: "Simplified → Traditional" },
        { value: "3", title: "Traditional → Traditional" },
      ] },
      { key: "input", title: "Input locale", type: "select", value: "0", options: LOCALES },
      { key: "output", title: "Output locale", type: "select", value: "2", options: LOCALES },
      { key: "phrases", title: "Use output locale phrases", type: "toggle", value: true },
      { key: "quotes", title: "Quotation marks", type: "select", value: "0", options: [
        { value: "0", title: "No change" },
        { value: "1", title: "Western “ ” ‘ ’" },
        { value: "2", title: "East Asian 「 」 『 』" },
      ] },
      { key: "orientation", title: "Text direction", type: "select", value: "0", options: [
        { value: "0", title: "No change" },
        { value: "1", title: "Horizontal" },
        { value: "2", title: "Vertical" },
      ] },
      { key: "punctuation", title: "Update punctuation for the text direction", type: "toggle", value: false },
      { key: "metadata", title: "Convert the book's details too", type: "toggle", value: true },
    ],
  });
  /** @type {Criteria} */
  const criteria = {
    conversionType: Number(a.conversion) || 0,
    inputLocale: Number(a.input) || 0,
    outputLocale: Number(a.output) || 0,
    phrases: a.phrases === true,
    quotationType: Number(a.quotes) || 0,
    orientation: Number(a.orientation) || 0,
    punctuation: null,
  };
  if (a.punctuation === true && criteria.orientation !== 0) {
    /** @type {Record<string, string>} */
    const h2v = {};
    for (const [k, v] of Object.entries(H2V_MASTER)) if (!PUNC_OMITS.includes(k)) h2v[k] = v;
    criteria.punctuation = criteria.orientation === 2 ? h2v : Object.fromEntries(Object.entries(h2v).map(([k, v]) => [v, k]));
  }
  const configuration = getConfiguration(criteria);
  if (configuration === "unsupported_conversion") {
    throw dalibre.error("invalid", "This combination of locales isn't supported. Choose other input and output locales.");
  }
  if (configuration === "no_conversion" && criteria.quotationType === 0 && criteria.orientation === 0) {
    return { message: "Nothing to change" };
  }
  const converter = new OpenCC(configuration);
  const lang = getLanguageCode(criteria);

  /** @type {NonNullable<Dalibre.ActionResult["formats"]>} */
  const formats = [];
  /** @type {NonNullable<Dalibre.ActionResult["changes"]>} */
  const changes = [];
  let done = 0;
  for (const book of input.books) {
    dalibre.progress(done++, input.books.length);
    const file = input.files?.[book.id];
    if (!file) continue;
    const epub = await openEpub(file.path);
    let changed = false;
    // process_files: metadata and TOC, then the text direction, then the documents.
    if (criteria.conversionType !== 0) changed = setMetadataToc(epub, lang, converter) || changed;
    if (criteria.orientation !== 0) changed = setFlowDirection(epub, criteria) || changed;
    for (const item of epub.manifest) {
      if (!/^(application\/xhtml\+xml|text\/html)$/i.test(item.type) || !epub.files[item.path]) continue;
      const before = text(epub, item.path);
      const after = processText(before, criteria, converter, lang);
      if (after !== before) {
        setText(epub, item.path, after);
        changed = true;
      }
    }
    if (!changed) continue;
    const out = `/work/converted-${book.id}.epub`;
    await save(epub, out);
    formats.push({ book: book.id, path: out });

    if (a.metadata === true && criteria.conversionType !== 0) {
      // The library's counterpart of set_metadata_toc's OPF fields.
      const c = (/** @type {string} */ s) => converter.convert(s);
      /** @type {Dalibre.Changes} */
      const m = {};
      if (c(book.title) !== book.title) m.title = c(book.title);
      if (book.authors.some((x) => c(x) !== x)) m.authors = book.authors.map(c);
      if (book.publisher && c(book.publisher) !== book.publisher) m.publisher = c(book.publisher);
      if (book.tags.some((x) => c(x) !== x)) m.tags = book.tags.map(c);
      if (book.comments && c(book.comments) !== book.comments) m.comments = processText(book.comments, criteria, converter, null);
      if (Object.keys(m).length) changes.push({ book: book.id, metadata: m });
    }
  }
  dalibre.progress(done, input.books.length);
  if (formats.length === 0) return { message: "No text meeting your criteria was found to change." };
  /** @type {Dalibre.ActionResult} */
  const result = { formats };
  if (changes.length) result.changes = changes;
  return result;
}

/** main.get_configuration: the OpenCC configuration for the criteria. @param {Criteria} c */
export function getConfiguration(c) {
  const { conversionType: mode, inputLocale: input, outputLocale: output } = c;
  const p = c.phrases ? "p" : "";
  if (mode === 0) return "no_conversion";
  if (mode === 1) {
    if (input === 0) return output === 0 ? "t2s" : output === 3 ? "t2jp" : "unsupported_conversion";
    if (input === 1) return output === 0 ? "hk2s" + p : "unsupported_conversion";
    if (input === 2) return output === 0 ? "tw2s" + p : "unsupported_conversion";
    return "unsupported_conversion";
  }
  if (mode === 2) {
    if (input === 0) return output === 0 ? "s2t" : output === 1 ? "s2hk" + p : output === 2 ? "s2tw" + p : "unsupported_conversion";
    if (input === 3) return output === 0 ? "jp2t" : "unsupported_conversion";
    return "unsupported_conversion";
  }
  if (input === 0) return output === 0 ? "no_conversion" : output === 1 ? "t2hk" : output === 2 ? "t2tw" : "unsupported_conversion";
  if (input === 1) return output === 0 ? "hk2t" : output === 1 ? "no_conversion" : "unsupported_conversion";
  if (input === 2) return output === 0 ? "tw2t" : output === 2 ? "no_conversion" : "unsupported_conversion";
  return "unsupported_conversion";
}

/** main.get_language_code: the lang the text becomes, or null. @param {Criteria} c */
export function getLanguageCode(c) {
  const { conversionType: mode, inputLocale: input, outputLocale: output } = c;
  if (mode === 1) return output === 0 ? "zh-Hans-CN" : null;
  if (mode === 2) return output === 0 ? "zh-Hant-CN" : output === 1 ? "zh-Hant-HK" : "zh-Hant-TW";
  if (mode === 3) {
    if (input === 0) return output === 1 ? "zh-Hant-HK" : output === 2 ? "zh-Hant-TW" : null;
    if (input === 1 || input === 2) return output === 0 ? "zh-Hant-CN" : null;
  }
  return null;
}

/**
 * HTML_TextProcessor.processText, for a whole document: converts text
 * (entities decoded, as calibre's HTMLParser does; never tags, comments,
 * script or style), quotation marks and punctuation, and sets Chinese lang
 * attributes.
 * @param {string} html @param {Criteria} criteria @param {OpenCC} converter @param {string | null} lang
 */
export function processText(html, criteria, converter, lang) {
  const zhNon = /lang="zh-Latn|lang="zh-Cyrl|lang="zh-Bopo|lang="zh-Mong/i;
  const zh = /lang="zh-[-\w+]+"|lang="zh"/gi;
  let code = false; // in script or style
  return dalibre.html.tokens(html).map((t) => {
    if (t.type === "start" || t.type === "self") {
      if (t.type === "start") code = t.name === "script" || t.name === "style";
      let tag = t.raw;
      // A body gets the class the text direction's CSS rule targets.
      if (criteria.orientation !== 0 && t.name === "body" && !tag.includes("calibre-chinese_text")) {
        tag = /\sclass\s*=/.test(tag)
          ? tag.replace(/(\sclass\s*=\s*)(["'])(.*?)\2/, (_, x, q, v) => `${x}${q}${v} calibre-chinese_text${q}`)
          : tag.replace(/^<body/i, '<body class="calibre-chinese_text"');
      }
      if (criteria.conversionType !== 0 && lang && !zhNon.test(tag)) tag = tag.replace(zh, `lang="${lang}"`);
      return tag;
    }
    if (t.type === "end") code = false;
    if (t.type !== "text" || code) return t.raw;
    const after = handleData(t.text, criteria, converter);
    return after === t.text ? t.raw : dalibre.html.escape(after);
  }).join("");
}

/** HTML_TextProcessor.handle_data. @param {string} s @param {Criteria} criteria @param {OpenCC} converter */
function handleData(s, criteria, converter) {
  if (!s.trim()) return s;
  if (criteria.orientation !== 1 && criteria.quotationType !== 0) s = replaceQuotations(s, criteria);
  if (criteria.punctuation) {
    const dict = criteria.punctuation;
    s = [...s].map((ch) => dict[ch] ?? ch).join("");
  }
  if (criteria.orientation === 1 && criteria.quotationType !== 0) s = replaceQuotations(s, criteria);
  return criteria.conversionType !== 0 ? converter.convert(s) : s;
}

/** @param {string} s @param {Criteria} criteria */
function replaceQuotations(s, criteria) {
  /** @type {Record<string, string>} */
  const map = criteria.quotationType === 1 ? TRAD_TO_SIMP_QUOTES : SIMP_TO_TRAD_QUOTES;
  return s.replace(/[「」『』“”‘’]/g, (ch) => map[ch] ?? ch);
}

/**
 * main.set_metadata_toc: the OPF's Chinese languages, creators (text and
 * attributes), title, description, publisher, subjects, contributors,
 * coverage and rights; and the NCX's labels.
 * @param {import("../shared/epub.js").Epub} epub @param {string | null} lang @param {OpenCC} converter
 */
function setMetadataToc(epub, lang, converter) {
  const conv = (/** @type {string} */ s) => escape(converter.convert(unescape(s)));
  let opf = epub.opf;
  if (lang) opf = opf.replace(/(<dc:language\b[^>]*>)([^<]*)(<\/dc:language>)/gi, (m, o, v, c) => (/zh-\w+|zh/i.test(v) ? o + lang + c : m));
  opf = opf.replace(/<dc:creator\b([^>]*)>([\s\S]*?)<\/dc:creator>/gi, (_, attrs, v) =>
    `<dc:creator${attrs.replace(/(=\s*)(["'])(.*?)\2/g, (/** @type {string} */ __, /** @type {string} */ e, /** @type {string} */ q, /** @type {string} */ x) => e + q + conv(x) + q)}>${conv(v)}</dc:creator>`);
  // main.py lists subject and contributor without a comma between them,
  // which matches neither; both are converted here, as intended.
  opf = opf.replace(/(<dc:(title|description|publisher|subject|contributor|coverage|rights)\b[^>]*>)([\s\S]*?)(<\/dc:\2>)/gi,
    (_, o, _n, v, c) => o + conv(v) + c);
  let changed = opf !== epub.opf;
  if (changed) {
    epub.opf = opf;
    saveOpf(epub);
  }
  const ncx = epub.manifest.find((i) => i.type === "application/x-dtbncx+xml");
  if (ncx && epub.files[ncx.path]) {
    const before = text(epub, ncx.path);
    const after = before.replace(/(<navLabel\b[^>]*>\s*<text\b[^>]*>)([\s\S]*?)(<\/text>)/gi, (_, o, v, c) => o + conv(v) + c);
    if (after !== before) {
      setText(epub, ncx.path, after);
      changed = true;
    }
  }
  return changed;
}

/**
 * main.set_flow_direction: the spine's page progression, and a
 * body.calibre-chinese_text rule with the writing mode in each stylesheet.
 * @param {import("../shared/epub.js").Epub} epub @param {Criteria} criteria
 */
function setFlowDirection(epub, criteria) {
  const flow = criteria.orientation === 2 ? "rtl" : "ltr";
  let changed = false;
  const opf = epub.opf.replace(/<((?:\w+:)?spine)\b([^>]*)>/i, (_, name, attrs) => {
    const next = /page-progression-direction\s*=/.test(attrs)
      ? attrs.replace(/page-progression-direction\s*=\s*(["'])[^"']*\1/, `page-progression-direction="${flow}"`)
      : `${attrs} page-progression-direction="${flow}"`;
    return `<${name}${next}>`;
  });
  if (opf !== epub.opf) {
    epub.opf = opf;
    saveOpf(epub);
    changed = true;
  }
  const orientation = criteria.orientation === 1 ? "horizontal-tb" : "vertical-rl";
  const lineBreak = criteria.orientation === 1 ? "auto" : "normal";
  const rule = `body.calibre-chinese_text {\n  writing-mode: ${orientation};\n  -epub-writing-mode: ${orientation};\n  -webkit-writing-mode: ${orientation};\n  line-break: ${lineBreak};\n  -webkit-line-break: ${lineBreak}\n}\n`;
  const sheets = epub.manifest.filter((i) => i.type === "text/css" && epub.files[i.path]);
  const existing = sheets.filter((i) => /body\.calibre-chinese_text\s*\{/.test(text(epub, i.path)));
  for (const sheet of existing.length ? existing : sheets) {
    const before = text(epub, sheet.path);
    const after = existing.length
      ? before.replace(/body\.calibre-chinese_text\s*\{[^}]*\}\n?/, rule)
      : `${before.replace(/\s*$/, "\n")}${rule}`;
    if (after !== before) {
      setText(epub, sheet.path, after);
      changed = true;
    }
  }
  return changed;
}

/**
 * The plugin's OpenCC configurations as opencc-js dictionary groups: from
 * a locale to OpenCC's traditional characters, then to another. opencc-js
 * has no HKPhrases, so s2hkp and hk2sp are s2hk and hk2s.
 * @type {Record<string, any[][]>}
 */
const CONFIGS = {
  s2t: [fromCn], s2tw: [fromCn, toTw], s2twp: [fromCn, toTwp], s2hk: [fromCn, toHk], s2hkp: [fromCn, toHk],
  t2s: [toCn], tw2s: [fromTw, toCn], tw2sp: [fromTwp, toCn], hk2s: [fromHk, toCn], hk2sp: [fromHk, toCn],
  t2tw: [toTw], t2hk: [toHk], tw2t: [fromTw], hk2t: [fromHk], t2jp: [toJp], jp2t: [fromJp],
};

/** The converter for one of the plugin's configurations. */
export class OpenCC {
  /** @param {string} conversion */
  constructor(conversion) {
    const groups = CONFIGS[conversion];
    /** @type {(s: string) => string} */
    this.convert = groups ? ConverterFactory(...groups) : (s) => s;
  }
}
