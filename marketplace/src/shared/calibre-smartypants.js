// @ts-check
// calibre's "Smarten punctuation", ported from calibre
// (https://github.com/kovidgoyal/calibre, GPL-3.0):
// src/calibre/utils/smartypants.py (smartyPants with attr "1"),
// src/calibre/ebooks/conversion/preprocess.py (smarten_punctuation) and
// src/calibre/ebooks/chardet.py (strip_encoding_declarations,
// xml_replace_entities). Works on a whole (X)HTML document's source.

// Python's \w and \W are Unicode-aware; these are the same in JavaScript.
const W = "[\\p{L}\\p{N}_]";
const NW = "[^\\p{L}\\p{N}_]";
const PUNCT = `[!"#$%'()*+,\\-./:;<=>?@\\[\\\\\\]^_\`{|}~]`;

/** @param {string} s @param {string} flags */
const re = (s, flags = "gu") => new RegExp(s, flags);

/** smartyPants(text, "1"): quotes, backticks, dashes, ellipses. @param {string} text */
export function smartyPants(text) {
  const skipped = [];
  let inPre = false;
  let prevLast = "";
  let out = "";
  for (const [kind, tok] of tokenize(text)) {
    if (kind === "tag") {
      out += tok;
      const m = /^<(\/)?(style|pre|code|kbd|script|math)[^>]*>/i.exec(tok);
      if (m && !/\/\s*>$/.test(m[0])) {
        if (!m[1]) {
          skipped.push(m[2].toLowerCase());
          inPre = true;
        } else {
          if (skipped.length && m[2].toLowerCase() === skipped[skipped.length - 1]) skipped.pop();
          if (!skipped.length) inPre = false;
        }
      }
      continue;
    }
    let t = tok;
    const last = t.slice(-1);
    if (!inPre) {
      t = processEscapes(t);
      t = t.replaceAll("&quot;", '"');
      t = educateDashes(t);
      t = educateEllipses(t);
      t = educateBackticks(t);
      if (t === "'") t = /\S/.test(prevLast) ? "&#8217;" : "&#8216;";
      else if (t === '"') t = /\S/.test(prevLast) ? "&#8221;" : "&#8220;";
      else t = educateQuotes(t);
    }
    prevLast = last;
    out += t;
  }
  return out;
}

/** @param {string} html @returns {["tag" | "text", string][]} */
function tokenize(html) {
  /** @type {["tag" | "text", string][]} */
  const tokens = [];
  const soup = /([^<]*)(<[^>]*>)/g;
  let end = 0;
  let m;
  while ((m = soup.exec(html))) {
    if (m[1]) tokens.push(["text", m[1]]);
    tokens.push(["tag", m[2]]);
    end = soup.lastIndex;
  }
  if (end < html.length) tokens.push(["text", html.slice(end)]);
  return tokens;
}

/** @param {string} text */
function educateQuotes(text) {
  text = text.replace(re(`^'(?=${PUNCT}\\B)`), "&#8217;");
  text = text.replace(re(`^"(?=${PUNCT}\\B)`), "&#8221;");
  text = text.replace(re(`"'(?=${W})`), "&#8220;&#8216;");
  text = text.replace(re(`'"(?=${W})`), "&#8216;&#8220;");
  text = text.replace(re(`""(?=${W})`), "&#8220;&#8220;");
  text = text.replace(re(`''(?=${W})`), "&#8216;&#8216;");
  text = text.replaceAll("\"'", "&#8221;&#8217;");
  text = text.replaceAll("'\"", "&#8217;&#8221;");
  text = text.replaceAll('""', "&#8221;&#8221;");
  text = text.replaceAll("''", "&#8217;&#8217;");
  text = text.replace(re(`(${NW}|^)'(?=\\d{2}s)`), "$1&#8217;");
  text = text.replace(re(`(${NW}|^)([-0-9.]+\\s*)'(\\s*[-0-9.]+)"`), "$1$2&#8242;$3&#8243;");
  text = text.replace(re(`(?<=${NW})"(?=${W})`), "&#8220;");
  text = text.replace(re(`(?<=${NW})'(?=${W})`), "&#8216;");
  text = text.replace(re(`(?<=${W})"(?=${NW})`), "&#8221;");
  text = text.replace(re(`(?<=${W})'(?=${NW})`), "&#8217;");
  const close = "[^ \\t\\r\\n\\[\\{\\(\\-]";
  const opening = `(\\s|&nbsp;|--|&[mn]dash;|&#8211;|&#8212;|&#x201[34];)`;
  text = text.replace(re(`${opening}'(?=${W})`), "$1&#8216;");
  text = text.replace(re(`(${close})'(?!\\s|s(?!${W})|\\d)`), "$1&#8217;");
  text = text.replace(re(`(${close})'(\\s|s(?!${W}))`), "$1&#8217;$2");
  text = text.replaceAll("'", "&#8216;");
  text = text.replace(re(`${opening}"(?=${W})`), "$1&#8220;");
  text = text.replace(/"(?=\s)/g, "&#8221;");
  text = text.replace(re(`(${close})"`), "$1&#8221;");
  if (text.endsWith('-"')) text = text.slice(0, -1) + "&#8221;";
  text = text.replaceAll('"', "&#8220;");
  return text;
}

/** @param {string} text */
function educateBackticks(text) {
  return text.replaceAll("``", "&#8220;").replaceAll("''", "&#8221;");
}

/** @param {string} text */
function educateDashes(text) {
  return text.replaceAll("---", "&#8211;").replaceAll("--", "&#8212;");
}

/** @param {string} text */
function educateEllipses(text) {
  return text.replaceAll("...", "&#8230;").replaceAll(". . .", "&#8230;");
}

/** @param {string} text */
function processEscapes(text) {
  return text
    .replaceAll("\\\\", "&#92;")
    .replaceAll('\\"', "&#34;")
    .replaceAll("\\'", "&#39;")
    .replaceAll("\\.", "&#46;")
    .replaceAll("\\-", "&#45;")
    .replaceAll("\\`", "&#96;");
}

/** strip_encoding_declarations: XML declarations and charset metas go. @param {string} raw */
export function stripEncodingDeclarations(raw) {
  const limit = 50 * 1024;
  let prefix = raw.slice(0, limit);
  prefix = prefix.replace(/<\?[^<>]+encoding\s*=\s*['"](.*?)['"][^<>]*>/gi, "");
  prefix = prefix.replace(/<meta\s+charset=['"]([-_a-z0-9]+)['"][^<>]*>(?:\s*<\/meta>){0,1}/gi, "");
  prefix = prefix.replace(/<meta\s+?[^<>]*?content\s*=\s*['"][^'"]*?charset=([-_a-z0-9]+)[^'"]*?['"][^<>]*>(?:\s*<\/meta>){0,1}/gi, "");
  return prefix + raw.slice(limit);
}

/**
 * xml_replace_entities: character references become characters, except
 * the five XML ones.
 * @param {string} raw
 */
export function xmlReplaceEntities(raw) {
  /** @type {Record<string, string>} */
  const named = { nbsp: " ", mdash: "—", ndash: "–", hellip: "…", lsquo: "‘", rsquo: "’", ldquo: "“", rdquo: "”", shy: "­", copy: "©" };
  return raw.replace(/&(#[xX][0-9a-fA-F]+|#\d+|[A-Za-z][A-Za-z0-9]*);/g, (m, e) => {
    if (e[0] === "#") {
      const n = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
      if (!Number.isFinite(n) || n <= 0 || n > 0x10ffff || [38, 60, 62, 34, 39].includes(n)) return m;
      return String.fromCodePoint(n);
    }
    return named[e] ?? m;
  });
}

/** smarten_punctuation of preprocess.py. @param {string} html */
export function smartenPunctuation(html) {
  const start = `calibre-smartypants-${Math.random().toString(16).slice(2)}`;
  const stop = `calibre-smartypants-${Math.random().toString(16).slice(2)}`;
  html = html.replaceAll("<!--", start).replaceAll("-->", stop);
  html = smartyPants(html);
  html = html.replaceAll(start, "<!--").replaceAll(stop, "-->");
  return xmlReplaceEntities(html);
}
