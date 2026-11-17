package app

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
)

// welcomeState is what the welcome screen keeps: the covers on its wall,
// calibre's own library when there is one, and its two sheets.
type welcomeState struct {
	// coversNet fetches the wall's covers from NeoDB; tests leave it off
	// and see the typographic covers.
	coversNet     bool
	coversStarted bool
	bitmaps       []*ui.Bitmap // by welcomeBooks index; nil until loaded
	box           ui.Handle    // the wall, measured to cover the window

	// found is the library calibre last used, when it has one.
	found string
	// missing marks recent libraries whose folder is gone, as last looked.
	missing  map[string]bool
	checking bool

	create createSheet
	notLib notLibSheet
}

// createSheet is the Create a new library sheet.
type createSheet struct {
	open         bool
	name, parent string
}

// notLibSheet asks what to do with a folder that holds no library.
type notLibSheet struct {
	open  bool
	root  string
	files int // what the folder holds already, hidden files aside
}

// welcomeBook is a cover on the welcome screen's wall: well-known books,
// their covers from NeoDB (neodb.social).
type welcomeBook struct{ title, author, cover string }

var welcomeBooks = []welcomeBook{
	{"三体", "刘慈欣", "https://neodb.social/m/item/bangumi/2024/12/22/184db1d2-e3a0-4e55-8111-464c00d8e8c8.jpg"},
	{"活着", "余华", "https://neodb.social/m/book/2021/09/1561f352a4-5276-4846-8e22-9287f21efd02.jpg"},
	{"百年孤独", "加西亚·马尔克斯", "https://neodb.social/m/book/2021/09/130e598f31-673c-4530-aa8d-87a59d783f8e.jpg"},
	{"挪威的森林", "村上春树", "https://neodb.social/m/book/2021/09/134af5adaf-cffe-4d55-9640-3d50e89e748e.jpg"},
	{"小王子", "圣-埃克苏佩里", "https://neodb.social/m/book/2021/09/1360f4c4c2-6b01-4adf-ad2b-3d202ff12ab2.jpg"},
	{"悉达多", "赫尔曼·黑塞", "https://neodb.social/m/item/book/2024/04/29/e1b700fe-0026-4268-a135-297d12b24b40.jpg"},
	{"挽救计划", "Andy Weir", "https://neodb.social/m/item/goodreads/2026/04/18/72dc1462-36b5-48f4-8007-1f898ec6885b.jpg"},
	{"Dune", "Frank Herbert", "https://neodb.social/m/book/2021/09/1628ce759c-c78c-4b9f-aca0-b8f9c7cf029b.jpg"},
	{"1984", "George Orwell", "https://neodb.social/m/item/fedi/2026/07/16/93d3d240-5546-4ee9-aba1-5955ffe4d91c.jpg"},
	{"了不起的盖茨比", "菲茨杰拉德", "https://neodb.social/m/book/2021/09/15fce64c47-af7f-472b-882e-6022b817a91a.jpg"},
	{"傲慢与偏见", "简·奥斯汀", "https://neodb.social/m/book/2021/09/15ba4e2eec-fd21-464c-916d-000e400b4660.jpg"},
	{"红楼梦", "曹雪芹", "https://neodb.social/m/book/2021/09/15402ade4c-ef1e-4a23-ae3f-55be46c1c7f9.jpg"},
	{"围城", "钱钟书", "https://neodb.social/m/book/2021/09/134f990af9-f573-486d-b339-34dd5ee48506.jpg"},
	{"平面国", "埃德温·A·艾勃特", "https://neodb.social/m/book/2021/09/1518b341f5-1978-4821-b777-8f310ac0ed59.jpg"},
	{"使女的故事", "玛格丽特·阿特伍德", "https://neodb.social/m/book/2021/09/16f228fb82-ed76-4a26-a6fd-ebc5d64c805f.jpg"},
	{"弹珠游戏", "埃莉萨·秀雅·迪萨潘", "https://neodb.social/m/item/doubanbook/2026/03/14/a5dad458-ab08-4fb8-b730-105cd582881c.jpg"},
	{"克拉拉与太阳", "石黑一雄", "https://neodb.social/m/book/2021/09/156cf6bc6e-4c54-43f8-92ed-6e7f9cce2161.jpg"},
	{"Normal People", "Sally Rooney", "https://neodb.social/m/book/2021/09/15100d2b42-a237-4b7c-8c1d-5d5df30f2c4d.jpg"},
	{"The Midnight Library", "Matt Haig", "https://neodb.social/m/book/2021/12/064789dbc1-0528-48ad-a850-cf7a4b8e1836.jpg"},
	{"Piranesi", "Susanna Clarke", "https://neodb.social/m/item/doubanbook/2026/04/19/4a4c7d11-65f7-42d7-a722-0a52d994f4df.jpg"},
	{"Circe", "Madeline Miller", "https://neodb.social/m/book/2021/11/01c592d233-a835-4d51-98a1-2ce05820ca1b.jpg"},
	{"柏青哥", "李敏金", "https://neodb.social/m/book/2021/09/174859f0d2-c130-4e22-909f-f32cac609f2b.jpg"},
	{"Educated", "Tara Westover", "https://neodb.social/m/book/2021/09/1681ef9db5-edf7-4f9a-841d-7e9f297b6b78.jpg"},
	{"人类简史", "尤瓦尔·赫拉利", "https://neodb.social/m/book/2021/09/1529d11838-57d9-47f3-9b63-989295154902.jpg"},
	{"Atomic Habits", "James Clear", "https://neodb.social/m/book/2021/09/16b54ff245-74bb-4307-be52-1eba93c7af23.jpg"},
	{"第十一站", "艾米丽·圣约翰·曼德尔", "https://neodb.social/m/book/2021/09/1808848136-d378-40ef-a2da-3fc375d6b025.jpg"},
	{"夜晚的潜水艇", "陈春成", "https://neodb.social/m/book/2021/09/15e5228d29-f93c-4c99-84cc-af506997f071.jpg"},
	{"流俗地", "黎紫书", "https://neodb.social/m/book/2021/09/169d241e6d-fa9d-4ab7-bdb3-f15ce7fe4fc4.jpg"},
	{"Tomorrow, and Tomorrow, and Tomorrow", "Gabrielle Zevin", "https://neodb.social/m/book/2022/06/04cf559ac0-2ef4-4e3a-9926-0a2a0e7a4e6b.jpeg"},
	{"长日将尽", "石黑一雄", "https://neodb.social/m/book/2021/09/1573d06b4f-d4f3-4203-a57d-402b6d26967d.jpg"},
	{"海边的卡夫卡", "村上春树", "https://neodb.social/m/book/2021/09/139b4ba767-2835-4381-929b-9d298a6d6cca.jpg"},
	{"局外人", "阿尔贝·加缪", "https://neodb.social/m/item/book/2026/02/11/b72ae794-7277-4244-a303-01b04580c004.jpeg"},
	{"月亮与六便士", "毛姆", "https://neodb.social/m/book/2021/09/1570f48ec2-2dba-47d2-a416-a465706879ee.jpg"},
	{"我与地坛", "史铁生", "https://neodb.social/m/book/2021/09/15d70558c0-2ef4-4e69-a4e9-57ddb348dfdf.jpg"},
	{"长安的荔枝", "马伯庸", "https://neodb.social/m/item/doubanbook/2026/01/16/12756c0c-6884-4c22-917f-d6411896029f.jpg"},
	{"Babel", "R. F. Kuang", "https://neodb.social/m/book/2022/09/09ea670f45-5b66-48ff-b0c9-f7684f212f51.jpg"},
	{"莫失莫忘", "石黑一雄", "https://neodb.social/m/book/2021/09/1718629eb5-a862-4de0-9c27-950420d91cbb.jpg"},
}

// welcomeShelf is welcomeBooks as books, for coverOf's typographic covers.
var welcomeShelf = func() []*calibre.Book {
	bs := make([]*calibre.Book, len(welcomeBooks))
	for i, b := range welcomeBooks {
		bs[i] = &calibre.Book{Title: b.title, Authors: []string{b.author}}
	}
	return bs
}()

// loadWelcomeCovers fetches the wall's covers once, from the cache on disk
// or else from NeoDB, a few at a time.
func (a *App) loadWelcomeCovers() {
	w := &a.welcome
	if w.coversStarted {
		return
	}
	w.coversStarted = true
	w.bitmaps = make([]*ui.Bitmap, len(welcomeBooks))
	if !w.coversNet {
		return
	}
	dir := ""
	if base, err := os.UserCacheDir(); err == nil {
		dir = filepath.Join(base, "Dalibre", "welcome")
	}
	a.run(func() {
		client := &http.Client{Timeout: 20 * time.Second}
		sem := make(chan struct{}, 6)
		var wg sync.WaitGroup
		for i, b := range welcomeBooks {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				data, err := welcomeCover(client, dir, b.cover)
				if err != nil {
					return
				}
				bmp, err := ui.DecodeBitmap(data)
				if err != nil {
					return
				}
				a.post(func() { w.bitmaps[i] = bmp })
			}()
		}
		wg.Wait()
	})
}

// welcomeCover returns the cover at url, kept in dir after the first time.
func welcomeCover(client *http.Client, dir, url string) ([]byte, error) {
	h := fnv.New64a()
	h.Write([]byte(url))
	path := ""
	if dir != "" {
		path = filepath.Join(dir, fmt.Sprintf("%016x%s", h.Sum64(), filepath.Ext(url)))
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Dalibre")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if path != "" && os.MkdirAll(dir, 0o755) == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
	return data, nil
}

// findCalibreLibrary looks for the library calibre last used, to offer it
// on the welcome screen. Its database is left alone until the user opens
// it: it may be on a network volume SQLite can't lock.
func (a *App) findCalibreLibrary() {
	p := calibreLibraryPath()
	if p == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(p, "metadata.db")); err != nil {
		return
	}
	a.welcome.found = p
}

// checkRecent looks, in the background, at which recent libraries' folders
// are gone.
func (a *App) checkRecent() {
	w := &a.welcome
	if w.checking || w.missing != nil {
		return
	}
	w.checking = true
	recent := append([]string(nil), a.settings.Recent...)
	a.run(func() {
		missing := map[string]bool{}
		for _, r := range recent {
			if _, err := os.Stat(filepath.Join(r, "metadata.db")); errors.Is(err, os.ErrNotExist) {
				missing[r] = true
			}
		}
		a.post(func() { w.missing, w.checking = missing, false })
	})
}

// forgetRecent takes root off the recent libraries.
func (a *App) forgetRecent(root string) {
	r := a.settings.Recent[:0]
	for _, p := range a.settings.Recent {
		if p != root {
			r = append(r, p)
		}
	}
	a.settings.Recent = r
	if a.settings.Library == root {
		a.settings.Library = ""
	}
	a.saveSettings()
	if a.loadErr != nil && a.loadErrDetail != nil && strings.Contains(a.loadErrDetail(), abbreviateHome(root)) {
		a.loadErr, a.loadErrDetail = nil, nil
	}
}

// askNotLibrary asks what to do with root, which holds no library.
func (a *App) askNotLibrary(root string) {
	n := 0
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				n++
			}
		}
	}
	a.welcome.notLib = notLibSheet{open: true, root: root, files: n}
}

// openCreateSheet shows the Create a new library sheet, in Documents.
func (a *App) openCreateSheet() {
	parent := ""
	if h, err := os.UserHomeDir(); err == nil {
		parent = h
		if st, err := os.Stat(filepath.Join(h, "Documents")); err == nil && st.IsDir() {
			parent = filepath.Join(h, "Documents")
		}
	}
	a.welcome.create = createSheet{open: true, name: locales.WelcomeDefaultName(), parent: parent}
}

// chooseCreateParent picks the folder a new library goes in.
func (a *App) chooseCreateParent() {
	a.run(func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: locales.WelcomeChooseWhereTitle(), Directory: true, CreateDirectories: true})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.post(func() { a.welcome.create.parent = dirs[0] })
	})
}

// abbreviateHome writes paths in the home folder with ~.
func abbreviateHome(p string) string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return p
	}
	if p == h {
		return "~"
	}
	if strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}

// expandHome reads a leading ~ as the home folder.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}
