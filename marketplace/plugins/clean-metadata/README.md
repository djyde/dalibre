# Clean Metadata

Cleans up titles, authors and series. Ported from the calibre plugin [Clean Metadata](https://www.mobileread.com/forums/showthread.php?t=249929) 0.0.6 by WS64 (`main.py`), GPL v3 as its files state.

Choose books and **Clean metadata…** from the book menu, or **Tools › Clean metadata…** for the whole library, then the cleanups:

- **Cleanup German umlauts in titles**: "Ueberfall" → "Überfall", with the plugin's exceptions; optionally German books only.
- **Cleanup titles**: drops trailers such as "(German Edition)", ". A novel", "- Thriller", file extensions, the author's name, underscores; moves a trailing article to the front.
- **Get series info from title**: "Harry Potter 3: The Prisoner of Azkaban" → series Harry Potter, 3; optionally skipping books already in a series.
- **Cleanup authors**: splits "A; B", turns "Last, First" round, formats initials ("S King" → "S. King").
- **Swap author and title** when the title is an author of another book.
- **Find duplicate author candidates**: lists books by authors spelt alike ("Dean Koontz", "Dean R. Koontz").

Dalibre shows the changes for review before applying them, so the plugin's Mark only and Execute are one step here.
