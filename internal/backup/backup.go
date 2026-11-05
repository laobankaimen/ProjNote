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
		list = append(list, Info{Name: e.Name(), Size: st.Size(), CreatedAt: st.ModTime().UTC().Format(time.RFC3339)})
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
		if err == nil && version != 1 {
			err = errors.New("备份数据库版本与当前程序不兼容")
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
