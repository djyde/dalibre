# Count Pages

Counts each book's words and estimates its pages. Ported from calibre's [Count Pages](https://github.com/kiwidude68/calibre_plugins/tree/main/count_pages) plugin by Grant Drake ([`statistics.py`](https://github.com/kiwidude68/calibre_plugins/tree/main/count_pages/statistics.py)) and calibre's word count ([`wordcount.py`](https://github.com/kovidgoyal/calibre/blob/master/src/calibre/utils/wordcount.py)), GPL-3.0.

Choose books, then **Count pages…** and a page algorithm: Paragraphs (APNX accurate, the default), E-book Viewer (calibre), Adobe Digital Editions or Custom chars per page. The form keeps your last choices, as calibre keeps the plugin's options. Each book is listed with "312 pages · 84,210 words". Words are counted as calibre does: between spaces, with each Chinese, Japanese or Korean character a word.

Dalibre has no custom columns: turn on **Add a pages:N tag** to keep the count as a tag. Works on EPUB, KEPUB, TXT, HTMLZ and FB2.

Not ported: PDF and comic page counts, readability statistics, downloading counts from Goodreads and others, ICU word count. The E-book Viewer algorithm counts the text's characters rather than rendering the book. Other formats are read as they are, not converted to EPUB first.
