// @ts-check
// Modify ePub: ported from Grant Drake's (kiwidude) Modify ePub calibre
// plugin (https://github.com/kiwidude68/calibre_plugins, folder
// modify_epub, GPL-3.0): modify.py (the fixes), container.py (the ePub
// container: manifest, spine, guide and NCX upkeep), jacket.py and
// dialogs.py (the options and their names).

import { escape, openEpub, save, strFromU8, strToU8 } from "../shared/epub.js";
import { byName, childElements, elements, getAttr, getAttrLocal, insert, local, parse, remove, removeAttr, serialize, setAttr, textOf } from "../shared/markup.js";
import { baseName, dirOf, normpath, outFile, unquote } from "../shared/epubkit.js";
import { smartenPunctuation, stripEncodingDeclarations } from "../shared/calibre-smartypants.js";
import { readFile } from "node:fs/promises";

/** The options, in Modify ePub's dialog order, with its names. */
const OPTIONS = /** @type {const} */ ([
  ["remove_itunes_files", "Remove iTunes files"],
  ["remove_calibre_bookmarks", "Remove calibre bookmark files"],
  ["remove_os_artifacts", "Remove OS artifact files"],
  ["remove_unused_images", "Remove unused image files"],
  ["unpretty", "De-indent (aka unpretty)"],
  ["strip_kobo", "Strip Kobo DRM remnants"],
  ["strip_spans", "Strip spans"],
  ["remove_missing_files", "Remove missing file entries from manifest"],
  ["add_unmanifested_files", "Add unmanifested files to manifest"],
  ["remove_unmanifested_files", "Remove unmanifested files from ePub"],
  ["zero_xpgt_margins", "Remove margins from Adobe .xpgt files"],
  ["remove_xpgt_files", "Remove Adobe .xpgt files and links"],
  ["remove_drm_meta_tags", "Remove Adobe resource DRM meta tags"],
  ["remove_page_map", "Remove page maps"],
  ["remove_gp_page_map", "Remove ONLY Google Play page maps"],
  ["flatten_toc", "Flatten TOC hierarchy in NCX file"],
  ["remove_broken_ncx_links", "Remove broken TOC entries in NCX file"],
  ["remove_all_jackets", "Remove all metadata jackets"],
  ["remove_legacy_jackets", "Remove legacy metadata jackets"],
  ["remove_broken_covers", "Remove broken image pages"],
  ["remove_cover", "Remove existing cover"],
  ["insert_replace_cover", "Insert or replace cover"],
  ["update_metadata", "Update metadata"],
  ["remove_non_dc_elements", "Remove non dc: metadata elements"],
  ["encode_html_utf8", "Encode HTML in UTF-8"],
  ["remove_embedded_fonts", "Remove embedded fonts"],
  ["smarten_punctuation", "Smarten punctuation"],
  ["remove_javascript", "Remove inline javascript and files"],
]);

const ITUNES_FILES = ["iTunesMetadata.plist", "iTunesArtwork"];
const BOOKMARKS_FILES = ["META-INF/calibre_bookmarks.txt"];
const OS_FILES = [".DS_Store", "thumbs.db"];
const ALL_ARTIFACTS = [...ITUNES_FILES, ...BOOKMARKS_FILES, ...OS_FILES];
const IMAGE_FILES = [".png", ".jpg", ".jpeg", ".gif", ".bmp", ".svg"];
const FONT_FILES = [".otf", ".ttf"];
const NON_HTML_FILES = [...IMAGE_FILES, ...FONT_FILES, ".opf", ".xpgt", ".ncx", ".css"];
const ADOBE_OBFUSCATION = "http://ns.adobe.com/pdf/enc#RC";
const IDPF_OBFUSCATION = "http://www.idpf.org/2008/embedding";
const META_INF = new Set(["container.xml", "manifest.xml", "encryption.xml", "metadata.xml", "signatures.xml", "rights.xml"]);
const GUESS = /** @type {Record<string, string>} */ ({
  ".xhtml": "application/xhtml+xml", ".html": "text/html", ".htm": "text/html", ".css": "text/css", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
  ".png": "image/png", ".gif": "image/gif", ".svg": "image/svg+xml", ".bmp": "image/bmp", ".ttf": "application/x-font-ttf", ".otf": "application/vnd.ms-opentype",
  ".woff": "application/font-woff", ".ncx": "application/x-dtbncx+xml", ".js": "application/javascript", ".xml": "application/xml", ".txt": "text/plain",
  ".xpgt": "application/vnd.adobe-page-template+xml",
});

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  if (action !== "modify") throw dalibre.error("invalid", `Unknown action ${action}.`);
  const books = input.books.filter((b) => input.files?.[b.id]);
  if (!books.length) throw dalibre.error("invalid", "Modify ePub needs a book with an EPUB.");

  const a = await dalibre.form({
    submit: "Modify",
    // Modify ePub restores the options last used; so does the form.
    remember: true,
    fields: [{ key: "options", title: "Options", type: "multiselect", value: [], options: OPTIONS.map(([value, title]) => ({ value, title })) }],
  });
  const chosen = new Set((Array.isArray(a.options) ? a.options : String(a.options ?? "").split(",")).map(String));
  /** @type {Record<string, boolean>} */
  const options = Object.fromEntries(OPTIONS.map(([k]) => [k, chosen.has(k)]));
  if (!OPTIONS.some(([k]) => options[k])) throw dalibre.error("invalid", "You must select at least one option to continue.");

  /** @type {{ book: number, path: string }[]} */
  const formats = [];
  /** @type {string[]} */
  const drm = [];
  for (const [n, book] of books.entries()) {
    dalibre.progress(n, books.length);
    const file = /** @type {Dalibre.HookFile} */ (input.files?.[book.id]);
    const epub = await openEpub(file.path);
    const container = new Container(epub);
    if (container.isDrmEncrypted()) drm.push(book.title);
    const coverPath = input.covers?.[book.id];
    const cover = coverPath ? new Uint8Array(await readFile(coverPath)) : null;
    const modifier = new BookModifier(container, book, cover);
    if (!modifier.processBook(options)) continue;
    container.write();
    const path = await outFile(`modified-${book.id}`, book.title, "epub");
    await save(epub, path);
    formats.push({ book: book.id, path });
  }
  dalibre.progress(books.length, books.length);
  if (!formats.length) return { message: drm.length ? "Nothing to change: the book is DRM-encrypted." : "Nothing to change" };
  const msg = books.length === 1 ? "ePub updated." : `${formats.length} of ${books.length} ePubs updated.`;
  return { formats, message: drm.length ? `${msg} Skipped most options for DRM-encrypted books.` : msg };
}

/** modify.py's BookModifier. */
class BookModifier {
  /** @param {Container} c @param {Dalibre.BookInfo} mi @param {Uint8Array | null} cover the library cover */
  constructor(c, mi, cover) {
    this.c = c;
    this.mi = mi;
    this.cover = cover;
  }

  /** _process_book, in its order. @param {Record<string, boolean>} o */
  processBook(o) {
    const c = this.c;
    let changed = false;
    if (o.update_metadata) changed = this.updateMetadata() || changed;
    // MANIFEST
    if (o.remove_missing_files) changed = this.removeMissingFiles() || changed;
    if (o.add_unmanifested_files) changed = this.processUnmanifestedFiles(true) || changed;
    else if (o.remove_unmanifested_files) changed = this.processUnmanifestedFiles(false) || changed;
    if (o.flatten_toc && !c.isDrmEncrypted()) changed = c.flattenToc() || changed;
    if (o.remove_broken_ncx_links && !c.isDrmEncrypted()) changed = c.deleteBrokenTocLinks(new Set([...c.htmlNames()].map((n) => n.toLowerCase()))) || changed;
    // ADOBE
    if (o.zero_xpgt_margins && !o.remove_xpgt_files) changed = this.zeroXpgtMargins() || changed;
    if (o.remove_xpgt_files) changed = this.removeXpgtFiles() || changed;
    if (o.remove_page_map) changed = this.removePagemaps() || changed;
    if (o.remove_gp_page_map) changed = this.removeGpPagemaps() || changed;
    if (o.remove_drm_meta_tags) changed = this.removeDrmMetaTags() || changed;
    // JACKETS
    if (o.remove_legacy_jackets && !o.remove_all_jackets) changed = this.removeJackets(false) || changed;
    if (o.remove_all_jackets) changed = this.removeJackets(true) || changed;
    // METADATA/COVER
    if (o.remove_broken_covers) changed = this.removeBrokenCovers() || changed;
    if (o.remove_cover && !o.insert_replace_cover && !c.isDrmEncrypted()) {
      new CoverUpdater(c, null, this.mi.title).removeExistingCover();
      changed = true;
    }
    if (o.remove_non_dc_elements) changed = this.removeNonDcElements() || changed;
    // HTML/STYLE
    if (o.encode_html_utf8) changed = this.encodeHtmlUtf8() || changed;
    if (o.remove_embedded_fonts) changed = this.removeEmbeddedFonts() || changed;
    if (o.remove_javascript) changed = this.removeJavascript() || changed;
    if (o.smarten_punctuation) changed = this.smartenPunctuation() || changed;
    // FILES
    if (o.strip_kobo) changed = this.stripKobo() || changed;
    if (o.remove_itunes_files) changed = this.removeFilesIfExist(ITUNES_FILES) || changed;
    if (o.remove_calibre_bookmarks) changed = this.removeFilesIfExist(BOOKMARKS_FILES) || changed;
    if (o.remove_os_artifacts) changed = this.removeFilesIfExist(OS_FILES) || changed;
    if (o.remove_unused_images && !c.isDrmEncrypted()) changed = c.removeUnusedImages(c.imageNames()) || changed;
    if (o.strip_spans) changed = this.loopUntilStable(stripSpanForPage) || changed;
    if (o.unpretty) changed = this.loopUntilStable(unprettyForPage) || changed;
    // Last, as in modify.py: the container isn't kept in step after it.
    if (o.insert_replace_cover && !c.isDrmEncrypted() && this.cover) {
      new CoverUpdater(c, this.cover, this.mi.title).insertOrReplaceCover();
      changed = true;
    }
    return changed;
  }

  /** @param {string[]} files */
  removeFilesIfExist(files) {
    const lower = files.map((f) => f.toLowerCase());
    let dirtied = false;
    for (const name of this.c.names()) {
      const l = name.toLowerCase();
      if (lower.includes(l) || lower.some((f) => l.endsWith("/" + f))) {
        this.c.deleteFromManifest(name);
        dirtied = true;
      }
    }
    return dirtied;
  }

  removeMissingFiles() {
    const names = new Set(this.c.names());
    let dirtied = false;
    for (const name of [...this.c.mime.keys()]) {
      if (names.has(name)) continue;
      this.c.deleteFromManifest(name);
      dirtied = true;
    }
    return dirtied;
  }

  /** @param {boolean} add */
  processUnmanifestedFiles(add) {
    const artifacts = ALL_ARTIFACTS.map((f) => f.toLowerCase());
    let dirtied = false;
    for (const name of this.c.manifestWorthyNames()) {
      const l = name.toLowerCase();
      if (artifacts.includes(l) || artifacts.some((x) => l.endsWith("/" + x))) continue;
      if (this.c.manifestItemFor(name)) continue;
      if (add) {
        const ext = extOfName(name);
        let mt;
        if (ext.startsWith(".htm") && this.c.raw(name).includes('xmlns="http://www.w3.org/1999/xhtml"')) mt = GUESS[".xhtml"];
        this.c.addNameToManifest(name, mt);
      } else this.c.deleteName(name);
      dirtied = true;
    }
    return dirtied;
  }

  removeNonDcElements() {
    const metadata = byName(this.c.opf, "metadata")[0];
    if (!metadata) return false;
    let removed = false;
    for (const child of [...metadata.children]) {
      if (child.type === "text") continue;
      if (child.type === "element") {
        const prefix = child.name.includes(":") ? child.name.slice(0, child.name.indexOf(":")) : "";
        if (prefix === "dc" || prefix === "dcterms") continue;
        if (getAttr(child, "property") === "dcterms:modified") continue;
      }
      removeTidy(child);
      removed = true;
    }
    if (removed) this.c.opfDirty = true;
    return removed;
  }

  zeroXpgtMargins() {
    if (this.c.isDrmEncrypted()) return false;
    let dirtied = false;
    for (const name of this.c.xpgtNames()) {
      const doc = parse(this.c.raw(name));
      for (const el of elements(doc)) {
        for (const m of ["left", "right", "top", "bottom"]) {
          if (getAttr(el, `margin-${m}`) !== undefined) {
            removeAttr(el, `margin-${m}`);
            dirtied = true;
          }
        }
      }
      if (dirtied) {
        this.c.set(name, serialize(doc));
        break;
      }
    }
    return dirtied;
  }

  removeXpgtFiles() {
    if (this.c.isDrmEncrypted()) return false;
    let dirtied = false;
    for (const name of [...this.c.xpgtNames()]) {
      this.c.deleteFromManifest(name);
      dirtied = true;
    }
    for (const name of this.c.htmlNames()) {
      const doc = parse(this.c.raw(name), { html: true });
      let changed = false;
      for (const link of byName(doc, "link")) {
        const rel = getAttr(link, "rel");
        const href = getAttr(link, "href");
        if ((rel === "stylesheet" || rel === "xpgt") && href && href.toLowerCase().endsWith(".xpgt")) {
          remove(link);
          changed = true;
        }
      }
      if (changed) {
        this.c.set(name, serialize(doc));
        dirtied = true;
      }
    }
    const IMPORT1 = /@import url\(['"]*(.*?)['"]*\)[^;<\-]*;?/g;
    const IMPORT2 = /@import\s+"(.*?)"[^;<\-]*;?/g;
    for (const name of [...this.c.cssNames(), ...this.c.htmlNames()]) {
      let data = this.c.raw(name);
      const orig = data;
      for (const m of [...orig.matchAll(IMPORT1), ...orig.matchAll(IMPORT2)]) {
        if (m[1].toLowerCase().endsWith(".xpgt")) data = data.replace(m[0], "");
      }
      if (data !== orig) {
        this.c.set(name, data);
        dirtied = true;
      }
    }
    return dirtied;
  }

  removeDrmMetaTags() {
    if (this.c.isDrmEncrypted()) return false;
    const RE = /(\n*\s*)?<meta [^>]*?name="adept\.[expctd.]*?resource"[^>]*?>/gi;
    return this.c.rewriteHtml((html) => html.replace(RE, ""));
  }

  removePagemaps() {
    if (this.c.isDrmEncrypted()) return false;
    let dirtied = this.removeGpPagemaps();
    for (const name of [...this.c.pagemapNames()]) {
      this.c.deleteFromManifest(name);
      dirtied = true;
      this.removeSpinePageMap();
    }
    return dirtied;
  }

  removeGpPagemaps() {
    if (this.c.isDrmEncrypted()) return false;
    let dirtied = false;
    const A1 = /<div( style="display:none;")?>\s*<a id="GBS\.\d+\.\d+"\/>\s*<\/div>/gi;
    const A2 = /<a id="GBS\.\d+\.\d+"\/>/gi;
    for (const name of [...this.c.pagemapNames()]) {
      if (!/#GBS\.\d+\.\d+/.test(this.c.raw(name))) continue;
      this.c.rewriteHtml((html) => html.replace(A1, "").replace(A2, ""));
      this.c.deleteFromManifest(name);
      dirtied = true;
      this.removeSpinePageMap();
    }
    return dirtied;
  }

  removeSpinePageMap() {
    const spine = byName(this.c.opf, "spine")[0];
    if (spine && getAttr(spine, "page-map") !== undefined) {
      removeAttr(spine, "page-map");
      this.c.opfDirty = true;
    }
  }

  /** jacket.py: remove_all_jackets, remove_legacy_jackets. @param {boolean} all */
  removeJackets(all) {
    let dirtied = false;
    for (const name of this.c.names()) {
      if (!name.includes("jacket") || !name.endsWith(".xhtml")) continue;
      const doc = parse(this.c.raw(name), { html: true });
      const current = byName(doc, "meta").some((m) => getAttr(m, "name") === "calibre-content" && getAttr(m, "content") === "jacket");
      const legacy = [...byName(doc, "h1"), ...byName(doc, "h2")].some((h) => (getAttr(h, "class") ?? "").startsWith("calibrerescale"));
      if (all ? current || legacy : !current && legacy) {
        this.c.deleteFromManifest(name);
        dirtied = true;
      }
    }
    return dirtied;
  }

  removeBrokenCovers() {
    if (this.c.isDrmEncrypted()) return false;
    const avail = new Set([...this.c.imageNames()].map((n) => n.toLowerCase()));
    if (!avail.size) return false;
    const toDelete = [];
    for (const html of this.c.htmlNames()) {
      let candidate = false;
      for (const [image] of this.c.pageImageNames(html)) {
        if (avail.has(image.toLowerCase())) {
          candidate = false;
          break;
        }
        candidate = true;
      }
      if (candidate) toDelete.push(html);
    }
    let dirtied = false;
    for (const html of toDelete) {
      if (this.c.bodyText(html)) continue;
      this.c.deleteFromManifest(html);
      dirtied = true;
    }
    return dirtied;
  }

  encodeHtmlUtf8() {
    if (this.c.isDrmEncrypted()) return false;
    return this.c.rewriteHtml((html) => {
      let h = stripEncodingDeclarations(html);
      if (!h.trim().startsWith("<?xml")) {
        h = '<?xml version="1.0" encoding="utf-8"?>' + h;
        h = h.replace(/<\?xml([^?]*?)\?></, "<?xml$1?>\n<");
      }
      return h;
    });
  }

  removeEmbeddedFonts() {
    if (this.c.isDrmEncrypted()) return false;
    const RE = /@font-face[^}]+?}\s*/gi;
    let dirtied = false;
    for (const name of this.c.names()) {
      const l = name.toLowerCase();
      if (l.endsWith(".ttf") || l.endsWith(".otf")) {
        this.c.deleteFromManifest(name);
        dirtied = true;
      }
    }
    for (const name of this.c.cssNames()) {
      const css = this.c.raw(name);
      const out = css.replace(RE, "");
      if (out !== css) {
        this.c.set(name, out);
        dirtied = true;
      }
    }
    return this.c.rewriteHtml((html) => html.replace(RE, "")) || dirtied;
  }

  removeJavascript() {
    if (this.c.isDrmEncrypted()) return false;
    let dirtied = false;
    for (const name of this.c.htmlNames()) {
      const doc = parse(this.c.raw(name), { html: true });
      const scripts = byName(doc, "script").filter((s) => getAttr(s, "type") === "text/javascript");
      if (!scripts.length) continue;
      for (const s of scripts) remove(s);
      this.c.set(name, serialize(doc));
      dirtied = true;
    }
    for (const name of this.c.names()) {
      if (name.toLowerCase().endsWith(".js")) {
        this.c.deleteFromManifest(name);
        dirtied = true;
      }
    }
    return dirtied;
  }

  smartenPunctuation() {
    if (this.c.isDrmEncrypted()) return false;
    let dirtied = false;
    for (const name of this.c.htmlNames()) {
      const html = this.c.raw(name);
      const out = smartenPunctuation(html);
      if (out !== html) {
        this.c.set(name, stripEncodingDeclarations(out));
        dirtied = true;
      }
    }
    return dirtied;
  }

  stripKobo() {
    if (this.c.isDrmEncrypted()) return false;
    const M1 = /\s*<!-- kobo-style -->/gi;
    const M2 = /\s*<script[^>]*? src="[^"]*?js\/kobo(|-android)\.js"(\/|><\/script)>/gi;
    const M3 = /\s*<style[^>]*? id="kobo[\s\S]*?<\/style>/gi;
    const M4 = /\s*<link[^>]*? href="[^"]*?css\/kobo(|-android)\.css"[\s\S]*?(\/|><\/link)>/gi;
    let dirtied = this.c.rewriteHtml((html) => html.replace(M1, "").replace(M2, "").replace(M3, "").replace(M4, ""));
    for (const name of this.c.names()) {
      const l = name.toLowerCase();
      if (l.endsWith("js/kobo.js") || l.endsWith("css/kobo.css") || l === "rights.xml") {
        this.c.deleteFromManifest(name);
        dirtied = true;
      }
    }
    return this.c.rewriteHtml(stripKoboForPage) || dirtied;
  }

  /** Applies fn to each page until it stops changing, as strip_spans and unpretty do. @param {(s: string) => string} fn */
  loopUntilStable(fn) {
    if (this.c.isDrmEncrypted()) return false;
    return this.c.rewriteHtml((orig) => {
      let html = orig;
      let next = fn(html);
      while (html !== next) {
        html = next;
        next = fn(html);
      }
      return next;
    });
  }

  /**
   * _update_metadata_and_cover: calibre writes the library's metadata into
   * the OPF with set_metadata. The fields Dalibre hands an action are
   * written the same way; the cover is left alone, as Dalibre hands
   * actions no cover.
   */
  updateMetadata() {
    const mi = this.mi;
    const opf = this.c.opf;
    const metadata = byName(opf, "metadata")[0];
    if (!metadata) return false;
    const before = serialize(metadata);
    /** @param {string} name @param {(el: import("../shared/markup.js").Element) => boolean} [keep] */
    const drop = (name, keep = () => false) => {
      const want = name.slice(name.indexOf(":") + 1);
      for (const el of childElements(metadata)) {
        const dcEl = el.name.toLowerCase().startsWith("dc:") || getAttr(el, "xmlns") === "http://purl.org/dc/elements/1.1/";
        if (dcEl && local(el) === want && !keep(el)) removeTidy(el);
      }
    };
    /** @param {string} src */
    const add = (src) => {
      for (const n of parse(src).children) insert(metadata, n);
      insert(metadata, parse("\n").children[0]);
    };
    const uid = getAttr(byName(opf, "package")[0] ?? metadata, "unique-identifier");
    drop("dc:title");
    add(`<dc:title>${escape(mi.title)}</dc:title>`);
    if (mi.authors.length) drop("dc:creator", (el) => !["aut", undefined].includes(getAttrLocal(el, "role")));
    // file-as from the author sort, one per author as calibre keeps them
    const sorts = (mi.authorSort ?? "").split(" & ");
    for (const [k, a] of mi.authors.entries()) {
      const fileAs = sorts.length === mi.authors.length ? sorts[k] : "";
      add(`<dc:creator opf:role="aut"${fileAs ? ` opf:file-as="${escape(fileAs)}"` : ""}>${escape(a)}</dc:creator>`);
    }
    drop("dc:subject");
    for (const t of mi.tags ?? []) add(`<dc:subject>${escape(t)}</dc:subject>`);
    if (mi.languages?.length) {
      drop("dc:language");
      for (const l of mi.languages) add(`<dc:language>${escape(iso6391(l))}</dc:language>`);
    }
    if (mi.publisher !== undefined) {
      drop("dc:publisher");
      if (mi.publisher) add(`<dc:publisher>${escape(mi.publisher)}</dc:publisher>`);
    }
    if (mi.pubdate) {
      drop("dc:date", (el) => (getAttrLocal(el, "event") ?? "publication") !== "publication");
      add(`<dc:date>${escape(mi.pubdate)}</dc:date>`);
    }
    if (mi.comments !== undefined) {
      drop("dc:description");
      if (mi.comments) add(`<dc:description>${escape(mi.comments)}</dc:description>`);
    }
    for (const [scheme, value] of Object.entries(mi.identifiers ?? {})) {
      drop("dc:identifier", (el) => getAttr(el, "id") === uid || (getAttrLocal(el, "scheme") ?? "").toLowerCase() !== scheme.toLowerCase());
      add(`<dc:identifier opf:scheme="${escape(scheme)}">${escape(value)}</dc:identifier>`);
    }
    for (const el of childElements(metadata)) {
      const n = getAttr(el, "name");
      if (local(el) === "meta" && (n === "calibre:series" || n === "calibre:series_index" || n === "calibre:rating")) remove(el);
    }
    for (const el of childElements(metadata)) if (local(el) === "meta" && getAttr(el, "name") === "calibre:title_sort") remove(el);
    if (mi.titleSort) add(`<meta name="calibre:title_sort" content="${escape(mi.titleSort)}"/>`);
    if (mi.series) {
      add(`<meta name="calibre:series" content="${escape(mi.series)}"/>`);
      add(`<meta name="calibre:series_index" content="${mi.seriesIndex ?? 1}"/>`);
    }
    if (mi.rating) add(`<meta name="calibre:rating" content="${mi.rating}"/>`);
    if (!/xmlns:opf\s*=/.test(serialize(opf))) {
      metadata.attrs.push(["xmlns:opf", "http://www.idpf.org/2007/opf"]);
      metadata.dirty = true;
    }
    if (serialize(metadata) !== before) this.c.opfDirty = true;
    // calibre's set_metadata also puts the library cover over the book's
    // identifiable cover image (here when both are the same image format).
    if (this.cover) {
      const meta = byName(opf, "meta").find((m) => getAttr(m, "name") === "cover");
      const id = meta ? getAttr(meta, "content") : undefined;
      const item = byName(opf, "item").find((i) => /\bcover-image\b/.test(getAttr(i, "properties") ?? "")) ?? byName(opf, "item").find((i) => getAttr(i, "id") === id);
      const name = item ? this.c.hrefToName(getAttr(item, "href") ?? "") : "";
      const old = name ? this.c.epub.files[name] : undefined;
      if (old && imageType(old) && imageType(old) === imageType(this.cover)) this.c.epub.files[name] = this.cover;
    }
    // modify.py: "Going to 'assume' it did something".
    return true;
  }
}

/**
 * Removes a node with the whitespace before it, as container.py's
 * fix_tail_before_delete keeps the indentation tidy.
 * @param {import("../shared/markup.js").Node} node
 */
function removeTidy(node) {
  const p = node.parent;
  if (p) {
    const i = p.children.indexOf(node);
    const prev = p.children[i - 1];
    if (prev && prev.type === "text" && !/\S/.test(prev.raw)) remove(prev);
  }
  return remove(node);
}

/** calibre writes languages as ISO 639-1 where there is one. @param {string} code */
function iso6391(code) {
  /** @type {Record<string, string>} */
  const map = {
    eng: "en", fra: "fr", fre: "fr", deu: "de", ger: "de", spa: "es", ita: "it", por: "pt", rus: "ru", jpn: "ja", kor: "ko",
    zho: "zh", chi: "zh", nld: "nl", dut: "nl", swe: "sv", nor: "no", dan: "da", fin: "fi", pol: "pl", ces: "cs", cze: "cs",
    tur: "tr", ara: "ar", heb: "he", ell: "el", gre: "el", hun: "hu", ukr: "uk", vie: "vi", tha: "th", ind: "id", cat: "ca",
  };
  return map[code.toLowerCase()] ?? code;
}

/** @param {string} name */
function extOfName(name) {
  const i = name.lastIndexOf(".");
  return i < 0 ? "" : name.slice(i).toLowerCase();
}

/** The TAG walk of strip_spans and strip_kobo: drops matched span pairs. @param {string} html @param {(e: string) => boolean} isTarget */
function dropPairedTags(html, isTarget) {
  /** @type {{ content: string, type: number, pair: number }[]} */
  const ents = [];
  for (const e of html.split(/(<.+?>)/)) {
    if (!e) continue;
    const content = e.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    let type;
    if (isTarget(content)) type = 9;
    else if (content.slice(-2) === "/>") type = 3;
    else if (content[0] !== "<") type = 4;
    else if (content.slice(0, 2) === "</") type = 2;
    else type = 1;
    ents.push({ content, type, pair: 0 });
  }
  let pair = 0;
  for (let pos = 0; pos < ents.length; pos++) {
    if (ents[pos].type !== 2) continue;
    pair++;
    ents[pos].pair = pair;
    for (let pp = pos - 1; pp >= 0; pp--) {
      const t = ents[pp].type;
      if ((t === 1 || t === 9) && ents[pp].pair === 0) {
        ents[pp].pair = pair;
        if (t === 9) ents[pos].type = 9;
        break;
      }
    }
  }
  return ents.filter((e) => e.type < 9).map((e) => e.content).join("");
}

/** strip_span_for_page. @param {string} html */
function stripSpanForPage(html) {
  html = html.replace(/<(\S+)([^/>]*?) style="display: ?none;"([^/>]*?)><\/\1>/g, "");
  html = html.replace(/<(\S+)([^/>]*?)><\/\1>/g, "<$1$2/>");
  html = html.replace(/<([^>]*?)(\s+?)\/>/g, "<$1/>");
  html = html.replace(/<\/(b|h)r>/g, "");
  html = html.replace(/<(b|h)r([^/>]*?)\/?>/g, "<$1r$2/>");
  html = html.replace(/<(b|i|u|a|em|strong|span|big|small)\/>/g, "");
  html = html.replace(/<\?dp([^>]*?)\?>\n?/g, "");
  return dropPairedTags(html, (e) => e === "<span>");
}

/** strip_kobo_for_page. @param {string} html */
function stripKoboForPage(html) {
  html = html.replace(/<(\S+)([^/>]*?)><\/\1>/g, "<$1$2/>");
  html = html.replace(/<([^>]*?)(\s+?)\/>/g, "<$1/>");
  html = html.replace(/<span([^>]+?) id="kobo([^"]+?)"/g, '<span id="kobo$2"$1');
  html = html.replace(/<\/(b|h)r>/g, "");
  html = html.replace(/<(b|h)r([^/>]*?)\/?>/g, "<$1r$2/>");
  html = html.replace(/<(b|i|u|a|em|strong|span|big|small)\/>/g, "");
  return dropPairedTags(html, (e) => e.slice(0, 15) === '<span id="kobo.');
}

/** unpretty_for_page; pages with <pre> are left alone. @param {string} t */
function unprettyForPage(t) {
  if (/<pre\s*([^>]*?)>/i.test(t)) return t;
  t = t.replace(/\r\n?/g, "\n");
  t = t.replace(/<!--([\s\S]*?)-->/g, "");
  t = t.replace(/<\/(b|h)r>/g, "");
  t = t.replace(/!DOCTYPE([^>]*?)\n([^>]*?)>/g, "!DOCTYPE$1 $2>");
  t = t.replace(/!DOCTYPE([^>]*?)>\s*/g, "!DOCTYPE$1>\n");
  t = t.replace(/>\n\s+</g, ">\n<");
  t = t.replace(/\s+<\/([^>]+)>/g, "</$1> ");
  t = t.replace(/[^\S\n]+\n/g, "\n");
  t = t.replace(/<(\S+)([^/>]*?) style="display: ?none;?"([^/>]*?)><\/\1>/g, "");
  t = t.replace(/>\s*<(html|head|title|meta|link|style|body|h\d|ul|ol|li|p|div|section|nav|tr|td)([^>]*?)(\/?)>/g, ">\n<$1$2$3>");
  t = t.replace(/<(h\d|li|p|div|section|nav|td)([^/>]*?)>\s*<(span|b|i|a|small)/g, "<$1$2><$3");
  t = t.replace(/<(span|b|i|a|u|em|strong|small)([^>]*?)> <(span|b|i|a|u|em|strong|small)/g, " <$1$2><$3");
  t = t.replace(/>\s+<(span|b|i|a|u|em|strong|big|small)/g, "> <$1");
  t = t.replace(/\s*<(section|nav|div)([^>]*?)>/g, "\n<$1$2>");
  t = t.replace(/<(section|nav|div)([^>]*?)>\s*/g, "<$1$2>\n");
  t = t.replace(/\s*<\/(title|body|html)>\s*/g, "</$1>\n");
  t = t.replace(/\s*<\/(h\d|ul|ol|p|table|tr)>\s*/g, "</$1>\n\n");
  t = t.replace(/\s*<(b|h)r([^>]*?)\/?>\s*/g, "<$1r$2/>\n");
  t = t.replace(/<(meta|link)([^>]*?)\/?>\s*/g, "<$1$2/>\n");
  t = t.replace(/>\n*<(body|h\d|ul|ol|p|hr|table)( ?)/g, ">\n\n<$1$2");
  t = t.replace(/<(body|table|tr)([^>]*?)>\n*/g, "<$1$2>\n");
  t = t.replace(/<td([^>]*?)>\n+/g, "<td$1>\n");
  t = t.replace(/\n+<\/td>/g, "\n</td>");
  t = t.replace(/\s*<\/(div|section|nav|table|tr|ul|ol|body)>/g, "\n</$1>");
  t = t.replace(/\s*<\/head>\s*/g, "\n</head>\n\n");
  t = t.replace(/\s*<\/(body|style)>/g, "\n</$1>");
  t = t.replace(/\/html>\s+/g, "/html>");
  t = t.replace(/ +/g, " ");
  return t;
}

/**
 * container.py's ExtendedContainer, over the EPUB in memory: names are
 * paths in the zip.
 */
class Container {
  /** @param {import("../shared/epub.js").Epub} epub */
  constructor(epub) {
    this.epub = epub;
    this.opfName = epub.opfPath;
    this.opfDir = epub.opfDir;
    this.opf = parse(epub.opf);
    this.opfDirty = false;
    /** @type {Map<string, string>} */
    this.rawMap = new Map();
    /** @type {Set<string>} */
    this.dirtied = new Set();
    /** @type {Map<string, string>} */
    this.mime = new Map([[this.opfName, "application/oebps-package+xml"]]);
    for (const item of byName(this.opf, "item")) {
      const href = getAttr(item, "href");
      const mt = getAttr(item, "media-type");
      if (href && mt) this.mime.set(this.hrefToName(href), mt);
    }
    this.ncxName = [...this.manifestWorthyNames()].find((n) => n.endsWith(".ncx")) ?? null;
    this.ncx = this.ncxName ? parse(this.raw(this.ncxName)) : null;
    this.ncxDirty = false;
  }

  names() {
    return Object.keys(this.epub.files).filter((n) => n !== "mimetype" && !n.endsWith("/"));
  }

  *manifestWorthyNames() {
    for (const name of this.names()) {
      if (name.endsWith(".opf")) continue;
      if (name.startsWith("META-INF") && META_INF.has(baseName(name))) continue;
      yield name;
    }
  }

  /** @param {string} href @param {string} [base] */
  hrefToName(href, base = this.opfDir) {
    const hash = href.indexOf("#");
    const period = href.indexOf(".");
    if (hash > 0 && hash > period) href = href.slice(0, hash);
    return normpath(base + unquote(href));
  }

  /** @param {string} name */
  nameToHref(name) {
    const from = this.opfDir.split("/").filter(Boolean);
    const to = name.split("/");
    let i = 0;
    while (i < from.length && i < to.length - 1 && from[i] === to[i]) i++;
    return [...from.slice(i).map(() => ".."), ...to.slice(i)].map((p) => (p === ".." ? p : encodeURIComponent(p))).join("/");
  }

  /** abshref: href relative to base_name, as a name. @param {string} href @param {string} baseName_ */
  abshref(href, baseName_) {
    if (/^[a-z][a-z0-9+.-]*:/i.test(href) && !href.startsWith("file:")) return href;
    const [path, frag] = [href.split("#")[0], href.includes("#") ? href.slice(href.indexOf("#") + 1) : ""];
    if (!path) return frag ? `${baseName_}#${frag}` : baseName_;
    if (!baseName_.includes("/")) return path;
    return normpath(dirOf(baseName_) + path);
  }

  /** @param {string} name */
  raw(name) {
    if (this.rawMap.has(name)) return /** @type {string} */ (this.rawMap.get(name));
    const data = this.epub.files[name];
    let s = data ? strFromU8(data) : "";
    if (s.charCodeAt(0) === 0xfeff) s = s.slice(1);
    s = s.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    this.rawMap.set(name, s);
    return s;
  }

  /** @param {string} name @param {string} val */
  set(name, val) {
    this.rawMap.set(name, val);
    this.dirtied.add(name);
  }

  /** Applies fn to each HTML page; true if any changed. @param {(html: string) => string} fn */
  rewriteHtml(fn) {
    let dirtied = false;
    for (const name of this.htmlNames()) {
      const html = this.raw(name);
      const out = fn(html);
      if (out !== html) {
        this.set(name, out);
        dirtied = true;
      }
    }
    return dirtied;
  }

  /** @param {string} name */
  manifestItemFor(name) {
    return byName(this.opf, "item").find((i) => {
      const href = getAttr(i, "href");
      return href !== undefined && this.hrefToName(href) === name;
    });
  }

  /** @param {string} name @param {string} [mt] */
  addNameToManifest(name, mt) {
    if (this.manifestItemFor(name)) return;
    const manifest = byName(this.opf, "manifest")[0];
    if (!manifest) return;
    const ids = new Set(byName(this.opf, "item").map((i) => getAttr(i, "id")));
    let x = 1;
    while (ids.has(`id${x}`)) x++;
    const type = mt || GUESS[extOfName(name)] || "application/octest-stream";
    for (const n of parse(`<item href="${escape(this.nameToHref(name))}" id="id${x}" media-type="${escape(type)}"/>\n`).children) insert(manifest, n);
    this.mime.set(name, type);
    this.opfDirty = true;
  }

  /** @param {string} name */
  deleteName(name) {
    this.mime.delete(name);
    delete this.epub.files[name];
    this.rawMap.delete(name);
    this.dirtied.delete(name);
  }

  /** Removes the file and its manifest item, spine entry, guide reference and TOC entries. @param {string} name */
  deleteFromManifest(name, deleteFromToc = true) {
    this.deleteName(name);
    const item = this.manifestItemFor(name);
    if (!item) return;
    removeTidy(item);
    this.opfDirty = true;
    const id = getAttr(item, "id");
    const ref = byName(this.opf, "itemref").find((r) => getAttr(r, "idref") === id);
    if (ref) removeTidy(ref);
    const href = getAttr(item, "href");
    const gref = byName(this.opf, "reference").find((r) => getAttr(r, "href") === href);
    if (gref) removeTidy(gref);
    if (deleteFromToc) this.deleteFromToc(this.hrefToName(href ?? ""));
  }

  /** @param {string} itemName */
  deleteFromToc(itemName) {
    if (!this.ncx || !this.ncxName) return;
    let dirtied = false;
    for (const np of byName(this.ncx, "navpoint")) {
      const content = childElements(np).find((e) => local(e) === "content");
      const src = content ? getAttr(content, "src") : undefined;
      if (src === undefined) continue;
      if (this.abshref(src.split("#")[0], this.ncxName).toLowerCase() !== itemName.toLowerCase()) continue;
      this.promoteAndRemove(np);
      dirtied = true;
    }
    if (this.fixTocPlayorder() || dirtied) this.ncxDirty = true;
  }

  /** Removes a navPoint, its child navPoints taking its place. @param {import("../shared/markup.js").Element} np */
  promoteAndRemove(np) {
    const p = np.parent;
    if (!p) return;
    const idx = removeTidy(np);
    const kids = childElements(np).filter((e) => local(e) === "navpoint");
    for (const child of kids.reverse()) insert(p, child, idx);
  }

  fixTocPlayorder() {
    if (!this.ncx) return false;
    let changed = false;
    let order = 1;
    for (const np of byName(this.ncx, "navpoint")) {
      const existing = getAttr(np, "playOrder");
      if (existing) {
        if (existing !== String(order)) {
          const a = np.attrs.find(([k]) => k === "playOrder");
          if (a) a[1] = String(order);
          np.dirty = true;
          changed = true;
        }
        order++;
      }
    }
    return changed;
  }

  flattenToc() {
    if (!this.ncx) return false;
    const all = byName(this.ncx, "navpoint");
    if (!all.some((np) => np.parent && np.parent.type === "element" && local(np.parent) === "navpoint")) return false;
    for (const np of all) {
      const kids = childElements(np).filter((e) => local(e) === "navpoint");
      if (!kids.length || !np.parent) continue;
      const p = np.parent;
      const idx = p.children.indexOf(np) + 1;
      for (const child of kids.reverse()) insert(p, child, idx);
    }
    this.ncxDirty = true;
    return true;
  }

  /** @param {Set<string>} htmlNames lower case */
  deleteBrokenTocLinks(htmlNames) {
    if (!this.ncx || !this.ncxName) return false;
    let dirtied = false;
    for (const np of byName(this.ncx, "navpoint")) {
      const content = childElements(np).find((e) => local(e) === "content");
      const src = content ? getAttr(content, "src") : undefined;
      if (src === undefined) continue;
      const link = this.abshref(unquote(src).split("#")[0], this.ncxName);
      if (htmlNames.has(link.toLowerCase())) continue;
      this.promoteAndRemove(np);
      dirtied = true;
    }
    if (this.fixTocPlayorder() || dirtied) {
      this.ncxDirty = true;
      dirtied = true;
    }
    return dirtied;
  }

  isDrmEncrypted() {
    for (const name of this.names()) {
      if (!name.toLowerCase().endsWith("encryption.xml")) continue;
      for (const m of this.raw(name).matchAll(/<[^>]*EncryptionMethod\b[^>]*Algorithm\s*=\s*["']([^"']*)["']/g)) {
        if (m[1] !== ADOBE_OBFUSCATION && m[1] !== IDPF_OBFUSCATION) return true;
      }
      return false;
    }
    return false;
  }

  *pagemapNames() {
    for (const name of this.names()) if ((this.mime.get(name) ?? "").toLowerCase() === "application/oebps-page-map+xml") yield name;
  }

  *xpgtNames() {
    const types = ["application/adobe-page-template+xml", "application/vnd.adobe-page-template+xml", "application/vnd.adobe.page-template+xml"];
    for (const name of this.names()) if (types.includes((this.mime.get(name) ?? "").toLowerCase())) yield name;
  }

  /** @returns {string[]} */
  htmlNames() {
    return this.names().filter((name) => !NON_HTML_FILES.includes(extOfName(name)) && (this.mime.get(name) ?? "").includes("html"));
  }

  cssNames() {
    return this.names().filter((n) => n.toLowerCase().endsWith(".css"));
  }

  imageNames() {
    return this.names().filter((n) => IMAGE_FILES.includes(extOfName(n)));
  }

  /** get_page_image_names: <svg><image xlink:href> and <img src>, as names. @param {string} html @returns {[string, string][]} */
  pageImageNames(html) {
    if (!this.epub.files[html]) return [];
    const doc = parse(this.raw(html), { html: true });
    /** @type {[string, string][]} */
    const out = [];
    for (const svg of byName(doc, "svg")) {
      const image = childElements(svg).find((e) => local(e) === "image");
      const href = image ? getAttr(image, "xlink:href") : undefined;
      if (href !== undefined) out.push([this.abshref(unquote(href), html), href]);
    }
    for (const img of byName(doc, "img")) {
      const src = getAttr(img, "src");
      if (src !== undefined) out.push([this.abshref(unquote(src), html), src]);
    }
    return out;
  }

  /** remove_unused_images. @param {string[]} imageNames */
  removeUnusedImages(imageNames) {
    if (!imageNames.length) return false;
    const missing = new Map(imageNames.map((n) => [n.toLowerCase(), n]));
    for (const html of this.htmlNames()) {
      for (const [name] of this.pageImageNames(html)) missing.delete(name.toLowerCase());
      if (!missing.size) break;
    }
    /** @param {string} n */
    const pattern = (n) => new RegExp(baseName(n).replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "iu");
    if (missing.size) {
      for (const css of this.cssNames()) {
        const data = this.raw(css);
        for (const [key, name] of [...missing]) if (pattern(name).test(data)) missing.delete(key);
        if (!missing.size) break;
      }
    }
    if (missing.size) {
      const meta = byName(this.opf, "meta").find((m) => getAttr(m, "name") === "cover");
      const coverId = meta ? getAttr(meta, "content") : undefined;
      const item = coverId ? byName(this.opf, "item").find((i) => getAttr(i, "id") === coverId) : undefined;
      const href = item ? getAttr(item, "href") ?? "" : "";
      if (item) for (const [key, name] of [...missing]) if (pattern(name).test(href)) missing.delete(key);
    }
    for (const name of missing.values()) this.deleteFromManifest(name);
    return missing.size > 0;
  }

  /** The body's text, without style and title elements or whitespace. @param {string} html */
  bodyText(html) {
    const doc = parse(this.raw(html), { html: true });
    for (const el of [...byName(doc, "style"), ...byName(doc, "title")]) remove(el);
    const body = byName(doc, "body")[0];
    return body ? textOf(body).replace(/\s+/g, "") : "";
  }

  /** Writes what changed back into the EPUB's files. */
  write() {
    for (const name of this.dirtied) if (this.rawMap.has(name)) this.epub.files[name] = strToU8(/** @type {string} */ (this.rawMap.get(name)));
    if (this.ncx && this.ncxName && this.ncxDirty) this.epub.files[this.ncxName] = strToU8(serialize(this.ncx));
    if (this.opfDirty) {
      this.epub.opf = serialize(this.opf);
      this.epub.files[this.opfName] = strToU8(this.epub.opf);
    }
  }
}

/** The image format from its first bytes. @param {Uint8Array} d */
function imageType(d) {
  if (d[0] === 0xff && d[1] === 0xd8) return "jpeg";
  if (d[0] === 0x89 && d[1] === 0x50 && d[2] === 0x4e && d[3] === 0x47) return "png";
  if (d[0] === 0x47 && d[1] === 0x49 && d[2] === 0x46) return "gif";
  return "";
}

/** An image's width and height from its header, or null. @param {Uint8Array} d @returns {[number, number] | null} */
function imageSize(d) {
  const t = imageType(d);
  if (t === "png") return [(d[16] << 24) | (d[17] << 16) | (d[18] << 8) | d[19], (d[20] << 24) | (d[21] << 16) | (d[22] << 8) | d[23]];
  if (t === "gif") return [d[6] | (d[7] << 8), d[8] | (d[9] << 8)];
  if (t === "jpeg") {
    let i = 2;
    while (i + 9 < d.length) {
      if (d[i] !== 0xff) {
        i++;
        continue;
      }
      const marker = d[i + 1];
      const len = (d[i + 2] << 8) | d[i + 3];
      if (marker >= 0xc0 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc) return [(d[i + 7] << 8) | d[i + 8], (d[i + 5] << 8) | d[i + 6]];
      i += 2 + len;
    }
  }
  return null;
}

/** calibre's CoverManager.SVG_TEMPLATE, filled as CoverManager.fill_metadata does. */
const SVG_TEMPLATE = `<html xmlns="http://www.w3.org/1999/xhtml"__lang__>
    <head>
        <meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
        <meta name="calibre:cover" content="true" />
        <title>__title__</title>
        <style type="text/css" title="override_css">
            @page {padding: 0pt; margin:0pt}
            body { text-align: center; padding:0pt; margin: 0pt; }
        </style>
    </head>
    <body>
        <div>
            <svg version="1.1" xmlns="http://www.w3.org/2000/svg"
                xmlns:xlink="http://www.w3.org/1999/xlink"
                width="100%" height="100%" viewBox="__viewbox__"
                preserveAspectRatio="__ar__">
                <title>__title__</title>
                <image width="__width__" height="__height__" xlink:href="__href__"/>
            </svg>
        </div>
    </body>
</html>
`;

/** covers.py's CoverUpdater, over the Container. */
class CoverUpdater {
  /** @param {Container} c @param {Uint8Array | null} coverData the library cover @param {string} title */
  constructor(c, coverData, title) {
    this.c = c;
    this.coverData = coverData;
    this.title = title;
    /** @type {string[]} */
    this.potentialUnusedImageNames = [];
    /** @type {import("../shared/markup.js").Element | null} */
    this.tocOldCoverContentNode = null;
    /** @type {string | null} */
    this.removedHtmlName = null;
    this.imagesFolder = "";
    this.textFolder = "";
  }

  get opf() {
    return this.c.opf;
  }

  removeExistingCover() {
    const item = this.getAndCleanExistingCoverItem();
    if (item) this.removeOldCoverIfSafe(item);
    if (this.removedHtmlName) {
      if (this.tocOldCoverContentNode) this.c.deleteFromToc(this.removedHtmlName);
      this.updateHtmlLinksToOldCover(this.removedHtmlName, null);
    }
    this.c.removeUnusedImages(this.potentialUnusedImageNames);
  }

  insertOrReplaceCover() {
    this.identifyImageTextFolders();
    const item = this.getAndCleanExistingCoverItem();
    if (item) this.removeOldCoverIfSafe(item);
    const [titlepage, cover] = this.createNewCover();
    const newTitleName = this.addNewCoverToManifest(titlepage, cover);
    if (this.removedHtmlName && this.removedHtmlName !== newTitleName) this.updateHtmlLinksToOldCover(this.removedHtmlName, newTitleName);
    this.c.removeUnusedImages(this.potentialUnusedImageNames);
    this.ensureOpfContainsCoverFixes();
  }

  identifyImageTextFolders() {
    const img = this.c.imageNames().find((n) => n.includes("/"));
    this.imagesFolder = img ? img.slice(0, img.lastIndexOf("/")) : "";
    const html = this.c.htmlNames().find((n) => n.includes("/"));
    this.textFolder = html ? html.slice(0, html.lastIndexOf("/")) : "";
  }

  /** @param {string} type */
  guideReference(type) {
    const guide = byName(this.opf, "guide")[0];
    return guide ? byName(guide, "reference").find((r) => getAttr(r, "type") === type) : undefined;
  }

  /** @param {string} id */
  itemById(id) {
    const manifest = byName(this.opf, "manifest")[0];
    return manifest ? byName(manifest, "item").find((i) => getAttr(i, "id") === id) : undefined;
  }

  /** @param {import("../shared/markup.js").Element} item */
  deleteFromGuide(item) {
    const href = getAttr(item, "href");
    const guide = byName(this.opf, "guide")[0];
    const ref = guide ? byName(guide, "reference").find((r) => getAttr(r, "href") === href) : undefined;
    if (ref) {
      removeTidy(ref);
      this.c.opfDirty = true;
    }
  }

  getAndCleanExistingCoverItem() {
    let item = this.getGuideCoverItem();
    const metaItem = this.getAndCleanMetaCover();
    if (!item) item = metaItem;
    const other = this.cleanOtherGuideCoverReferences();
    if (!item) item = other;
    if (!item) return undefined;
    this.deleteFromGuide(item);
    if ((getAttr(item, "media-type") ?? "").toLowerCase().startsWith("image/")) {
      item = this.findItemForCoverImage(item);
      if (!item) return undefined;
    }
    this.tocOldCoverContentNode = this.tocNavpointContent(item);
    return item;
  }

  getGuideCoverItem() {
    const ref = this.guideReference("cover");
    if (!ref) return undefined;
    return this.c.manifestItemFor(this.c.hrefToName((getAttr(ref, "href") ?? "").split("#")[0]));
  }

  getAndCleanMetaCover() {
    const metadata = byName(this.opf, "metadata")[0];
    const meta = metadata ? byName(metadata, "meta").find((m) => getAttr(m, "name") === "cover" && getAttr(m, "content") !== undefined) : undefined;
    if (!meta) return undefined;
    const id = getAttr(meta, "content") ?? "";
    removeTidy(meta);
    this.c.opfDirty = true;
    return this.itemById(id);
  }

  cleanOtherGuideCoverReferences() {
    /** @type {[import("../shared/markup.js").Element, number][]} */
    const covers = [];
    const types = ["coverimagestandard", "other.ms-coverimage-standard", "other.ms-titleimage-standard", "other.ms-titleimage", "other.ms-coverimage", "other.ms-thumbimage-standard", "other.ms-thumbimage", "thumbimagestandard"];
    for (const x of types) {
      const ref = this.guideReference(x);
      if (!ref) continue;
      let href = getAttr(ref, "href") ?? "";
      let name = this.c.hrefToName(href);
      if (!this.c.epub.files[name]) {
        const found = this.c.names().find((n) => n.toLowerCase() === name.toLowerCase());
        if (!found) continue;
        name = found;
        href = this.c.nameToHref(name);
        setAttr(ref, "href", href);
      }
      covers.push([ref, this.c.epub.files[name].length]);
      this.potentialUnusedImageNames.push(name);
      this.deleteFromGuide(ref);
    }
    covers.sort((a, b) => b[1] - a[1]);
    if (!covers.length) return undefined;
    return this.c.manifestItemFor(this.c.hrefToName(getAttr(covers[0][0], "href") ?? ""));
  }

  /** @param {import("../shared/markup.js").Element} imageItem */
  findItemForCoverImage(imageItem) {
    const coverName = this.c.hrefToName(getAttr(imageItem, "href") ?? "").toLowerCase();
    for (const ref of byName(this.opf, "itemref")) {
      const item = this.itemById(getAttr(ref, "idref") ?? "");
      if (!item) continue;
      const html = this.c.hrefToName(getAttr(item, "href") ?? "");
      if (this.c.pageImageNames(html).some(([n]) => n.toLowerCase() === coverName)) return item;
    }
    return undefined;
  }

  /** @param {import("../shared/markup.js").Element} item */
  tocNavpointContent(item) {
    if (!this.c.ncx || !this.c.ncxName) return null;
    const href = this.c.hrefToName(getAttr(item, "href") ?? "");
    for (const np of byName(this.c.ncx, "navpoint")) {
      const content = childElements(np).find((e) => local(e) === "content");
      if (!content) continue;
      const src = unquote(getAttr(content, "src") ?? "").split("#")[0];
      if (this.c.abshref(src, this.c.ncxName).toLowerCase() === href.toLowerCase()) return content;
    }
    return null;
  }

  /** @param {import("../shared/markup.js").Element} coverItem */
  removeOldCoverIfSafe(coverItem) {
    const html = this.c.hrefToName(getAttr(coverItem, "href") ?? "");
    this.removedHtmlName = "";
    const doc = parse(this.c.raw(html), { html: true });
    // the page's first <img> or <svg><image>, removed from the page
    const images = [...byName(doc, "svg").map((svg) => childElements(svg).find((e) => local(e) === "image")).filter((x) => x && getAttr(x, "xlink:href") !== undefined), ...byName(doc, "img").filter((i) => getAttr(i, "src") !== undefined)];
    let imageName = null;
    const first = /** @type {import("../shared/markup.js").Element | undefined} */ (images[0]);
    if (first) {
      imageName = this.c.abshref(unquote(getAttr(first, "xlink:href") ?? getAttr(first, "src") ?? ""), html);
      remove(first);
    }
    const bodyText = this.c.bodyText(html);
    if (!bodyText && images.length <= 1) {
      this.c.deleteFromManifest(html, false);
      this.removedHtmlName = html;
    } else this.c.set(html, serialize(doc));
    if (imageName) {
      const imageItem = this.c.manifestItemFor(imageName);
      if (imageItem && !this.findItemForCoverImage(imageItem)) this.c.deleteFromManifest(imageName);
    }
  }

  /** _create_new_cover: the library cover, and calibre's SVG titlepage for it. */
  createNewCover() {
    const data = /** @type {Uint8Array} */ (this.coverData);
    // calibre re-encodes the cover as a JPEG scaled to the output profile;
    // it is written as it is here.
    const ext = imageType(data) === "png" ? "png" : imageType(data) === "gif" ? "gif" : "jpeg";
    const coverName = this.uniqueName(this.imagesFolder ? `${this.imagesFolder}/cover.${ext}` : `cover.${ext}`);
    this.c.epub.files[coverName] = data;
    const [width, height] = imageSize(data) ?? [600, 800];
    const tname = this.uniqueName(this.textFolder ? `${this.textFolder}/titlepage.xhtml` : "titlepage.xhtml");
    const lang = byName(this.opf, "language")[0];
    const rel = this.relPath(coverName, dirOf(tname));
    const page = SVG_TEMPLATE.replace("__ar__", "none")
      .replace("__viewbox__", `0 0 ${width} ${height}`)
      .replace("__width__", String(width))
      .replace("__height__", String(height))
      .replaceAll("__title__", escape(this.title.trim() || "Cover"))
      .replace("__lang__", lang && textOf(lang).trim() ? ` xml:lang="${escape(textOf(lang).trim())}"` : "")
      .replace("__href__", escape(rel));
    this.c.epub.files[tname] = strToU8(page);
    return [tname, coverName];
  }

  /** _get_unique_filename. @param {string} preferred */
  uniqueName(preferred) {
    let name = normpath(preferred);
    const dot = name.lastIndexOf(".");
    const [base, ext] = [name.slice(0, dot), name.slice(dot)];
    for (let c = 1; this.c.epub.files[name]; c++) name = `${base}_u${c}${ext}`;
    return name;
  }

  /** os.path.relpath for zip names. @param {string} name @param {string} dir */
  relPath(name, dir) {
    const from = dir.split("/").filter(Boolean);
    const to = name.split("/");
    let i = 0;
    while (i < from.length && i < to.length - 1 && from[i] === to[i]) i++;
    return [...from.slice(i).map(() => ".."), ...to.slice(i)].join("/");
  }

  /** @param {string} titlepage @param {string} cover */
  addNewCoverToManifest(titlepage, cover) {
    const [tid, thref] = this.generateUnique("titlepage", this.c.nameToHref(titlepage));
    this.addToManifest(tid, thref, "application/xhtml+xml");
    const [cid, chref] = this.generateUnique("cover", this.c.nameToHref(cover));
    this.addToManifest(cid, chref, GUESS[extOfName(cover)] ?? "image/jpeg");
    const metadata = byName(this.opf, "metadata")[0];
    if (metadata) addChild(metadata, `<meta name="cover" content="${escape(cid)}"/>`);
    const spine = byName(this.opf, "spine")[0];
    if (spine) addChild(spine, `<itemref idref="${escape(tid)}"/>`, 0);
    let guide = byName(this.opf, "guide")[0];
    if (!guide) {
      const pkg = byName(this.opf, "package")[0];
      if (pkg) {
        addChild(pkg, "<guide>\n</guide>");
        guide = byName(this.opf, "guide")[0];
      }
    }
    if (guide) addChild(guide, `<reference href="${escape(thref)}" title="Cover" type="cover"/>`);
    this.c.opfDirty = true;
    if (this.tocOldCoverContentNode && this.c.ncxName) {
      const tname = this.c.hrefToName(thref);
      setAttr(this.tocOldCoverContentNode, "src", this.relPath(tname, dirOf(this.c.ncxName)));
      this.c.ncxDirty = true;
    }
    return this.c.hrefToName(thref);
  }

  /** generate_unique. @param {string} id @param {string} href @returns {[string, string]} */
  generateUnique(id, href) {
    const items = byName(this.opf, "item");
    const ids = new Set(items.map((i) => getAttr(i, "id")));
    let newId = id;
    for (let n = 1; ids.has(newId); n++) newId = id + n;
    const hrefs = new Set(items.map((i) => (getAttr(i, "href") ?? "").toLowerCase()));
    const dot = href.lastIndexOf(".");
    let newHref = href;
    for (let n = 1; hrefs.has(newHref.toLowerCase()); n++) newHref = href.slice(0, dot) + n + href.slice(dot);
    return [newId, newHref];
  }

  /** @param {string} id @param {string} href @param {string} mt */
  addToManifest(id, href, mt) {
    const manifest = byName(this.opf, "manifest")[0];
    if (manifest) addChild(manifest, `<item href="${escape(href)}" id="${escape(id)}" media-type="${escape(mt)}"/>`);
    this.c.mime.set(this.c.hrefToName(href), mt);
  }

  /** @param {string} removed @param {string | null} newTitle */
  updateHtmlLinksToOldCover(removed, newTitle) {
    for (const html of this.c.htmlNames()) {
      const doc = parse(this.c.raw(html), { html: true });
      let changed = false;
      for (const a of byName(doc, "a")) {
        const href = getAttr(a, "href");
        if (href === undefined) continue;
        if (this.c.abshref(unquote(href).split("#")[0], html).toLowerCase() !== removed.toLowerCase()) continue;
        if (newTitle) setAttr(a, "href", this.relPath(newTitle, dirOf(html)));
        else remove(a);
        changed = true;
      }
      if (changed) this.c.set(html, serialize(doc));
    }
  }

  /**
   * _ensure_opf_contains_cover_fixes: calibre's workaround for the Nook
   * (the cover image's id is "cover") and Pocketbook (that item first).
   */
  ensureOpfContainsCoverFixes() {
    const metas = byName(this.opf, "meta").filter((m) => getAttr(m, "name") === "cover" && getAttr(m, "content") !== "cover");
    if (metas.length === 1) {
      const covid = getAttr(metas[0], "content") ?? "";
      const items = byName(this.opf, "item").filter((i) => getAttr(i, "id") === covid);
      if (covid && items.length === 1 && (getAttr(items[0], "media-type") ?? "").startsWith("image/")) {
        const newid = `id${Math.random().toString(16).slice(2, 10)}`;
        for (const el of elements(this.opf)) {
          if (getAttr(el, "id") === "cover") setAttr(el, "id", newid);
          if (getAttr(el, "idref") === "cover") setAttr(el, "idref", newid);
        }
        setAttr(items[0], "id", "cover");
        setAttr(metas[0], "content", "cover");
      }
    }
    const item = this.itemById("cover");
    const manifest = byName(this.opf, "manifest")[0];
    if (item && manifest) {
      removeTidy(item);
      addChild(manifest, serialize(item), 0);
    }
    this.c.opfDirty = true;
  }
}

/**
 * Adds an element, given as source, to parent at index (default last),
 * on its own line as fix_tail_after_insert does.
 * @param {import("../shared/markup.js").Element} parent @param {string} src @param {number} [index]
 */
function addChild(parent, src, index) {
  const nodes = parse(src).children;
  let at = index ?? parent.children.length;
  if (index === undefined) {
    // before the whitespace that precedes the closing tag
    const last = parent.children[parent.children.length - 1];
    if (last && last.type === "text" && !/\S/.test(last.raw)) at--;
  }
  const ws = parse("\n").children[0];
  insert(parent, ws, at);
  for (const [k, n] of nodes.entries()) insert(parent, n, at + 1 + k);
}
