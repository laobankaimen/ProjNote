package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"projnote/internal/backup"
	"projnote/internal/store"
)

func TestRelocationKeepsSourceAndDatasetIdentity(t *testing.T) {
	source := t.TempDir()
	st, err := store.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.Create("projects", map[string]any{"name": "迁移项目"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := st.Settings()
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	target := filepath.Join(t.TempDir(), "目标数据")
	if err = relocateOffline(source, target); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{source, target} {
		db, e := store.Open(root)
		if e != nil {
			t.Fatal(e)
		}
		p, e := db.Get("projects", project["id"].(string))
		if e != nil || p["name"] != "迁移项目" {
			t.Fatalf("missing project at %s: %v %v", root, p, e)
		}
		s, e := db.Settings()
		if e != nil || s["dataset_id"] != settings["dataset_id"] {
			t.Fatal("dataset changed across migration")
		}
		_ = db.Close()
	}
	list, err := backup.List(target)
	if err != nil || len(list) == 0 || !list[0].Protected {
		t.Fatal("migration backup not protected")
	}
	if err = relocateOffline(source, target); err == nil {
		t.Fatal("overwrote nonempty target")
	}
}
func TestUpgradeCreatesProtectedVersionOneSnapshot(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the v1 database from the preserved migration fixture in SQL.
	_ = st.Close()
	if err = os.Remove(filepath.Join(root, "app.db")); err != nil {
		t.Fatal(err)
	}
	// A minimal v1 structure is sufficient to prove snapshot precedes attempted
	// migration: an invalid migration must fail without touching the old DB.
	db, err := openLegacyFixture(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	upgraded, err := openWithBackup(root)
	if err == nil {
		upgraded.Close()
		t.Fatal("invalid legacy fixture should fail migration")
	}
	list, e := backup.List(root)
	if e != nil || len(list) != 1 || !list[0].Protected {
		t.Fatalf("missing protected pre-upgrade snapshot: %v %v", list, e)
	}
}

func openLegacyFixture(root string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("CREATE TABLE projects(id TEXT PRIMARY KEY); PRAGMA user_version=1;")
	return db, err
}

func TestRestoreClearsBackedUpLANSessionsAndPairingCodes(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateLANMember(map[string]any{"name": "恢复验证成员"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := st.CreateLANPairing(member["id"].(string), map[string]any{"version": member["version"]})
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.ExchangeLANPairing(pair["code"].(string), "restore-fixture")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := st.CreateLANPairing(member["id"].(string), map[string]any{"version": member["version"]})
	if err != nil {
		t.Fatal(err)
	}
	point, err := backup.Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	if err = restoreOffline(root, filepath.Join(root, "backups", point.Name)); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.LANSession(session["token"].(string)); err == nil {
		t.Fatal("restore revived an old LAN session")
	}
	if _, err = restored.ExchangeLANPairing(pending["code"].(string), "restored-client"); err == nil {
		t.Fatal("restore revived an unconsumed pairing code")
	}
	members, err := restored.ListLANMembers(nil)
	if err != nil || members["total"] != 1 {
		t.Fatal("restore removed the member identity", members, err)
	}
	audit, err := restored.VerifyAudit()
	if err != nil || audit["ok"] != true {
		t.Fatal(audit, err)
	}
}
