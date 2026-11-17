// @ts-check
// A small, forgiving (X)HTML/XML tokenizer and DOM, without a browser:
// tags, text and the rest (comments, CDATA, doctype, processing
// instructions), each keeping its source, so a document is written back
// byte for byte where nothing was changed.

/**
 * @typedef {{
 *   kind: "text" | "open" | "close" | "other",
 *   raw: string,
 *   name: string,
 *   self: boolean,
 * }} Token
 */

/** @param {string} html @returns {Token[]} */
export function tokenize(html) {
  /** @type {Token[]} */
  const out = [];
  const n = html.length;
  let i = 0;
  /** @param {string} raw */
  const pushText = (raw) => {
    if (!raw) return;
    const last = out[out.length - 1];
    if (last && last.kind === "text") last.raw += raw;
    else out.push({ kind: "text", raw, name: "", self: false });
  };
  /** @param {string} close @param {number} from */
  const until = (close, from) => {
    const e = html.indexOf(close, from);
    return e < 0 ? n : e + close.length;
  };
  while (i < n) {
    const lt = html.indexOf("<", i);
    if (lt < 0) {
      pushText(html.slice(i));
      break;
    }
    pushText(html.slice(i, lt));
    let end;
    if (html.startsWith("<!--", lt)) end = until("-->", lt + 4);
    else if (html.startsWith("<![CDATA[", lt)) end = until("]]>", lt + 9);
    else if (html[lt + 1] === "?") end = until("?>", lt + 2);
    else if (html[lt + 1] === "!") {
      const gt = html.indexOf(">", lt);
      const br = html.indexOf("[", lt);
      end = br >= 0 && (gt < 0 || br < gt) ? until("]>", br) : gt < 0 ? n : gt + 1;
    } else {
      const m = /^<(\/?)([A-Za-z_][\w:.-]*)/.exec(html.slice(lt, lt + 200));
      if (!m) {
        pushText("<");
        i = lt + 1;
        continue;
      }
      let j = lt + 1;
      let q = "";
      for (; j < n; j++) {
        const c = html[j];
        if (q) {
          if (c === q) q = "";
        } else if ((c === '"' || c === "'") && /=\s*$/.test(html.slice(Math.max(lt, j - 8), j))) q = c;
        else if (c === ">") break;
      }
      end = Math.min(j + 1, n);
      const raw = html.slice(lt, end);
      const self = !m[1] && /\/\s*>$/.test(raw);
      out.push({ kind: m[1] ? "close" : "open", raw, name: m[2], self });
      i = end;
      // script and style hold raw text, as an HTML parser reads them
      const lname = m[2].toLowerCase();
      if (!m[1] && !self && (lname === "script" || lname === "style")) {
        const close = html.slice(i).search(new RegExp(`</${lname}\\s*>`, "i"));
        const stop = close < 0 ? n : i + close;
        if (stop > i) out.push({ kind: "text", raw: html.slice(i, stop), name: "#raw", self: false });
        i = stop;
      }
      continue;
    }
    out.push({ kind: "other", raw: html.slice(lt, end), name: "", self: false });
    i = end;
  }
  return out;
}

// ---- entities ----

const NAMED = /** @type {Record<string, string>} */ ({
  amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " ", shy: "­", copy: "©", reg: "®",
  mdash: "—", ndash: "–", hellip: "…", lsquo: "‘", rsquo: "’", ldquo: "“", rdquo: "”",
  laquo: "«", raquo: "»", middot: "·", bull: "•", times: "×", deg: "°", ensp: " ",
  emsp: " ", thinsp: " ", zwnj: "‌", zwj: "‍", trade: "™", sect: "§", para: "¶",
});

/** Text with its character references decoded; unknown named ones are kept. @param {string} s */
export function decode(s) {
  if (!s.includes("&")) return s;
  return s.replace(/&(#[xX][0-9a-fA-F]+|#\d+|[A-Za-z][A-Za-z0-9]*);/g, (m, e) => {
    if (e[0] === "#") {
      const n = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
      return Number.isFinite(n) && n > 0 && n <= 0x10ffff ? String.fromCodePoint(n) : m;
    }
    return NAMED[e] ?? m;
  });
}

/** Text escaped for XML content. @param {string} s */
export function escapeText(s) {
  return s.replace(/&(?![A-Za-z][A-Za-z0-9]*;|#\d+;|#[xX][0-9a-fA-F]+;)/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

/** @param {string} s */
export function escapeAttr(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

// ---- DOM ----

/**
 * @typedef {{
 *   type: "element", name: string, attrs: [string, string][], children: Node[], parent: Parent | null,
 *   self: boolean, closed: boolean, rawOpen: string, rawClose: string, dirty: boolean,
 * }} Element
 * @typedef {{ type: "text", raw: string, parent: Parent | null }} Text
 * @typedef {{ type: "raw", raw: string, parent: Parent | null }} Raw
 * @typedef {{ type: "root", children: Node[], parent: null }} Root
 * @typedef {Element | Text | Raw} Node
 * @typedef {Element | Root} Parent
 */

const VOID = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"]);

/** @param {string} raw @returns {[string, string][]} */
function parseAttrs(raw) {
  const body = raw.replace(/^<[^\s/>]+/, "").replace(/\/?\s*>$/, "");
  /** @type {[string, string][]} */
  const out = [];
  for (const m of body.matchAll(/([^\s="'\/>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g)) {
    out.push([m[1], decode(m[2] ?? m[3] ?? m[4] ?? "")]);
  }
  return out;
}

/**
 * Parses a document. With html, void elements (br, img, meta…) never have
 * children, as in HTML.
 * @param {string} src @param {{ html?: boolean }} [opts] @returns {Root}
 */
export function parse(src, opts = {}) {
  /** @type {Root} */
  const root = { type: "root", children: [], parent: null };
  /** @type {Element[]} */
  const open = [];
  /** @param {Node} node */
  const add = (node) => {
    const p = open.length ? open[open.length - 1] : root;
    node.parent = p;
    p.children.push(node);
  };
  for (const t of tokenize(src)) {
    if (t.kind === "text") add({ type: "text", raw: t.raw, parent: null });
    else if (t.kind === "other") add({ type: "raw", raw: t.raw, parent: null });
    else if (t.kind === "open") {
      /** @type {Element} */
      const el = { type: "element", name: t.name, attrs: parseAttrs(t.raw), children: [], parent: null, self: t.self, closed: false, rawOpen: t.raw, rawClose: "", dirty: false };
      add(el);
      if (!t.self && !(opts.html && VOID.has(t.name.toLowerCase()))) open.push(el);
    } else {
      const lower = t.name.toLowerCase();
      let k = open.length - 1;
      while (k >= 0 && open[k].name.toLowerCase() !== lower) k--;
      if (k < 0) {
        add({ type: "raw", raw: t.raw, parent: null });
        continue;
      }
      open[k].closed = true;
      open[k].rawClose = t.raw;
      open.length = k;
    }
  }
  return root;
}

/** The node and everything in it, as source. @param {Node | Root} node @returns {string} */
export function serialize(node) {
  if (node.type === "text" || node.type === "raw") return node.raw;
  if (node.type === "root") return node.children.map(serialize).join("");
  const open = node.dirty ? openTag(node) : node.rawOpen;
  if (node.self && !node.children.length) return node.dirty ? open.replace(/>$/, "/>") : open;
  const inner = node.children.map(serialize).join("");
  const close = node.closed ? node.rawClose || `</${node.name}>` : "";
  return (node.self ? open.replace(/\/\s*>$/, ">") : open) + inner + (node.self && !node.closed ? `</${node.name}>` : close);
}

/** @param {Element} el */
function openTag(el) {
  return `<${el.name}${el.attrs.map(([k, v]) => ` ${k}="${escapeAttr(v)}"`).join("")}>`;
}

/**
 * A new element. Children are given as source.
 * @param {string} name @param {Record<string, string>} [attrs] @param {string} [inner]
 */
export function element(name, attrs = {}, inner) {
  const kids = inner === undefined ? [] : parse(inner).children;
  /** @type {Element} */
  const el = { type: "element", name, attrs: Object.entries(attrs), children: kids, parent: null, self: inner === undefined, closed: inner !== undefined, rawOpen: "", rawClose: "", dirty: true };
  for (const k of kids) k.parent = el;
  return el;
}

/** The local name, lower case: "dc:Title" → "title". @param {Node | Root} n */
export function local(n) {
  return n.type === "element" ? n.name.slice(n.name.indexOf(":") + 1).toLowerCase() : "";
}

/** @param {Element} el @param {string} name */
export function getAttr(el, name) {
  const l = name.toLowerCase();
  return el.attrs.find(([k]) => k.toLowerCase() === l)?.[1];
}

/** An attribute matched by local name, any prefix: "role" finds opf:role. @param {Element} el @param {string} name */
export function getAttrLocal(el, name) {
  const l = name.toLowerCase();
  return el.attrs.find(([k]) => k.slice(k.indexOf(":") + 1).toLowerCase() === l)?.[1];
}

/** @param {Element} el @param {string} name @param {string} value */
export function setAttr(el, name, value) {
  const a = el.attrs.find(([k]) => k === name);
  if (a) a[1] = value;
  else el.attrs.push([name, value]);
  el.dirty = true;
}

/** @param {Element} el @param {string} name */
export function removeAttr(el, name) {
  const n = el.attrs.length;
  el.attrs = el.attrs.filter(([k]) => k.toLowerCase() !== name.toLowerCase());
  if (el.attrs.length !== n) el.dirty = true;
}

/** Every element under node, in document order. @param {Node | Root} node @returns {Element[]} */
export function elements(node) {
  /** @type {Element[]} */
  const out = [];
  /** @param {Node | Root} n */
  const walk = (n) => {
    if (n.type !== "element" && n.type !== "root") return;
    if (n.type === "element") out.push(n);
    for (const c of n.children) walk(c);
  };
  if (node.type === "root") node.children.forEach(walk);
  else if (node.type === "element") node.children.forEach(walk);
  return out;
}

/** Elements under node with this local name. @param {Node | Root} node @param {string} name */
export function byName(node, name) {
  return elements(node).filter((e) => local(e) === name);
}

/** The element children of node. @param {Parent} node @returns {Element[]} */
export function childElements(node) {
  return /** @type {Element[]} */ (node.children.filter((c) => c.type === "element"));
}

/** The decoded text in node. @param {Node | Root} node @returns {string} */
export function textOf(node) {
  if (node.type === "text") return decode(node.raw);
  if (node.type === "raw") return node.raw.startsWith("<![CDATA[") ? node.raw.slice(9, -3) : "";
  return node.children.map(textOf).join("");
}

/** Takes node out of its parent; returns where it was. @param {Node} node */
export function remove(node) {
  const p = node.parent;
  if (!p) return -1;
  const i = p.children.indexOf(node);
  if (i >= 0) p.children.splice(i, 1);
  node.parent = null;
  return i;
}

/** @param {Parent} parent @param {Node} node @param {number} [index] */
export function insert(parent, node, index = parent.children.length) {
  if (node.parent) remove(node);
  node.parent = parent;
  parent.children.splice(index, 0, node);
}

/** A text node with this text, escaped. @param {string} s @returns {Text} */
export function textNode(s) {
  return { type: "text", raw: escapeText(s), parent: null };
}
