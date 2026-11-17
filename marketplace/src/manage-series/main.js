// @ts-check
// Manage Series, after the calibre plugin by Grant Drake (GPL v3):
// https://github.com/kiwidude68/calibre_plugins/tree/main/manage_series
// Follows its dialog's start index, sort orders and renumbering.

/** @param {string} action @param {Dalibre.ActionInput} input @returns {Promise<Dalibre.ActionResult>} */
export async function run(action, input) {
  const books = input.books;
  if (!books.length) throw dalibre.error("invalid", "Choose at least one book.");

  if (action === "clear-series") {
    const changes = books.filter((b) => b.series).map((b) => ({ book: b.id, metadata: { series: "" } }));
    if (!changes.length) return { message: "None of these books is in a series" };
    return { message: `${changes.length} ${changes.length === 1 ? "book" : "books"} out of their series`, changes };
  }
  if (action !== "set-series") throw dalibre.error("invalid", `Unknown action ${action}.`);

  // As the plugin: the first book's series, and its whole index to start at.
  const first = books[0];
  const a = await dalibre.form({
    submit: "Set series",
    fields: [
      { key: "series", title: "Series", type: "text", value: first.series ?? "" },
      { key: "start", title: "Start at", type: "text", value: String(first.series ? Math.trunc(first.seriesIndex ?? 1) : 1) },
      {
        key: "sort", title: "Order", type: "select", value: "selection",
        options: [
          { value: "selection", title: "As chosen" },
          { value: "series", title: "Original series index" },
          { value: "name", title: "Original series name" },
          { value: "pubdate", title: "Published date" },
          { value: "title", title: "Title" },
        ],
      },
    ],
  });
  const series = String(a.series ?? "").trim();
  if (!series) throw dalibre.error("invalid", "Enter a series name.");
  const start = parseInt(String(a.start).trim(), 10);
  if (!Number.isFinite(start) || start < 0) throw dalibre.error("invalid", "Start at must be a whole number, 0 or more.");

  let ordered = books;
  if (a.sort !== "selection") {
    const key = (/** @type {Dalibre.BookInfo} */ b) => sortKey(b, String(a.sort));
    ordered = [...books].sort((x, y) => (key(x) < key(y) ? -1 : key(x) > key(y) ? 1 : 0));
  }

  // Each book one more than the last; the dialog's indents and locked
  // indexes have no counterpart in a form.
  const changes = ordered.map((b, row) => ({ book: b.id, metadata: { series, seriesIndex: start + row } }));
  return { message: `${changes.length} ${changes.length === 1 ? "book" : "books"} in ${series}`, changes };
}

/** The plugin's sort keys. @param {Dalibre.BookInfo} b @param {string} by */
function sortKey(b, by) {
  if (by === "pubdate") {
    // No date (calibre's year 101) gives an empty key, so those books come first, as in the plugin.
    if (b.pubdate && parseInt(b.pubdate, 10) > 101) return b.pubdate.slice(0, 10).replace(/-/g, "");
    return "";
  }
  if (by === "title") return b.title.toLowerCase(); // the plugin uses ICU; moejs has no Intl.Collator
  if (b.series) {
    const idx = (b.seriesIndex ?? 1).toFixed(2).padStart(6, "0");
    return by === "name" ? b.series + idx : idx + b.series;
  }
  return "";
}
