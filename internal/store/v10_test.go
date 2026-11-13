package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestV10LateMigrationFailureRollsBackRosterLogsAndSchemaThenRetries(t *testing.T) {
	root := t.TempDir()
	db := legacyV8(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV9(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	old := &Store{DB: db}
	p := mustCreate(t, old, "projects", map[string]any{"name": "末尾失败不丢旧库"})
	log := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "log_date": "2026-11-13", "content": "升级前人工正文"})
	var originalTail string
	if err = db.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&originalTail); err != nil {
		t.Fatal(err)
	}
	originalCount := sqlCount(t, old, "audit")
	if _, err = db.Exec("CREATE TABLE task_reminders(collision TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if partial, err := Open(root); err == nil {
		partial.Close()
		t.Fatal("schema10 accepted a late table collision")
	}
	db, err = sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var schema, addedColumns, auditCount int
	if err = db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tasks", "worklogs", "project_daily_logs"} {
		var count int
		if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('" + table + "') WHERE name IN('assignee_member_id','title','variant')").Scan(&count); err != nil {
			t.Fatal(err)
		}
		addedColumns += count
	}
	var tail, content, personalIndex, projectIndex string
	if err = db.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&tail); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM audit").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT content FROM worklogs WHERE id=?", log["id"]).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='index' AND name='worklogs_period'").Scan(&personalIndex); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='index' AND name='project_daily_log_active'").Scan(&projectIndex); err != nil {
		t.Fatal(err)
	}
	if schema != 9 || addedColumns != 0 || tail != originalTail || auditCount != originalCount || content != log["content"] {
		t.Fatalf("partial schema10 migration escaped rollback: schema=%d columns=%d audit=%d/%d tail=%s/%s content=%s", schema, addedColumns, auditCount, originalCount, tail, originalTail, content)
	}
	var variantIndexes int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN('worklogs_period','project_daily_log_active') AND instr(sql,'variant')>0").Scan(&variantIndexes); err != nil || variantIndexes != 0 {
		t.Fatal("old unique indexes were not restored", variantIndexes, err, personalIndex, projectIndex)
	}
	if _, err = db.Exec("DROP TABLE task_reminders"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(root)
	if err != nil {
		t.Fatal("retry after removing the collision failed", err)
	}
	defer upgraded.Close()
	if err = upgraded.DB.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != SchemaVersion {
		t.Fatal(schema, err)
	}
	if got := getNow(t, upgraded, "worklogs", text(log["id"])); got["content"] != log["content"] || got["title"] != "" || got["variant"] != "" {
		t.Fatal("retry changed existing log", got)
	}
	if err = upgraded.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&tail); err != nil || tail != originalTail || sqlCount(t, upgraded, "audit") != originalCount {
		t.Fatal("retry rewrote audit", tail, err)
	}
	assertChain(t, upgraded)
}
