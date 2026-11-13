package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

const taskReminderJoin = ` FROM task_reminders r JOIN tasks t ON t.id=r.task_id JOIN projects p ON p.id=t.project_id`
const taskReminderFields = `r.id,r.task_id,r.due_at,r.notified_at,r.done_at,r.last_error,r.created_at,r.updated_at,r.version,t.name AS task_name,t.status AS task_status,t.project_id,p.name AS project_name`
const taskReminderActionable = `t.deleted_at='' AND t.status IN('todo','doing') AND p.deleted_at='' AND p.status IN('active','paused') AND NOT EXISTS(SELECT 1 FROM tasks c WHERE c.parent_id=t.id AND c.deleted_at='')`

func taskReminderGet(q queryer, id string) (map[string]any, error) {
	rows, err := q.Query("SELECT "+taskReminderFields+taskReminderJoin+" WHERE r.id=? AND r.deleted_at=''", id)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fail("not_found", "任务提醒不存在", 404)
	}
	return items[0], nil
}
func (s *Store) GetTaskReminder(id string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return taskReminderGet(s.DB, id)
}
func (s *Store) ListTaskReminders(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return taskReminderList(s.DB, filters)
}
func taskReminderList(q queryer, filters map[string]string) (map[string]any, error) {
	where := " WHERE r.deleted_at=''"
	args := []any{}
	switch filters["status"] {
	case "", "pending":
		where += " AND r.done_at='' AND " + taskReminderActionable
	case "done":
		where += " AND (r.done_at!='' OR NOT (" + taskReminderActionable + "))"
	case "all":
	default:
		return nil, invalid("提醒状态须为pending/done/all")
	}
	if id := filters["project_id"]; id != "" {
		where += " AND t.project_id=?"
		args = append(args, id)
	}
	if due := filters["due_to"]; due != "" {
		at, err := time.Parse(time.RFC3339Nano, due)
		if err != nil {
			return nil, invalid("提醒截止时间无效")
		}
		where += " AND julianday(r.due_at)<julianday(?)"
		args = append(args, at.UTC().Format(time.RFC3339Nano))
	}
	var total int
	if err := q.QueryRow("SELECT count(*)"+taskReminderJoin+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limit := pageLimit(filters, 30)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := q.Query("SELECT "+taskReminderFields+taskReminderJoin+where+" ORDER BY julianday(r.due_at),r.id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, nil
}
func reminderInput(input map[string]any, creating bool) (string, string, error) {
	for k := range input {
		if k != "due_at" && !(creating && k == "task_id") && !(!creating && k == "version") {
			return "", "", invalid("未知或不可修改字段: " + k)
		}
	}
	due, err := time.Parse(time.RFC3339Nano, text(input["due_at"]))
	if err != nil {
		return "", "", invalid("请选择有效提醒时间")
	}
	if due.Year() < 2000 || due.Year() > 2200 {
		return "", "", invalid("提醒时间超出支持范围")
	}
	return text(input["task_id"]), due.UTC().Format(time.RFC3339Nano), nil
}
func taskCanRemind(q queryer, id string) error {
	var count int
	if err := q.QueryRow("SELECT count(*) FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=? AND "+taskReminderActionable, id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fail("read_only", "请选择有效项目中未完成的叶子任务", 409)
	}
	return nil
}
func (s *Store) CreateTaskReminder(input map[string]any) (map[string]any, error) {
	id, due, err := reminderInput(input, true)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, invalid("请选择待完成任务")
	}
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if err := taskCanRemind(tx, id); err != nil {
			return nil, err
		}
		rid, at := uid(), now()
		if _, err := tx.Exec("INSERT INTO task_reminders(id,task_id,due_at,created_at,updated_at)VALUES(?,?,?,?,?)", rid, id, due, at, at); err != nil {
			return nil, failReminderDuplicate(err)
		}
		after, err := taskReminderGet(tx, rid)
		if err == nil {
			err = audit(tx, "task_reminders", rid, "create", nil, after)
		}
		return after, err
	})
}
func failReminderDuplicate(err error) error {
	if ae, ok := dbErr(err).(*AppError); ok && ae.Status == 409 {
		return fail("conflict", "该任务已有提醒，请调整原提醒时间", 409)
	}
	return err
}
func (s *Store) UpdateTaskReminder(id string, input map[string]any) (map[string]any, error) {
	_, due, err := reminderInput(input, false)
	if err != nil {
		return nil, err
	}
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		before, err := taskReminderGet(tx, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if nonempty(before["done_at"]) {
			return nil, fail("read_only", "已结束提醒只读，请重新创建", 409)
		}
		if err = taskCanRemind(tx, text(before["task_id"])); err != nil {
			return nil, err
		}
		_, err = tx.Exec("UPDATE task_reminders SET due_at=?,notified_at='',last_error='',claim_until='',updated_at=?,version=version+1 WHERE id=?", due, now(), id)
		if err != nil {
			return nil, err
		}
		after, err := taskReminderGet(tx, id)
		if err == nil {
			err = audit(tx, "task_reminders", id, "update", before, after)
		}
		return after, err
	})
}
func (s *Store) DismissTaskReminder(id string, input map[string]any) (map[string]any, error) {
	for k := range input {
		if k != "version" {
			return nil, invalid("未知字段: " + k)
		}
	}
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		before, err := taskReminderGet(tx, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if nonempty(before["done_at"]) {
			return before, nil
		}
		at := now()
		if _, err = tx.Exec("UPDATE task_reminders SET done_at=?,claim_until='',updated_at=?,version=version+1 WHERE id=?", at, at, id); err != nil {
			return nil, err
		}
		after, err := taskReminderGet(tx, id)
		if err == nil {
			err = audit(tx, "task_reminders", id, "dismiss", before, after)
		}
		return after, err
	})
}

// Claim one notification at a time. A lease lets a restarted service retry interrupted delivery.
func (s *Store) ClaimTaskReminder(at time.Time) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		rows, err := tx.Query("SELECT "+taskReminderFields+taskReminderJoin+" WHERE r.deleted_at='' AND r.done_at='' AND r.notified_at='' AND julianday(r.due_at)<=julianday(?) AND (r.claim_until='' OR julianday(r.claim_until)<=julianday(?)) AND "+taskReminderActionable+" ORDER BY julianday(r.due_at),r.id LIMIT 1", at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
		items, err := rawRows(rows)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, nil
		}
		m := items[0]
		lease := at.UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
		_, err = tx.Exec("UPDATE task_reminders SET claim_until=? WHERE id=?", lease, m["id"])
		m["claim_until"] = lease
		return m, err
	})
}
func (s *Store) FinishTaskReminder(claim map[string]any, at time.Time, deliveryErr error) error {
	_, err := s.write(func(tx *sql.Tx) (map[string]any, error) {
		notified, retry, message := "", "", ""
		if deliveryErr == nil {
			notified = at.UTC().Format(time.RFC3339Nano)
		} else {
			retry = at.UTC().Add(time.Minute).Format(time.RFC3339Nano)
			message = fmt.Sprint(deliveryErr)
			if len(message) > 1000 {
				message = message[:1000]
			}
		}
		_, err := tx.Exec("UPDATE task_reminders SET notified_at=?,claim_until=?,last_error=? WHERE id=? AND version=? AND claim_until=? AND done_at='' AND deleted_at=''", notified, retry, message, claim["id"], claim["version"], claim["claim_until"])
		return nil, err
	})
	return err
}

// End a reminder in the same transaction when its task becomes terminal or a
// summary. Reopening a task must never revive an old reminder or delivery lease.
func endIneligibleTaskReminders(tx *sql.Tx, projectID, actor string) error {
	var exists int
	if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='task_reminders'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	} // Historical migration fixtures precede this feature.
	rows, err := tx.Query("SELECT r.id"+taskReminderJoin+" WHERE t.project_id=? AND r.deleted_at='' AND r.done_at='' AND (t.deleted_at!='' OR t.status IN('done','canceled') OR EXISTS(SELECT 1 FROM tasks c WHERE c.parent_id=t.id AND c.deleted_at=''))", projectID)
	if err != nil {
		return err
	}
	items, err := rawRows(rows)
	if err != nil {
		return err
	}
	for _, item := range items {
		id := text(item["id"])
		before, err := taskReminderGet(tx, id)
		if err != nil {
			return err
		}
		at := now()
		if _, err = tx.Exec("UPDATE task_reminders SET done_at=?,claim_until='',updated_at=?,version=version+1 WHERE id=?", at, at, id); err != nil {
			return err
		}
		after, err := taskReminderGet(tx, id)
		if err != nil {
			return err
		}
		if err = auditAs(tx, actor, "task_reminders", id, "task_ended", before, after); err != nil {
			return err
		}
	}
	return nil
}
