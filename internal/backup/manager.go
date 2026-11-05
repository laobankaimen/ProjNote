package backup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"projnote/internal/platform"
)

// Health is persisted outside business data so backup failures survive restarts.
type Health struct {
	LastSuccess    string `json:"last_success"`
	LastError      string `json:"last_error"`
	CheckedAt      string `json:"checked_at"`
	InProgress     bool   `json:"in_progress"`
	Warning        string `json:"warning"`
	AvailableBytes uint64 `json:"available_bytes"`
	ArchiveName    string `json:"archive_name"`
}
type Manager struct {
	Root     string
	DB       *sql.DB
	Gate     *sync.RWMutex
	Settings func() (map[string]any, error)
	Event    func(action, entityType, entityID string, detail map[string]any) error
	Validate func(string) error
	mu       sync.Mutex
	healthMu sync.Mutex
	health   Health
}

func (m *Manager) Health() Health {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	if m.health.CheckedAt == "" {
		if raw, e := os.ReadFile(filepath.Join(m.Root, "backup-health.json")); e == nil {
			_ = json.Unmarshal(raw, &m.health)
			m.health.InProgress = false
		}
	}
	return m.health
}
func (m *Manager) setHealth(change func(*Health)) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	change(&m.health)
	raw, e := json.Marshal(m.health)
	if e == nil {
		_ = atomicWrite(filepath.Join(m.Root, "backup-health.json"), raw)
	}
}
func atomicWrite(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (m *Manager) policy() (int, int64, bool, error) {
	settings, err := m.Settings()
	if err != nil {
		return 0, 0, false, err
	}
	keep, e := strconv.Atoi(fmt.Sprint(settings["backup_keep_count"]))
	if e != nil || keep < 2 || keep > 365 {
		keep = 30
	}
	mb, e := strconv.ParseInt(fmt.Sprint(settings["backup_max_mb"]), 10, 64)
	if e != nil || mb < 0 {
		mb = 0
	}
	auto := fmt.Sprint(settings["backup_auto"]) != "0"
	return keep, mb << 20, auto, nil
}
func (m *Manager) RunDaily() error {
	_, _, auto, err := m.policy()
	if err != nil {
		return err
	}
	if !auto {
		return nil
	}
	health := m.Health()
	if at, e := time.Parse(time.RFC3339Nano, health.LastSuccess); e == nil && at.Local().Format("2006-01-02") == time.Now().Format("2006-01-02") {
		// A deleted recovery point cannot satisfy today's backup requirement.
		if SafeName(health.ArchiveName) {
			if info, e := os.Stat(filepath.Join(m.Root, "backups", health.ArchiveName)); e == nil && info.Mode().IsRegular() {
				return nil
			}
		}
	}
	_, err = m.Create("automatic", false)
	return err
}
func (m *Manager) Create(reason string, protected bool) (info Info, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.Health()
	m.setHealth(func(h *Health) {
		h.InProgress = true
		h.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
		h.LastError = ""
		h.Warning = ""
	})
	defer func() {
		m.setHealth(func(h *Health) {
			h.InProgress = false
			if err != nil {
				h.LastError = err.Error()
			}
		})
	}()
	if err = m.checkSpace(); err != nil {
		return info, err
	}
	m.Gate.Lock()
	info, err = Create(m.Root, m.DB)
	m.Gate.Unlock()
	if err != nil {
		return info, err
	}
	if protected {
		if err = Protect(m.Root, info.Name, reason); err != nil {
			return info, err
		}
		info.Protected = true
	}
	if err = m.verify(info.Name); err != nil {
		return info, fmt.Errorf("新备份校验失败，未清理旧备份: %w", err)
	}
	if m.Event != nil {
		if err = m.Event("backup", "system", info.Name, map[string]any{"size": info.Size, "reason": reason, "verified": true, "protected": protected}); err != nil {
			return info, err
		}
	}
	m.setHealth(func(h *Health) {
		h.LastSuccess = time.Now().UTC().Format(time.RFC3339Nano)
		h.ArchiveName = info.Name
		h.LastError = ""
	})
	if !protected {
		_, warn, e := m.prune(info.Name)
		if e != nil {
			m.setHealth(func(h *Health) { h.Warning = "备份已验证，但清理旧备份失败：" + e.Error() })
		} else if warn != "" {
			m.setHealth(func(h *Health) { h.Warning = warn })
		}
	}
	return info, nil
}
func (m *Manager) verify(name string) error {
	stage, _, err := Extract(filepath.Join(m.Root, "backups", name), m.Root)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if m.Validate != nil {
		return m.Validate(stage)
	}
	return nil
}
func (m *Manager) checkSpace() error {
	available, err := platform.AvailableBytes(m.Root)
	if err != nil {
		return fmt.Errorf("无法检查备份磁盘空间: %w", err)
	}
	m.setHealth(func(h *Health) { h.AvailableBytes = available })
	var size uint64
	for _, name := range []string{"app.db", "attachments", "templates"} {
		base := filepath.Join(m.Root, name)
		if _, e := os.Stat(base); os.IsNotExist(e) {
			continue
		}
		err = filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("备份目录包含链接")
			}
			if d.IsDir() {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			size += uint64(info.Size())
			return nil
		})
		if err != nil {
			return err
		}
	}
	if db, e := os.Stat(filepath.Join(m.Root, "app.db")); e == nil {
		size += uint64(db.Size())
	}
	required := size*2 + size/50 + (64 << 20)
	if available < required {
		return fmt.Errorf("磁盘可用空间不足：预计需要 %d MB，当前可用 %d MB；旧备份未删除", required>>20, available>>20)
	}
	return nil
}
func Protect(root, name, reason string) error {
	if !SafeName(name) {
		return fmt.Errorf("备份名称无效")
	}
	return atomicWrite(filepath.Join(root, "backups", name+".protected"), []byte(reason))
}

// prune only runs after validation of the newest complete archive. Protected
// upgrade/restore points and the newest verified archive are never deleted.
func (m *Manager) prune(newest string) ([]string, string, error) {
	keep, maxBytes, _, err := m.policy()
	if err != nil {
		return nil, "", err
	}
	items, err := List(m.Root)
	if err != nil {
		return nil, "", err
	}
	var total int64
	normal := 0
	for _, i := range items {
		total += i.Size
		if !i.Protected {
			normal++
		}
	}
	removed := []string{}
	for n := len(items) - 1; n >= 0; n-- {
		i := items[n]
		if i.Protected || i.Name == newest {
			continue
		}
		if normal <= keep && (maxBytes == 0 || total <= maxBytes) {
			break
		}
		if err = os.Remove(filepath.Join(m.Root, "backups", i.Name)); err != nil {
			return removed, "", err
		}
		removed = append(removed, i.Name)
		normal--
		total -= i.Size
	}
	warning := ""
	if maxBytes > 0 && total > maxBytes {
		warning = "已达到容量预算，最近的有效备份与受保护的回滚点已保留"
	}
	if len(removed) > 0 && m.Event != nil {
		if err = m.Event("backup_prune", "system", newest, map[string]any{"removed": removed}); err != nil {
			return removed, warning, err
		}
	}
	return removed, warning, nil
}
func (m *Manager) Prune() (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := List(m.Root)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return map[string]any{"removed": []string{}}, nil
	}
	newest := items[0].Name
	if err = m.verify(newest); err != nil {
		return nil, fmt.Errorf("最新备份未通过校验，未清理任何文件: %w", err)
	}
	removed, warning, err := m.prune(newest)
	if err != nil {
		return nil, err
	}
	m.setHealth(func(h *Health) { h.Warning = warning })
	return map[string]any{"removed": removed, "warning": warning}, nil
}
func (m *Manager) MigrationBackup(reason string) (Info, error) {
	return m.Create(strings.TrimSpace(reason), true)
}
