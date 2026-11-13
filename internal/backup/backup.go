// Package backup creates independently restorable snapshots with per-file hashes.
package backup

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"projnote/internal/store"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Format    int     `json:"format"`
	CreatedAt string  `json:"created_at"`
	Files     []Entry `json:"files"`
}
type Info struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
	Protected bool   `json:"protected"`
}

// Create must run while the caller holds the exclusive application write lock.
func Create(root string, db *sql.DB) (Info, error) {
	dir := filepath.Join(root, "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Info{}, err
	}
	stage, err := os.MkdirTemp(root, ".snapshot-")
	if err != nil {
		return Info{}, err
	}
	defer os.RemoveAll(stage)
	snapshot := filepath.Join(stage, "app.db")
	if _, err = db.Exec("VACUUM INTO '" + strings.ReplaceAll(snapshot, "'", "''") + "'"); err != nil {
		return Info{}, err
	}
	sf, err := os.OpenFile(snapshot, os.O_RDWR, 0600)
	if err != nil {
		return Info{}, err
	}
	err = sf.Sync()
	_ = sf.Close()
	if err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp(dir, ".backup-*.tmp")
	if err != nil {
		return Info{}, err
	}
	tempName := tmp.Name()
	defer os.Remove(tempName)
	z := zip.NewWriter(tmp)
	manifest := Manifest{Format: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Files: []Entry{}}
	add := func(local, name string) error {
		f, err := os.Open(local)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("备份中不允许链接或特殊文件: %s", name)
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(0600)
		hdr.SetModTime(info.ModTime())
		w, err := z.CreateHeader(hdr)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(w, h), f)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, Entry{Path: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		return nil
	}
	if err = add(snapshot, "app.db"); err == nil {
		for _, folder := range []string{"attachments", "templates"} {
			base := filepath.Join(root, folder)
			if _, e := os.Stat(base); os.IsNotExist(e) {
				continue
			}
			err = filepath.WalkDir(base, func(local string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("备份中不允许符号链接")
				}
				if d.IsDir() {
					return nil
				}
				rel, e := filepath.Rel(root, local)
				if e != nil {
					return e
				}
				return add(local, filepath.ToSlash(rel))
			})
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		var w io.Writer
		w, err = z.Create("manifest.json")
		if err == nil {
			err = json.NewEncoder(w).Encode(manifest)
		}
	}
	if err != nil {
		_ = z.Close()
		_ = tmp.Close()
		return Info{}, err
	}
	if err = z.Close(); err == nil {
		err = tmp.Sync()
	}
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return Info{}, err
	}
	name := "backup-" + time.Now().UTC().Format("20060102-150405.000000000") + ".zip"
	if err = os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return Info{}, err
	}
	st, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return Info{}, err
	}
	return Info{Name: name, Size: st.Size(), CreatedAt: manifest.CreatedAt}, nil
}

func List(root string) ([]Info, error) {
	list := []Info{}
	entries, err := os.ReadDir(filepath.Join(root, "backups"))
	if os.IsNotExist(err) {
		return list, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "backup-") || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		st, err := e.Info()
		if err != nil {
			return nil, err
		}
		_, protectErr := os.Stat(filepath.Join(root, "backups", e.Name()+".protected"))
		list = append(list, Info{Name: e.Name(), Size: st.Size(), CreatedAt: st.ModTime().UTC().Format(time.RFC3339), Protected: protectErr == nil})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list, nil
}

func SafeName(name string) bool {
	return name == filepath.Base(name) && strings.HasPrefix(name, "backup-") && strings.HasSuffix(name, ".zip") && !strings.ContainsAny(name, "/\\")
}

// Extract verifies every hash before returning a private staging directory.
// The caller owns removing the directory. It never writes into the live data tree.
func Extract(archive, parent string) (string, Manifest, error) {
	var m Manifest
	z, err := zip.OpenReader(archive)
	if err != nil {
		return "", m, err
	}
	defer z.Close()
	if len(z.File) > 100000 {
		return "", m, fmt.Errorf("备份文件数量超限")
	}
	entries := map[string]*zip.File{}
	var total uint64
	for _, f := range z.File {
		name := f.Name
		if name != path.Clean(name) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, ":") || !f.Mode().IsRegular() {
			return "", m, fmt.Errorf("备份中包含不安全路径: %s", name)
		}
		if name != "manifest.json" && name != "app.db" && !strings.HasPrefix(name, "attachments/") && !strings.HasPrefix(name, "templates/") {
			return "", m, fmt.Errorf("备份中包含未知文件: %s", name)
		}
		if _, exists := entries[name]; exists {
			return "", m, fmt.Errorf("备份中有重复路径")
		}
		entries[name] = f
		if f.UncompressedSize64 > 60<<30 || total > 60<<30-f.UncompressedSize64 {
			return "", m, fmt.Errorf("备份解压容量超过 60 GB")
		}
		total += f.UncompressedSize64
	}
	mf, ok := entries["manifest.json"]
	if !ok || mf.UncompressedSize64 > 16<<20 {
		return "", m, fmt.Errorf("备份缺少有效清单")
	}
	r, err := mf.Open()
	if err != nil {
		return "", m, err
	}
	dec := json.NewDecoder(io.LimitReader(r, 16<<20))
	dec.DisallowUnknownFields()
	err = dec.Decode(&m)
	_ = r.Close()
	if err != nil || m.Format != 1 {
		return "", m, fmt.Errorf("不支持或损坏的备份清单")
	}
	if len(m.Files) != len(entries)-1 {
		return "", m, fmt.Errorf("清单与压缩包文件数量不一致")
	}
	stage, err := os.MkdirTemp(parent, ".restore-")
	if err != nil {
		return "", m, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(stage)
		}
	}()
	seen := map[string]bool{}
	for _, entry := range m.Files {
		f, ok := entries[entry.Path]
		if !ok || entry.Path == "manifest.json" || seen[entry.Path] || entry.Size < 0 || uint64(entry.Size) != f.UncompressedSize64 {
			return "", m, fmt.Errorf("清单文件不匹配: %s", entry.Path)
		}
		seen[entry.Path] = true
		dst := filepath.Join(stage, filepath.FromSlash(entry.Path))
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return "", m, err
		}
		in, err := f.Open()
		if err != nil {
			return "", m, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = in.Close()
			return "", m, err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, entry.Size+1))
		_ = in.Close()
		if err == nil {
			err = out.Sync()
		}
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			return "", m, err
		}
		if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return "", m, fmt.Errorf("文件校验失败: %s", entry.Path)
		}
	}
	if !seen["app.db"] {
		return "", m, fmt.Errorf("备份没有数据库")
	}
	db, err := sql.Open("sqlite", filepath.Join(stage, "app.db"))
	if err != nil {
		return "", m, err
	}
	var integrity string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if err == nil && integrity != "ok" {
		err = errors.New("数据库完整性校验失败")
	}
	if err == nil {
		var rows *sql.Rows
		rows, err = db.Query("PRAGMA foreign_key_check")
		if err == nil {
			if rows.Next() {
				err = errors.New("数据库外键校验失败")
			}
			if e := rows.Err(); err == nil {
				err = e
			}
			_ = rows.Close()
		}
	}
	if err == nil {
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('projects','tasks','records','issues','worklogs')").Scan(&count)
		if err == nil && count != 5 {
			err = errors.New("数据库不属于此版本 ProjNote")
		}
	}
	if err == nil {
		var version int
		err = db.QueryRow("PRAGMA user_version").Scan(&version)
		if err == nil && (version < 1 || version > store.SchemaVersion) {
			err = errors.New("备份数据库版本与当前程序不兼容")
		}
		if err == nil && version >= 3 {
			var count int
			err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('time_entries','reminders')").Scan(&count)
			if err == nil && count != 2 {
				err = errors.New("备份缺少此数据库版本必需的工时或提醒表")
			}
		}
		if err == nil && version >= 4 {
			var count int
			err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('team_daily_entries','project_daily_logs','task_imports')").Scan(&count)
			if err == nil && count != 3 {
				err = errors.New("备份缺少此数据库版本必需的项目日志或导入表")
			}
			if err == nil {
				var columns int
				err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('time_entries') WHERE name='assignee'").Scan(&columns)
				if err == nil && columns != 1 {
					err = errors.New("备份缺少此数据库版本必需的工时人员字段")
				}
			}

		}

		if err == nil && version >= 5 {
			var count int
			err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('desktop_drafts','export_jobs')").Scan(&count)
			if err == nil && count != 2 {
				err = errors.New("备份缺少此数据库版本必需的桌面草稿或导出任务表")
			}
		}
		if err == nil && version >= 6 {
			var count int
			err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('desktop_drafts') WHERE name='mode'").Scan(&count)
			if err == nil && count != 1 {
				err = errors.New("备份缺少此数据库版本必需的便签模式字段")
			}
		}

		if err == nil && version >= 7 {
			var count int
			err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('meeting_minutes') WHERE name IN ('id','code','project_id','title','meeting_date','location','attendees','content','created_at','updated_at','version','deleted_at')").Scan(&count)
			if err == nil && count != 12 {
				err = errors.New("备份缺少此数据库版本必需的会议纪要表或字段")
			}
		}

		if err == nil && version >= 8 {
			for _, spec := range []struct {
				table, fields string
				count         int
			}{
				{"plan_versions", "'id','project_id','number','name','description','created_by','created_at','snapshot','hash','task_count','version'", 11},
				{"requirements", "'id','project_id','code','title','description','acceptance_criteria','owner','source','status','revision','acceptance_note','change_note','created_at','updated_at','version','deleted_at'", 16},
				{"requirement_task_links", "'requirement_id','task_id'", 2},
				{"requirement_revisions", "'requirement_id','revision','created_by','created_at','change_note','snapshot','hash'", 7},
			} {
				var count int
				err = db.QueryRow("SELECT count(*) FROM pragma_table_info('" + spec.table + "') WHERE name IN (" + spec.fields + ")").Scan(&count)
				if err == nil && count != spec.count {
					err = errors.New("备份缺少此数据库版本必需的计划或需求字段：" + spec.table)
				}
				if err != nil {
					break
				}
			}
			if err == nil {
				var count int
				err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name IN ('plan_versions_immutable_update','plan_versions_immutable_delete','requirement_revisions_immutable_update','requirement_revisions_immutable_delete')").Scan(&count)
				if err == nil && count != 4 {
					err = errors.New("备份缺少计划或需求历史版本的只读保护")
				}
			}
			if err == nil {
				err = validateVersionSnapshots(db)
			}
		}

		if err == nil && version >= 9 {
			for _, spec := range []struct {
				table, fields string
				count         int
			}{
				{"lan_members", "'id','name','enabled','auth_version','created_at','updated_at','version'", 7},
				{"lan_member_projects", "'member_id','project_id'", 2},
				{"lan_member_tasks", "'task_id','member_id'", 2},
				{"lan_pairings", "'hash','member_id','auth_version','expires_at','created_at'", 5},
				{"lan_sessions", "'hash','member_id','auth_version','device','expires_at','created_at'", 6},
				{"lan_daily_entries", "'id','member_id','member_name','project_id','work_date','content','next_plan','blockers','minutes','created_at','updated_at','version'", 12},
			} {
				var count int
				err = db.QueryRow("SELECT count(*) FROM pragma_table_info('" + spec.table + "') WHERE name IN (" + spec.fields + ")").Scan(&count)
				if err == nil && count != spec.count {
					err = errors.New("备份缺少局域网协同必需字段：" + spec.table)
				}
				if err != nil {
					break
				}
			}
		}

		if err == nil && version >= 10 {
			for _, spec := range []struct {
				table, fields string
				count         int
			}{
				{"tasks", "'assignee_member_id'", 1},
				{"worklogs", "'title','variant'", 2},
				{"project_daily_logs", "'title','variant'", 2},
				{"task_reminders", "'id','task_id','due_at','notified_at','done_at','last_error','claim_until','created_at','updated_at','version','deleted_at'", 11},
			} {
				var count int
				err = db.QueryRow("SELECT count(*) FROM pragma_table_info('" + spec.table + "') WHERE name IN (" + spec.fields + ")").Scan(&count)
				if err == nil && count != spec.count {
					err = errors.New("备份缺少成员、日志或任务提醒必需字段：" + spec.table)
				}
				if err != nil {
					break
				}
			}
		}

	}
	cerr := db.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return "", m, err
	}
	success = true
	return stage, m, nil
}

// Install replaces the managed files while keeping the data directory and its
// instance lock in place. The DB must already be closed. It rolls back renames
// on failure; a separately created full backup remains the recovery point.
func Install(root, stage string) error {
	if err := CheckInterrupted(root); err != nil {
		return err
	}
	old, err := os.MkdirTemp(root, ".restore-old-")
	if err != nil {
		return err
	}
	names := []string{"app.db", "app.db-wal", "app.db-shm", "attachments", "templates"}
	movedOld := []string{}
	movedNew := []string{}
	rollback := func(cause error) error {
		var rollbackErrors []error
		for i := len(movedNew) - 1; i >= 0; i-- {
			name := movedNew[i]
			if e := os.Rename(filepath.Join(root, name), filepath.Join(stage, name)); e != nil {
				rollbackErrors = append(rollbackErrors, e)
			}
		}
		for i := len(movedOld) - 1; i >= 0; i-- {
			name := movedOld[i]
			if e := os.Rename(filepath.Join(old, name), filepath.Join(root, name)); e != nil {
				rollbackErrors = append(rollbackErrors, e)
			}
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("%w; 回滚未完成，原数据保留于 %s: %v", cause, old, rollbackErrors)
		}
		_ = os.RemoveAll(old)
		return cause
	}
	for _, name := range names {
		src := filepath.Join(root, name)
		if _, e := os.Lstat(src); os.IsNotExist(e) {
			continue
		}
		if err = os.Rename(src, filepath.Join(old, name)); err != nil {
			return rollback(err)
		}
		movedOld = append(movedOld, name)
	}
	for _, name := range []string{"app.db", "attachments", "templates"} {
		src := filepath.Join(stage, name)
		if _, e := os.Lstat(src); os.IsNotExist(e) {
			continue
		}
		if err = os.Rename(src, filepath.Join(root, name)); err != nil {
			return rollback(err)
		}
		movedNew = append(movedNew, name)
	}
	return os.RemoveAll(old)
}

// CheckInterrupted prevents an interrupted restore from silently starting an
// empty database when the original DB is still in the saved rename directory.
func CheckInterrupted(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restore-old-") {
			return fmt.Errorf("检测到未完成的恢复，已停止启动以保护原数据：%s。请保留整个目录，并从完整备份恢复到新的数据目录", filepath.Join(root, entry.Name()))
		}
	}
	return nil
}

// Version rows are independent saved content. Validate their frozen bytes before
// installing a backup, even if its outer ZIP manifest has been recomputed.
func validateVersionSnapshots(db *sql.DB) error {
	for _, table := range []string{"plan_versions", "requirement_revisions"} {
		rows, err := db.Query("SELECT snapshot,hash FROM " + table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var snapshot, expected string
			if err = rows.Scan(&snapshot, &expected); err != nil {
				break
			}
			sum := sha256.Sum256([]byte(snapshot))
			var object map[string]any
			if json.Unmarshal([]byte(snapshot), &object) != nil || object == nil || hex.EncodeToString(sum[:]) != expected {
				err = errors.New("备份中的计划或需求历史版本内容校验失败")
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	var missing int
	err := db.QueryRow("SELECT count(*) FROM requirements r WHERE NOT EXISTS(SELECT 1 FROM requirement_revisions v WHERE v.requirement_id=r.id AND v.revision=r.revision) OR r.revision!=(SELECT count(*) FROM requirement_revisions v WHERE v.requirement_id=r.id) OR r.revision!=(SELECT MAX(revision) FROM requirement_revisions v WHERE v.requirement_id=r.id)").Scan(&missing)
	if err == nil && missing > 0 {
		return errors.New("备份中的需求缺少完整历史版本")
	}
	return err
}
