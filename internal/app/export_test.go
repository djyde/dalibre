package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestExportToFolder exports a book from its menu through the dialog,
// and a second time numbers the copy instead of replacing the first.
func TestExportToFolder(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	dir := t.TempDir()
	for round, want := range []string{"Dune - Frank Herbert.epub", "Dune - Frank Herbert (2).epub"} {
		if err := tt.RightClick("Dune"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Export to folder…"); err != nil {
			t.Fatalf("menu %q: %v", tt.Menu(), err)
		}
		tt.Frame()
		if !a.ex.open {
			t.Fatal("dialog not open")
		}
		a.ex.folder = dir
		tt.Frame()
		for _, s := range []string{"Export 1 book", "EPUB", "Keep both, numbered", "Herbert, Frank/Dune/" + want} {
			if !tt.HasText(s) {
				t.Errorf("round %d: dialog misses %q; texts %q", round, s, tt.Texts())
			}
		}
		if round == 1 {
			if !tt.HasText("Already there") {
				t.Errorf("collision not marked; texts %q", tt.Texts())
			}
			shot(t, tt, "export-dialog")
		}
		if err := tt.Click("Export 1 file"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.ex.open || a.busy != "" {
			t.Fatalf("after export: open %v, busy %q", a.ex.open, a.busy)
		}
		if _, err := os.Stat(filepath.Join(dir, "Herbert, Frank", "Dune", want)); err != nil {
			t.Errorf("round %d: %v", round, err)
		}
	}
	if a.settings.Export.Folder != dir {
		t.Errorf("folder not remembered: %q", a.settings.Export.Folder)
	}
}
