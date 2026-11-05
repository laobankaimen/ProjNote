package server

import (
	_ "embed"
	"mime"
	"net/http"
)

//go:embed assets/task-import-template.xlsx
var taskImportTemplate []byte

func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "ProjNote-任务计划导入模板.xlsx"}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(taskImportTemplate)
}
