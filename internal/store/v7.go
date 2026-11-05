package store

import (
	"database/sql"
	"strings"
)

func migrateV7(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE meeting_minutes(
 id TEXT PRIMARY KEY,
 code TEXT NOT NULL UNIQUE,
 project_id TEXT REFERENCES projects(id) ON DELETE RESTRICT,
 title TEXT NOT NULL,
 meeting_date TEXT NOT NULL,
 location TEXT NOT NULL DEFAULT '',
 attendees TEXT NOT NULL DEFAULT '',
 content TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 deleted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX meeting_minutes_date ON meeting_minutes(deleted_at,meeting_date DESC,created_at DESC,id DESC);
CREATE INDEX meeting_minutes_project_date ON meeting_minutes(project_id,deleted_at,meeting_date DESC);
PRAGMA user_version=7;`)
	return err
}

func meetingProjectWritable(q queryer, id string) error {
	if id == "" {
		return nil
	}
	if err := projectWritable(q, id, ""); err != nil {
		return err
	}
	var status string
	if err := q.QueryRow("SELECT status FROM projects WHERE id=? AND deleted_at=''", id).Scan(&status); err != nil {
		return err
	}
	if status == "closed" {
		return fail("read_only", "已关闭项目的会议纪要只读", 409)
	}
	return nil
}

func validateMeeting(q queryer, m map[string]any) error {
	if err := meetingProjectWritable(q, text(m["project_id"])); err != nil {
		return err
	}
	for _, field := range []struct {
		key, label string
		maximum    int
		required   bool
	}{{"title", "会议标题", 500, true}, {"location", "会议地点", 500, false}, {"attendees", "参会人", 2000, false}, {"content", "会议正文", 200000, true}} {
		value, ok := m[field.key].(string)
		if !ok {
			return invalid(field.label + "必须为文本")
		}
		if field.required && strings.TrimSpace(value) == "" {
			return invalid(field.label + "不能为空")
		}
		if len([]rune(value)) > field.maximum {
			return invalid(field.label + "过长")
		}
	}
	if !nonempty(m["meeting_date"]) || !validDate(m["meeting_date"]) {
		return invalid("会议日期须为真实 YYYY-MM-DD 日期")
	}
	m["title"] = strings.TrimSpace(text(m["title"]))
	return nil
}
