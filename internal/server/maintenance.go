package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"projnote/internal/backup"
	"projnote/internal/files"
	"projnote/internal/store"
)

func (s *Server) backupManager() *backup.Manager {
	s.backupOnce.Do(func() {
		if s.Backup == nil {
			s.Backup = &backup.Manager{Root: s.DataDir, DB: s.Store.DB, Gate: &s.Store.Mu, Settings: s.Store.Settings, Event: s.Store.RecordEvent, Validate: func(root string) error {
				st, err := store.Open(root)
				if err != nil {
					return err
				}
				defer st.Close()
				check, err := st.VerifyAudit()
				if err != nil {
					return err
				}
				if check["ok"] != true {
					return fmt.Errorf("审计链校验失败")
				}
				return files.CheckReferences(root, st.DB)
			}}
		}
	})
	return s.Backup
}
func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	exe, _ := os.Executable()
	settings, err := s.Store.Settings()
	if err != nil {
		handleErr(w, err)
		return
	}
	jsonResult(w, 200, map[string]any{"data_dir": s.DataDir, "executable": exe, "platform": runtime.GOOS, "gjb_level": "2", "dataset_id": settings["dataset_id"], "backup": s.backupManager().Health(), "migration_note": "迁移保留原目录；恢复替换当前业务数据，先保留完整回滚备份。两项均需停止服务后执行。"})
}
func quoteCommand(value string) string {
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
func (s *Server) maintenancePlan(w http.ResponseWriter, r *http.Request) {
	input, ok := body(w, r)
	if !ok {
		return
	}
	action, _ := input["action"].(string)
	exe, _ := os.Executable()
	program := quoteCommand(exe)
	if runtime.GOOS == "windows" {
		program = "& " + program
	}
	base := program + " --data-dir " + quoteCommand(s.DataDir)
	commands := []string{}
	notes := []string{"先保存草稿，再停止服务。停止命令会等待活动请求结束。"}
	if runtime.GOOS == "windows" {
		notes = append(notes, "以下指令在 PowerShell 中执行。")
	}
	result := map[string]any{"source_dir": s.DataDir, "platform": runtime.GOOS}
	switch action {
	case "relocate":
		target, _ := input["target_dir"].(string)
		target, err := backup.RelocationTarget(s.DataDir, target)
		if err != nil {
			writeError(w, 400, "invalid_target", err.Error())
			return
		}
		commands = append(commands, base+" --stop", base+" --relocate "+quoteCommand(target), program+" --data-dir "+quoteCommand(target))
		notes = append(notes, "新目录必须为空，旧目录完整保留；旧备份历史仍留在原目录，新目录含本次迁移恢复点。", "以后使用新目录启动命令，或将它保存为快捷方式。")
		result["target_dir"] = target
	case "restore":
		name, _ := input["backup_name"].(string)
		if !backup.SafeName(name) {
			writeError(w, 400, "invalid_backup", "请选择当前数据目录中的完整备份")
			return
		}
		archive := filepath.Join(s.DataDir, "backups", name)
		if _, err := os.Stat(archive); err != nil {
			writeError(w, 404, "not_found", "备份文件不存在")
			return
		}
		commands = append(commands, program+" --verify-backup "+quoteCommand(archive), base+" --stop", base+" --restore "+quoteCommand(archive), base)
		notes = append(notes, "恢复会替换当前业务数据，程序会先生成不参与自动清理的完整回滚点。", "恢复异常时保留整个数据目录，从完整备份恢复到新目录。")
		result["backup_path"] = archive
	default:
		writeError(w, 400, "invalid_action", "请选择迁移或恢复操作")
		return
	}
	result["commands"] = commands
	result["notes"] = notes
	jsonResult(w, 200, result)
}
