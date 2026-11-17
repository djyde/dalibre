// @ts-check
// A Dalibre book source. Try it without the app:
//   dalibre-plugin search . "some title" --filter language=en
//   dalibre-plugin get . "some title"
//   dalibre-plugin download . "some title" --format EPUB
// The API is described in dalibre.d.ts.

const API = "https://api.example.com";

/**
 * Searches the catalogue. Called again with page + 1 for more.
 * @param {Dalibre.Query} query
 * @returns {Promise<Dalibre.Page>}
 */
export async function search({ text, page, filters }) {
  let url = `${API}/search?q=${encodeURIComponent(text)}&page=${page}`;
  if (filters.language) url += `&lang=${filters.language}`;
  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${dalibre.settings.apiKey}` },
  });
  if (res.status === 401) throw dalibre.error("auth", "The API key was refused");
  if (res.status === 429) throw dalibre.error("rateLimited");
  if (!res.ok) throw dalibre.error("unavailable", `example.com answered ${res.status}`);

  const data = await res.json();
  return {
    items: data.results.map((/** @type {any} */ b) => ({
      id: String(b.id),
      title: b.title,
      authors: b.authors,
      language: b.language,
      identifiers: { isbn: b.isbn },
      cover: b.cover_url,
      formats: [{ format: "EPUB", url: b.epub_url, size: b.epub_size }],
    })),
    hasMore: Boolean(data.next),
  };
}

// Optional: everything you know about a result, for the detail panel when
// the user opens it (a description, subjects, file sizes, url, fields).
//
// /**
//  * @param {Dalibre.Result} item
//  * @returns {Promise<Dalibre.Result>}
//  */
// export async function get(item) {
//   const b = await (await fetch(`${API}/books/${item.id}`)).json();
//   return { ...item, description: b.summary, tags: b.subjects, url: b.page_url,
//            fields: [{ label: "Rights", value: b.rights }] };
// }

// Optional: lists to browse while the search box is empty. Add
// "collections": true to capabilities.bookSource in plugin.json.
//
// export function collections() {
//   return [{ id: "popular", title: "Most popular" }];
// }
// export async function list({ collection, page, filters }) { … }

// Optional: for download links that expire or need a second request.
// Without it, the host downloads format.url.
//
// /**
//  * @param {Dalibre.Result} item
//  * @param {Dalibre.FormatName} format
//  * @returns {Promise<Dalibre.DownloadRequest>}
//  */
// export async function resolve(item, format) {
//   const res = await fetch(`${API}/books/${item.id}/download?format=${format}`);
//   return { url: (await res.json()).url };
// }
