// Makes small EPUBs to try the plugins on:
//   node test/make-epub.mjs out.epub "Title" "Author" [chapters] [isbn]
import { zipSync, strToU8 } from "fflate";
import { writeFileSync } from "node:fs";

const [out, title = "Sample Book", author = "Ann Author", n = "3", isbn = ""] = process.argv.slice(2);
const count = Number(n);
const chapters = Array.from({ length: count }, (_, i) => i + 1);
const para = (i) => `<p>This is paragraph ${i} of a sample chapter. "It's a test," she said -- quietly... The quick brown fox jumps over the lazy dog. 这是一个简体中文的句子，用于测试软件。</p>`;
const files = {
  mimetype: [strToU8("application/epub+zip"), { level: 0 }],
  "META-INF/container.xml": strToU8(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
  "OEBPS/content.opf": strToU8(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="uid">urn:uuid:12345678-1234-4123-8123-123456789abc</dc:identifier>
<dc:title>${title}</dc:title><dc:creator>${author}</dc:creator><dc:language>en</dc:language>
<meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>
</metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="css" href="style.css" media-type="text/css"/>
${chapters.map((i) => `<item id="c${i}" href="text/ch${i}.xhtml" media-type="application/xhtml+xml"/>`).join("\n")}
</manifest>
<spine>${chapters.map((i) => `<itemref idref="c${i}"/>`).join("")}</spine>
</package>`),
  "OEBPS/style.css": strToU8("p { text-indent: 1em; }"),
  "OEBPS/nav.xhtml": strToU8(`<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol>${chapters.map((i) => `<li><a href="text/ch${i}.xhtml">Chapter ${i}</a></li>`).join("")}</ol></nav></body></html>`),
};
for (const i of chapters) {
  const extra = i === 1 && isbn ? `<p>ISBN ${isbn}</p>` : "";
  files[`OEBPS/text/ch${i}.xhtml`] = strToU8(`<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter ${i}</title><link rel="stylesheet" href="../style.css"/></head><body><h1>Chapter ${i}</h1>${extra}${Array.from({ length: 40 }, (_, k) => para(k)).join("")}</body></html>`);
}
writeFileSync(out, zipSync(files));
