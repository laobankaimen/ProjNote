package store

import (
	"database/sql"
	"strings"
	"unicode/utf8"
)

func migrateV10(tx *sql.Tx) error {
	if err := migrateProjectMembersV10(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`
ALTER TABLE worklogs ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE worklogs ADD COLUMN variant TEXT NOT NULL DEFAULT '';
ALTER TABLE project_daily_logs ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE project_daily_logs ADD COLUMN variant TEXT NOT NULL DEFAULT '';
DROP INDEX worklogs_period;
CREATE UNIQUE INDEX worklogs_period ON worklogs(COALESCE(project_id,''),period_type,log_date,variant) WHERE deleted_at='';
DROP INDEX project_daily_log_active;
CREATE UNIQUE INDEX project_daily_log_active ON project_daily_logs(project_id,log_date,variant) WHERE deleted_at='';
CREATE TABLE task_reminders (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
 due_at TEXT NOT NULL, notified_at TEXT NOT NULL DEFAULT '', done_at TEXT NOT NULL DEFAULT '',
 last_error TEXT NOT NULL DEFAULT '', claim_until TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1,
 deleted_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX task_reminder_active ON task_reminders(task_id) WHERE deleted_at='' AND done_at='';
CREATE INDEX task_reminder_due ON task_reminders(deleted_at,done_at,notified_at,due_at);
PRAGMA user_version=10;`); err != nil {
		return err
	}
	return nil
}

func logCreateInput(kind string, input map[string]any) (map[string]any, string, error) {
	if kind != "worklogs" && kind != "project_daily_logs" {
		return input, "", nil
	}
	in := copyMap(input)
	value, supplied := in["save_as_new"]
	delete(in, "save_as_new")
	if supplied {
		enabled, ok := value.(bool)
		if !ok {
			return nil, "", invalid("另存标识必须为布尔值")
		}
		if enabled {
			if !nonempty(in["title"]) {
				return nil, "", invalid("另存日志请填写新标题")
			}
			return in, uid(), nil
		}
	}
	return in, "", nil
}
func validateLogTitle(m map[string]any) error {
	title := strings.TrimSpace(text(m["title"]))
	if utf8.RuneCountInString(title) > 200 {
		return invalid("日志标题最多200字")
	}
	m["title"] = title
	return nil
}
