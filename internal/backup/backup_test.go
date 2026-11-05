package backup

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestSchemaFourSnapshotRequiresDomainTablesAndAssignee(t *testing.T) {
	for _, corrupt := range []string{"DROP TABLE task_imports", "DROP INDEX time_entries_person_day; ALTER TABLE time_entries DROP COLUMN assignee"} {
		t.Run(corrupt, func(t *testing.T) {
			root := t.TempDir()
			st, err := store.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err = st.DB.Exec(corrupt); err != nil {
				t.Fatal(err)
			}
			info, err := Create(root, st.DB)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = Extract(filepath.Join(root, "backups", info.Name), t.TempDir()); err == nil {
				t.Fatal("accepted incomplete schema4 backup")
			}
		})
	}
}

func TestSchemaFiveSnapshotRequiresScratchAndExportTables(t *testing.T) {
	for _, table := range []string{"desktop_drafts", "export_jobs"} {
		t.Run(table, func(t *testing.T) {
			root := t.TempDir()
			st, e := store.Open(root)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			if _, e = st.DB.Exec("DROP TABLE " + table); e != nil {
				t.Fatal(e)
			}
			snapshot, e := Create(root, st.DB)
			if e != nil {
				t.Fatal(e)
			}
			if stage, _, e := Extract(filepath.Join(root, "backups", snapshot.Name), t.TempDir()); e == nil {
				os.RemoveAll(stage)
				t.Fatal("incomplete schema5 backup accepted")
			}
		})
	}
}

func TestSchemaSixSnapshotRequiresDesktopMode(t *testing.T) {
	root := t.TempDir()
	st, e := store.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if _, e = st.DB.Exec("ALTER TABLE desktop_drafts DROP COLUMN mode"); e != nil {
		t.Fatal(e)
	}
	snapshot, e := Create(root, st.DB)
	if e != nil {
		t.Fatal(e)
	}
	if stage, _, e := Extract(filepath.Join(root, "backups", snapshot.Name), t.TempDir()); e == nil {
		os.RemoveAll(stage)
		t.Fatal("schema6 backup missing draft mode accepted")
	}
}

func TestSchemaSevenSnapshotRequiresCompleteMeetingTable(t *testing.T) {
	for _, corruption := range []string{"DROP TABLE meeting_minutes", "ALTER TABLE meeting_minutes DROP COLUMN location"} {
		t.Run(corruption, func(t *testing.T) {
			root := t.TempDir()
			st, err := store.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err = st.DB.Exec(corruption); err != nil {
				t.Fatal(err)
			}
			snapshot, err := Create(root, st.DB)
			if err != nil {
				t.Fatal(err)
			}
			if stage, _, err := Extract(filepath.Join(root, "backups", snapshot.Name), t.TempDir()); err == nil {
				os.RemoveAll(stage)
				t.Fatal("incomplete schema7 meeting backup accepted")
			}
		})
	}
}

func TestSchemaEightRequiresVersionTablesColumnsAndImmutableProtection(t *testing.T) {
	for _, corruption := range []string{"DROP TABLE plan_versions", "DROP TABLE requirement_task_links", "ALTER TABLE requirement_revisions DROP COLUMN hash", "DROP TRIGGER plan_versions_immutable_update"} {
		t.Run(corruption, func(t *testing.T) {
			root := t.TempDir()
			st, err := store.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err = st.DB.Exec(corruption); err != nil {
				t.Fatal(err)
			}
			snapshot, err := Create(root, st.DB)
			if err != nil {
				t.Fatal(err)
			}
			if stage, _, err := Extract(filepath.Join(root, "backups", snapshot.Name), t.TempDir()); err == nil {
				os.RemoveAll(stage)
				t.Fatal("incomplete schema8 backup accepted")
			}
		})
	}
}

func TestSchemaEightRejectsCorruptedVersionSnapshotDespiteValidOuterManifest(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.Create("projects", map[string]any{"name": "历史版本项目"})
	if err != nil {
		t.Fatal(err)
	}
	version, err := st.CreatePlanVersion(p["id"].(string), map[string]any{"project_version": p["version"], "name": "独立完整计划"})
	if err != nil {
		t.Fatal(err)
	}
	var protection string
	if err = st.DB.QueryRow("SELECT sql FROM sqlite_master WHERE type='trigger' AND name='plan_versions_immutable_update'").Scan(&protection); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("DROP TRIGGER plan_versions_immutable_update"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("UPDATE plan_versions SET hash=? WHERE id=?", strings.Repeat("0", 64), version["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(protection); err != nil {
		t.Fatal(err)
	}
	archive, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if stage, _, err := Extract(filepath.Join(root, "backups", archive.Name), t.TempDir()); err == nil {
		os.RemoveAll(stage)
		t.Fatal("corrupted saved plan accepted after outer hash recomputation")
	}
}

func TestSchemaEightRejectsRevisionGapEvenWhenCountMatchesCurrent(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.Create("projects", map[string]any{"name": "历史连续性项目"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := st.Create("requirements", map[string]any{"project_id": p["id"], "title": "版本连续性", "description": "保留所有历史", "acceptance_criteria": "修订从一开始连续"})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"第二版", "第三版"} {
		req, err = st.Update("requirements", req["id"].(string), map[string]any{"version": req["version"], "title": title, "change_note": "修订测试"})
		if err != nil {
			t.Fatal(err)
		}
	}
	var protection string
	if err = st.DB.QueryRow("SELECT sql FROM sqlite_master WHERE name='requirement_revisions_immutable_update'").Scan(&protection); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("DROP TRIGGER requirement_revisions_immutable_update"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("UPDATE requirement_revisions SET revision=4 WHERE requirement_id=? AND revision=2", req["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec(protection); err != nil {
		t.Fatal(err)
	}
	// The old check accepted {1,3,4}: three rows and the current revision 3 exist.
	archive, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if stage, _, err := Extract(filepath.Join(root, "backups", archive.Name), t.TempDir()); err == nil {
		os.RemoveAll(stage)
		t.Fatal("non-contiguous requirement revisions accepted")
	}
}
