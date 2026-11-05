package backup

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"projnote/internal/store"
)

func TestRetentionRequiresVerifiedNewBackupAndKeepsRecoveryPoints(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	settings := map[string]any{"backup_keep_count": "2", "backup_max_mb": "0", "backup_auto": "1"}
	m := &Manager{Root: root, DB: st.DB, Gate: &st.Mu, Settings: func() (map[string]any, error) { return settings, nil }}
	protected, err := m.MigrationBackup("restore-rollback")
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, err = m.Create("test", false); err != nil {
			t.Fatal(err)
		}
	}
	items, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected protected+2 normal: %v", items)
	}
	if _, err = os.Stat(filepath.Join(root, "backups", protected.Name)); err != nil {
		t.Fatal("protected rollback backup deleted")
	}
	health := m.Health()
	if health.LastSuccess == "" || health.LastError != "" || health.InProgress {
		t.Fatalf("invalid health %v", health)
	}
	latest := items[0].Name
	if err = os.WriteFile(filepath.Join(root, "backups", latest), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	settings["backup_keep_count"] = "1" // policy clamps invalid count to safe default
	if _, err = m.Prune(); err == nil {
		t.Fatal("pruned after corrupt latest archive")
	}
	after, _ := List(root)
	if len(after) != 3 {
		t.Fatal("failed validation removed archives")
	}
}
func TestBackupFailureHealthPersists(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	settings := func() (map[string]any, error) { return map[string]any{}, nil }
	m := &Manager{Root: root, DB: st.DB, Gate: &st.Mu, Settings: settings, Validate: func(string) error { return os.ErrInvalid }}
	if _, err = m.Create("test", false); err == nil {
		t.Fatal("validation unexpectedly succeeded")
	}
	if m.Health().LastError == "" {
		t.Fatal("backup failure not surfaced")
	}
	other := &Manager{Root: root, DB: st.DB, Gate: &sync.RWMutex{}, Settings: settings}
	if other.Health().LastError == "" {
		t.Fatal("failure lost after restart")
	}
	items, _ := List(root)
	if len(items) != 1 {
		t.Fatal("failed validation should leave archive for diagnosis")
	}
}
func TestRelocationRejectsNonemptyAndNestedTargets(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{root, filepath.Join(root, "child"), filepath.Dir(root), "relative"} {
		if _, err := RelocationTarget(root, target); err == nil {
			t.Fatalf("accepted invalid destination %s", target)
		}
	}
	parent := t.TempDir()
	dest := filepath.Join(parent, "new")
	got, err := RelocationTarget(root, dest)
	if err != nil || got != dest {
		t.Fatalf("valid new directory rejected: %s %v", got, err)
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dest, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RelocationTarget(root, dest); err == nil {
		t.Fatal("accepted nonempty destination")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(root, alias); err == nil {
		if _, err = RelocationTarget(root, filepath.Join(alias, "child")); err == nil {
			t.Fatal("accepted symlink destination into source")
		}
	}
}

func TestAutomaticBackupReplacesMissingSuccessfulArchive(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := &Manager{Root: root, DB: st.DB, Gate: &st.Mu, Settings: func() (map[string]any, error) { return map[string]any{"backup_auto": "1"}, nil }}
	if err = m.RunDaily(); err != nil {
		t.Fatal(err)
	}
	first := m.Health().ArchiveName
	if err = os.Remove(filepath.Join(root, "backups", first)); err != nil {
		t.Fatal(err)
	}
	if err = m.RunDaily(); err != nil {
		t.Fatal(err)
	}
	second := m.Health().ArchiveName
	if second == first || second == "" {
		t.Fatal("missing archive was still treated as success")
	}
	if _, err = os.Stat(filepath.Join(root, "backups", second)); err != nil {
		t.Fatal(err)
	}
}
