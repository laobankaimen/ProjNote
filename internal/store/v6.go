package store

import (
	"database/sql"
	"time"
)

// Every DDL statement runs in the same migration transaction. Rebuilding the
// CHECK-constrained tables leaves the audit/search tables and frozen JSON alone.
func migrateV6(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE desktop_drafts ADD COLUMN mode TEXT NOT NULL DEFAULT 'record' CHECK(mode IN('task','record'));
CREATE TABLE worklogs_v6(id TEXT PRIMARY KEY,project_id TEXT REFERENCES projects(id) ON DELETE RESTRICT,period_type TEXT NOT NULL CHECK(period_type IN('day','week','month')),log_date TEXT NOT NULL,content TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '');
INSERT INTO worklogs_v6(id,project_id,period_type,log_date,content,created_at,updated_at,version,deleted_at) SELECT id,project_id,period_type,log_date,content,created_at,updated_at,version,deleted_at FROM worklogs;
DROP TABLE worklogs;
ALTER TABLE worklogs_v6 RENAME TO worklogs;
CREATE UNIQUE INDEX worklogs_period ON worklogs(COALESCE(project_id,''),period_type,log_date) WHERE deleted_at='';
CREATE INDEX worklogs_active ON worklogs(deleted_at);
CREATE TABLE export_jobs_v6(id TEXT PRIMARY KEY,state TEXT NOT NULL CHECK(state IN('queued','running','succeeded','failed')),format TEXT NOT NULL CHECK(format IN('docx','md','json','xlsx','html')),source_kind TEXT NOT NULL,source_id TEXT NOT NULL,source_version INTEGER NOT NULL CHECK(source_version>=1),template_id TEXT NOT NULL DEFAULT '',template_hash TEXT NOT NULL DEFAULT '',source_snapshot TEXT NOT NULL CHECK(json_valid(source_snapshot)),output_name TEXT NOT NULL DEFAULT '',output_sha256 TEXT NOT NULL DEFAULT '',size INTEGER NOT NULL DEFAULT 0 CHECK(size>=0),error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
INSERT INTO export_jobs_v6(id,state,format,source_kind,source_id,source_version,template_id,template_hash,source_snapshot,output_name,output_sha256,size,error,created_at,updated_at,version) SELECT id,state,format,source_kind,source_id,source_version,template_id,template_hash,source_snapshot,output_name,output_sha256,size,error,created_at,updated_at,version FROM export_jobs;
DROP TABLE export_jobs;
ALTER TABLE export_jobs_v6 RENAME TO export_jobs;
CREATE INDEX export_jobs_created ON export_jobs(created_at,id);
CREATE INDEX export_jobs_state ON export_jobs(state);
INSERT OR IGNORE INTO settings(key,value)VALUES('desktop_default','1');
PRAGMA user_version=6;`)
	return err
}

// Use local calendar boundaries before converting timestamp filters to UTC.
// AddDate handles month lengths, leap years, year changes and local DST.
func logPeriodBounds(date, period string) (time.Time, time.Time, error) {
	if !oneOf(period, "day", "week", "month") {
		return time.Time{}, time.Time{}, invalid("日志周期无效")
	}
	start, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil || start.Format("2006-01-02") != date {
		return time.Time{}, time.Time{}, invalid("日期格式应为 YYYY-MM-DD")
	}
	switch period {
	case "week":
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		return start, start.AddDate(0, 0, 7), nil
	case "month":
		start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		return start, start.AddDate(0, 1, 0), nil
	default:
		return start, start.AddDate(0, 0, 1), nil
	}
}
