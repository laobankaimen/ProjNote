package backup

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"projnote/internal/store"
)

func TestCompleteSnapshotRestoreIsIndependent(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.Create("projects", map[string]any{"name": "现场项目"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "attachments", "one"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "attachments", "one", "original"), []byte("original evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Update("projects", p["id"].(string), map[string]any{"name": "修改后", "version": p["version"]}); err != nil {
		t.Fatal(err)
	}
	second, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "backups", first.Name)); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	stage, m, err := Extract(filepath.Join(root, "backups", second.Name), parent)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if len(m.Files) != 2 {
		t.Fatalf("expected DB and attachment, got %d", len(m.Files))
	}
	restoredRoot := t.TempDir()
	if err = Install(restoredRoot, stage); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(restoredRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Get("projects", p["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "修改后" {
		t.Fatalf("restored wrong project: %v", got)
	}
	b, err := os.ReadFile(filepath.Join(restoredRoot, "attachments", "one", "original"))
	if err != nil || string(b) != "original evidence" {
		t.Fatalf("attachment not restored: %q %v", b, err)
	}
}
func TestRejectUnsafeOrUnlistedArchives(t *testing.T) {
	for _, name := range []string{"../escape", "attachments/../../escape", "attachments\\escape", "/absolute", "templates/safe"} {
		t.Run(name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "bad.zip")
			f, err := os.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			z := zip.NewWriter(f)
			w, _ := z.Create(name)
			_, _ = w.Write([]byte("bad"))
			w, _ = z.Create("manifest.json")
			_ = json.NewEncoder(w).Encode(Manifest{Format: 1})
			_ = z.Close()
			_ = f.Close()
			if stage, _, err := Extract(filename, t.TempDir()); err == nil {
				os.RemoveAll(stage)
				t.Fatal("accepted unsafe/unlisted archive")
			}
		})
	}
}
func TestRejectCorruptFileBeforeTouchingLiveData(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	src, err := zip.OpenReader(filepath.Join(root, "backups", info.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	filename := filepath.Join(t.TempDir(), "corrupt.zip")
	f, _ := os.Create(filename)
	z := zip.NewWriter(f)
	for _, entry := range src.File {
		w, _ := z.Create(entry.Name)
		if entry.Name == "manifest.json" {
			r, _ := entry.Open()
			var m Manifest
			_ = json.NewDecoder(r).Decode(&m)
			_ = r.Close()
			m.Files[0].SHA256 = "wrong"
			_ = json.NewEncoder(w).Encode(m)
		} else {
			r, _ := entry.Open()
			_, _ = w.Write(mustRead(t, r))
			_ = r.Close()
		}
	}
	_ = z.Close()
	_ = f.Close()
	if stage, _, err := Extract(filename, t.TempDir()); err == nil {
		os.RemoveAll(stage)
		t.Fatal("accepted checksum mismatch")
	}
	if _, err = os.Stat(filepath.Join(root, "app.db")); err != nil {
		t.Fatal("live DB was touched")
	}
}

func mustRead(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInterruptedRestoreBlocksOpeningEmptyDatabase(t *testing.T) {
	root := t.TempDir()
	saved := filepath.Join(root, ".restore-old-interrupted")
	if err := os.Mkdir(saved, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saved, "app.db"), []byte("original DB preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckInterrupted(root); err == nil {
		t.Fatal("interrupted restore was ignored")
	}
	if _, err := os.Stat(filepath.Join(root, "app.db")); !os.IsNotExist(err) {
		t.Fatal("live DB unexpectedly created")
	}
	if err := Install(root, t.TempDir()); err == nil {
		t.Fatal("new restore overwrote interrupted state")
	}
}

func TestSchemaThreeSnapshotMustIncludeNewDomainTables(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	// A valid ZIP and SQLite integrity check do not prove that a database claiming
	// schema 3 actually includes the migration's required tables.
	if _, err = st.DB.Exec("DROP TABLE time_entries; DROP TABLE reminders;"); err != nil {
		t.Fatal(err)
	}
	info, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if stage, _, err := Extract(filepath.Join(root, "backups", info.Name), t.TempDir()); err == nil {
		os.RemoveAll(stage)
		t.Fatal("accepted incomplete schema3 snapshot")
	}
}
