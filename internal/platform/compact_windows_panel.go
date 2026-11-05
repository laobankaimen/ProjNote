//go:build windows

package platform

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	compactProjectID    = 1002
	compactBody         = 1003
	compactSave         = 1004
	compactRefresh      = 1005
	compactStatus       = 1006
	compactTasks        = 1007
	compactClose        = 1008
	compactRestore      = 1009
	compactVersion      = 1010
	compactMode         = 1011
	compactHeading      = 1012
	compactTodayHeading = 1013
	compactFull         = 1014
)

const compactPanelStyle = 0x80000000
const compactPanelExStyle = 0x00000008 | 0x00000080

type compactDrawItem struct {
	ControlType, ControlID, ItemID, Action, State uint32
	Window, DC                                    uintptr
	Rect                                          compactRect
	Data                                          uintptr
}

func compactText(hwnd uintptr) string {
	n, _, _ := compactUser32.NewProc("GetWindowTextLengthW").Call(hwnd)
	if n > 2<<20 {
		n = 2 << 20
	}
	text := make([]uint16, n+1)
	compactUser32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&text[0])), n+1)
	return windows.UTF16ToString(text)
}
func compactSetText(hwnd uintptr, value string) {
	compactUser32.NewProc("SetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(value))))
}
func compactSend(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	r, _, _ := compactUser32.NewProc("SendMessageW").Call(hwnd, uintptr(message), wparam, lparam)
	return r
}
func (e *windowsCompact) statusText(value string) { compactSetText(e.controls[compactStatus], value) }
func (e *windowsCompact) postUI(fn func()) {
	e.pendingMu.Lock()
	e.pending = append(e.pending, fn)
	e.pendingMu.Unlock()
	compactPost.Call(e.hwnd, 0x8031, 0, 0)
}
func (e *windowsCompact) runPending() {
	e.pendingMu.Lock()
	pending := e.pending
	e.pending = nil
	e.pendingMu.Unlock()
	for _, fn := range pending {
		fn()
	}
}
func (e *windowsCompact) createControl(class, text string, id int, style uintptr) {
	hwnd, _, _ := compactCreate.Call(0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(class))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))), 0x40000000|0x10000000|style, 0, 0, 100, 25, e.panel, uintptr(id), compactInstance, 0)
	e.controls[id] = hwnd
	if e.font != 0 {
		compactSend(hwnd, 0x0030, e.font, 1)
	}
}
func compactPanelOuterSize(client compactGeometrySize, dpi uint32) compactGeometrySize {
	r := compactRect{Right: client.Width, Bottom: client.Height}
	adjust := compactUser32.NewProc("AdjustWindowRectExForDpi")
	if adjust.Find() == nil {
		if ok, _, _ := adjust.Call(uintptr(unsafe.Pointer(&r)), compactPanelStyle, 0, compactPanelExStyle, uintptr(dpi)); ok != 0 {
			return compactGeometrySize{r.Right - r.Left, r.Bottom - r.Top}
		}
	}
	compactUser32.NewProc("AdjustWindowRectEx").Call(uintptr(unsafe.Pointer(&r)), compactPanelStyle, 0, compactPanelExStyle)
	return compactGeometrySize{r.Right - r.Left, r.Bottom - r.Top}
}
func (e *windowsCompact) fitPanelToDPI() {
	if e.panel == 0 {
		return
	}
	dpi := compactWindowDPI(e.panel)
	var client compactRect
	compactUser32.NewProc("GetClientRect").Call(e.panel, uintptr(unsafe.Pointer(&client)))
	size := compactGeometrySize{client.Right, client.Bottom}
	if e.panelDPI != 0 && e.panelDPI != dpi {
		size.Width = int32((int64(size.Width)*int64(dpi) + int64(e.panelDPI)/2) / int64(e.panelDPI))
		size.Height = int32((int64(size.Height)*int64(dpi) + int64(e.panelDPI)/2) / int64(e.panelDPI))
	}
	minimum := compactPanelMinimumClient(dpi)
	size.Width = max(size.Width, minimum.Width)
	size.Height = max(size.Height, minimum.Height)
	e.panelDPI = dpi
	e.updatePanelFont(dpi)
	outer := compactPanelOuterSize(size, dpi)
	monitor, work := compactAreasForWindow(e.panel)
	area := compactVisibleBounds(monitor, work)
	position := compactPanelGeometry(area, outer, dpi)
	compactSetPos.Call(e.panel, ^uintptr(0), uintptr(position.Left), uintptr(position.Top), uintptr(position.width()), uintptr(position.height()), 0x0010)
	e.layoutPanel()
}
func (e *windowsCompact) showPanel() {
	if e.panel == 0 {
		dpi := compactWindowDPI(e.hwnd)
		size := compactPanelOuterSize(compactGeometrySize{compactScale(compactPanelWidth, dpi), compactScale(compactPanelHeight, dpi)}, dpi)
		area := e.workarea()
		hwnd, _, err := compactCreate.Call(compactPanelExStyle, uintptr(unsafe.Pointer(compactClassName)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("ProjNote 便签"))), compactPanelStyle, uintptr(area.Left), uintptr(area.Top), uintptr(size.Width), uintptr(size.Height), 0, 0, compactInstance, 0)
		if hwnd == 0 {
			e.statusText(fmt.Sprintf("便签窗口创建失败: %v", err))
			return
		}
		e.panel = hwnd
		e.panelDPI = compactWindowDPI(hwnd)
		e.updatePanelFont(e.panelDPI)
		compactWindowsMu.Lock()
		compactWindows[hwnd] = e
		compactWindowsMu.Unlock()
		e.suppress = true
		e.createControl("STATIC", "今日工作", compactHeading, 0)
		e.createControl("COMBOBOX", "", compactMode, 0x0003|0x00010000)
		compactSend(e.controls[compactMode], 0x0143, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("添加任务"))))
		compactSend(e.controls[compactMode], 0x0143, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("现场速记"))))
		compactSend(e.controls[compactMode], 0x014E, 0, 0)
		e.createControl("COMBOBOX", "", compactProjectID, 0x0003|0x00200000|0x00010000)
		compactSend(e.controls[compactProjectID], 0x0143, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("未归类"))))
		compactSend(e.controls[compactProjectID], 0x014E, 0, 0)
		e.createControl("EDIT", "", compactBody, 0x00800000|0x00200000|0x0004|0x0040|0x1000|0x00010000)
		e.createControl("BUTTON", "添加任务", compactSave, 0x00010000|0x000B)
		e.createControl("BUTTON", "刷新任务", compactRefresh, 0x00010000)
		e.createControl("BUTTON", "恢复草稿", compactRestore, 0x00010000)
		e.createControl("STATIC", "正在读取正文草稿…", compactStatus, 0)
		e.createControl("STATIC", "今日任务", compactTodayHeading, 0)
		e.createControl("LISTBOX", "", compactTasks, 0x00800000|0x00200000|0x0001|0x1000)
		e.createControl("BUTTON", "×", compactClose, 0x00010000)
		e.createControl("BUTTON", "打开完整工作台", compactFull, 0x00010000)
		e.createControl("STATIC", "版本读取中…", compactVersion, 0x0002)
		go func() {
			version, err := e.client.version(e.ctx)
			if err != nil {
				version = "版本未知"
			}
			e.postUI(func() {
				if e.panel != 0 {
					compactSetText(e.controls[compactVersion], version)
				}
			})
		}()
		compactSend(e.controls[compactBody], 0x00C5, 2<<20, 0)
		e.suppress = false
		e.layoutPanel()
		e.loadDraft(false)
	} else if !e.initialized && !e.draftBusy {
		e.loadDraft(false)
	}
	area := e.workarea()
	var current compactRect
	compactGetRect.Call(e.panel, uintptr(unsafe.Pointer(&current)))
	position := compactPanelGeometry(area, compactGeometrySize{current.Right - current.Left, current.Bottom - current.Top}, e.panelDPI)
	compactSetPos.Call(e.panel, ^uintptr(0), uintptr(position.Left), uintptr(position.Top), uintptr(position.width()), uintptr(position.height()), 0)
	e.view = e.view.transition(compactViewOpenPanel)
	compactUser32.NewProc("ShowWindow").Call(e.panel, 5)
	compactUser32.NewProc("ShowWindow").Call(e.hwnd, 0)
	e.fitPanelToDPI()
	compactUser32.NewProc("SetForegroundWindow").Call(e.panel)
	compactUser32.NewProc("SetFocus").Call(e.controls[compactBody])
	e.refreshPanel()
}
func (e *windowsCompact) layoutPanel() {
	if e.panel == 0 {
		return
	}
	var client compactRect
	compactUser32.NewProc("GetClientRect").Call(e.panel, uintptr(unsafe.Pointer(&client)))
	layout := compactPanelLayoutFor(client.Right, client.Bottom, compactWindowDPI(e.panel))
	for id, rect := range map[int]compactGeometryRect{
		compactHeading: layout.Heading, compactMode: layout.Mode, compactProjectID: layout.Project,
		compactBody: layout.Body, compactSave: layout.Save, compactRestore: layout.Restore, compactRefresh: layout.Refresh,
		compactStatus: layout.Status, compactTodayHeading: layout.TodayHeading, compactTasks: layout.Tasks,
		compactClose: layout.Close, compactFull: layout.Full, compactVersion: layout.Version,
	} {
		compactUser32.NewProc("MoveWindow").Call(e.controls[id], uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.width()), uintptr(rect.height()), 1)
	}
	compactSend(e.controls[compactTasks], 0x01A0, 0, uintptr(layout.TaskRowHeight))
}
func (e *windowsCompact) selectedProject() string {
	index := int(compactSend(e.controls[compactProjectID], 0x0147, 0, 0))
	if index > 0 && index <= len(e.projects) {
		return e.projects[index-1].ID
	}
	return ""
}
func (e *windowsCompact) currentDraft() compactDraft {
	mode := "task"
	if compactSend(e.controls[compactMode], 0x0147, 0, 0) == 1 {
		mode = "record"
	}
	return compactDraft{Content: compactText(e.controls[compactBody]), ProjectID: e.selectedProject(), Mode: mode, Version: e.draft.Version}
}
func (e *windowsCompact) selectMode(mode string) {
	index, label := uintptr(0), "添加任务"
	if mode == "record" {
		index, label = 1, "保存速记"
	}
	compactSend(e.controls[compactMode], 0x014E, index, 0)
	compactSetText(e.controls[compactSave], label)
	compactUser32.NewProc("InvalidateRect").Call(e.controls[compactSave], 0, 1)
}
func (e *windowsCompact) maybeAutoProject() {
	if !e.autoProject || !e.projectsLoaded {
		return
	}
	e.autoProject = false
	draft := e.currentDraft()
	if draft.Mode == "task" && strings.TrimSpace(draft.Content) == "" && draft.ProjectID == "" && len(e.projects) == 1 {
		e.selectProject(e.projects[0].ID)
		e.scheduleDraft()
	}
}
func (e *windowsCompact) selectProject(id string) {
	index := 0
	for i, p := range e.projects {
		if p.ID == id {
			index = i + 1
			break
		}
	}
	if id != "" && index == 0 {
		e.projects = append(e.projects, compactProject{ID: id, Name: "原草稿项目（需确认可写）"})
		compactSend(e.controls[compactProjectID], 0x0143, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(e.projects[len(e.projects)-1].Name))))
		index = len(e.projects)
	}
	compactSend(e.controls[compactProjectID], 0x014E, uintptr(index), 0)
}
func (e *windowsCompact) editing(enabled bool) {
	value := uintptr(0)
	if enabled {
		value = 1
	}
	for _, id := range []int{compactMode, compactProjectID, compactBody, compactSave, compactRestore} {
		compactUser32.NewProc("EnableWindow").Call(e.controls[id], value)
	}
}
func (e *windowsCompact) loadDraft(explicit bool) {
	if e.draftBusy || e.saving {
		return
	}
	before := e.currentDraft()
	e.draftBusy = true
	e.statusText("正在读取正文草稿…")
	go func() {
		draft, err := e.client.draft(e.ctx)
		e.postUI(func() {
			e.draftBusy = false
			if err != nil {
				e.statusText("草稿读取失败，输入保留：" + err.Error())
				return
			}
			e.initialized = true
			e.draft.Version = draft.Version
			now := e.currentDraft()
			if explicit || (!e.dirty && now.Content == before.Content && now.ProjectID == before.ProjectID && now.Mode == before.Mode) {
				e.suppress = true
				compactSetText(e.controls[compactBody], draft.Content)
				e.selectProject(draft.ProjectID)
				e.selectMode(draft.Mode)
				e.autoProject = !e.firstDraftRead && !explicit && draft.UpdatedAt == "" && draft.Mode == "task" && strings.TrimSpace(draft.Content) == "" && draft.ProjectID == ""
				e.suppress = false
				e.dirty = false
				e.conflict = false
				e.draft = draft
				e.statusText("草稿与模式就绪；收起不丢内容")
				e.maybeAutoProject()
			} else {
				e.conflict = true
				e.statusText("读取期间已有输入，保留本地；可明确恢复服务端草稿")
			}
			e.firstDraftRead = true
		})
	}()
}
func (e *windowsCompact) scheduleDraft() {
	if e.suppress {
		return
	}
	e.dirty = true
	compactUser32.NewProc("KillTimer").Call(e.panel, 1)
	compactUser32.NewProc("SetTimer").Call(e.panel, 1, 1000, 0)
}
func (e *windowsCompact) persistDraft() {
	if !e.initialized || e.draftBusy || e.saving || e.conflict || !e.dirty {
		return
	}
	snapshot := e.currentDraft()
	e.draftBusy = true
	e.statusText("正在保存正文草稿…")
	go func() {
		result, err := e.client.putDraft(e.ctx, snapshot)
		e.postUI(func() {
			e.draftBusy = false
			if err != nil {
				if h, ok := err.(*compactHTTPError); ok && h.status == 409 {
					e.conflict = true
				}
				e.statusText("草稿未写入，输入仍保留：" + err.Error())
				return
			}
			e.draft = result
			now := e.currentDraft()
			e.dirty = now.Content != snapshot.Content || now.ProjectID != snapshot.ProjectID || now.Mode != snapshot.Mode
			if e.dirty {
				e.scheduleDraft()
			} else {
				e.statusText("正文草稿已保存")
			}
		})
	}()
}
func (e *windowsCompact) saveNote() {
	if e.saving || e.draftBusy {
		e.statusText("草稿正在提交，请稍后保存记录")
		return
	}
	if !e.initialized {
		e.statusText("草稿尚未读取确认，请恢复连接后点击恢复草稿；输入仍保留")
		return
	}
	snapshot := e.currentDraft()
	if strings.TrimSpace(snapshot.Content) == "" {
		if snapshot.Mode == "task" {
			e.statusText("请填写任务标题")
		} else {
			e.statusText("请填写速记正文")
		}
		return
	}
	if snapshot.Mode == "task" && snapshot.ProjectID == "" {
		e.statusText("请选择项目后添加任务")
		return
	}
	confirmed := "速记已保存"
	if snapshot.Mode == "task" {
		confirmed = "已添加到今日任务"
	}
	e.saving = true
	e.editing(false)
	e.statusText("正在保存…")
	version := e.draft.Version
	conflict := e.conflict
	go func() {
		flushedDraft := snapshot
		if !conflict {
			flushed, flushErr := e.client.putDraft(e.ctx, snapshot)
			if flushErr != nil {
				e.postUI(func() {
					e.saving = false
					e.editing(true)
					if he, ok := flushErr.(*compactHTTPError); ok && he.status == 409 {
						e.conflict = true
					}
					e.statusText("正文仍保留，草稿写入未确认：" + flushErr.Error())
				})
				return
			}
			version = flushed.Version
			flushedDraft = flushed
		}
		_, err := e.client.saveDraftContent(e.ctx, snapshot)
		if err != nil {
			e.postUI(func() {
				if !conflict {
					e.draft = flushedDraft
				}
				e.saving = false
				e.editing(true)
				e.statusText("保存失败，输入保留：" + err.Error())
			})
			return
		}
		var cleared compactDraft
		var clearErr error
		if version > 0 && !conflict {
			cleared, clearErr = e.client.putDraft(e.ctx, compactDraft{Content: "", ProjectID: snapshot.ProjectID, Mode: snapshot.Mode, Version: version})
		}
		e.postUI(func() {
			e.saving = false
			e.suppress = true
			compactSetText(e.controls[compactBody], "")
			e.suppress = false
			e.dirty = false
			e.editing(true)
			if clearErr != nil || conflict {
				e.conflict = true
				e.statusText(confirmed + "，请勿重复提交；服务端草稿保留，请明确恢复核对")
			} else {
				e.draft = cleared
				e.statusText(confirmed)
			}
			e.refreshPanel()
		})
	}()
}
func (e *windowsCompact) refreshPanel() {
	go func() {
		projects, err := e.client.projects(e.ctx)
		tasks, taskErr := e.client.today(e.ctx)
		e.postUI(func() {
			if e.panel == 0 {
				return
			}
			if err != nil {
				e.statusText("项目读取失败，输入保留：" + err.Error())
			} else {
				selected := e.selectedProject()
				e.suppress = true
				e.projects = projects
				e.projectsLoaded = true
				compactSend(e.controls[compactProjectID], 0x014B, 0, 0)
				compactSend(e.controls[compactProjectID], 0x0143, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("未归类"))))
				for _, p := range projects {
					compactSend(e.controls[compactProjectID], 0x0143, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(p.Name))))
				}
				e.selectProject(selected)
				e.suppress = false
				e.maybeAutoProject()
			}
			if taskErr != nil {
				e.statusText("今日任务读取失败，输入保留：" + taskErr.Error())
				return
			}
			compactSend(e.controls[compactTasks], 0x0184, 0, 0)
			if len(tasks) == 0 {
				compactSend(e.controls[compactTasks], 0x0180, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("今日暂无任务"))))
			}
			for _, task := range tasks {
				label := "待办 · " + task.Name
				if task.Status == "doing" {
					label = "进行中 · " + task.Name
				}
				if task.PlanEnd != "" {
					label += "  " + task.PlanEnd
				}
				compactSend(e.controls[compactTasks], 0x0180, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label))))
			}
		})
	}()
}
func (e *windowsCompact) hidePanel() {
	if e.panel != 0 {
		compactUser32.NewProc("ShowWindow").Call(e.panel, 0)
		e.persistDraft()
	}
	e.view = e.view.transition(compactViewHidePanel)
	e.entryPressed = false
	e.showEntry()
}
func (e *windowsCompact) panelProc(message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case 0x00A1: // No caption, resize or native drag behavior.
		return 0
	case 0x00A3:
		return 0
	case 0x0112: // Alt+F4 normally sends SC_CLOSE; it only returns to the icon.
		if wparam&0xfff0 == 0xf060 {
			e.hidePanel()
			return 0
		}
		if wparam&0xfff0 == 0xf010 || wparam&0xfff0 == 0xf000 {
			return 0
		}
	case 0x0104: // WM_SYSKEYDOWN, including when the borderless panel has focus.
		if wparam == 0x73 {
			e.hidePanel()
			return 0
		}
	case 0x0014: // WM_ERASEBKGND
		var rect compactRect
		compactUser32.NewProc("GetClientRect").Call(e.panel, uintptr(unsafe.Pointer(&rect)))
		compactUser32.NewProc("FillRect").Call(wparam, uintptr(unsafe.Pointer(&rect)), compactPanelBrush)
		return 1
	case 0x0138, 0x0135: // native static/button backgrounds
		color := uintptr(0x4a3324)
		if lparam == e.controls[compactVersion] {
			color = 0x9d8471
		}
		compactGDI32.NewProc("SetTextColor").Call(wparam, color)
		compactGDI32.NewProc("SetBkColor").Call(wparam, 0xfaf6f3)
		return compactPanelBrush
	case 0x0133, 0x0134: // native edit/list backgrounds
		compactGDI32.NewProc("SetTextColor").Call(wparam, 0x4a3324)
		compactGDI32.NewProc("SetBkColor").Call(wparam, 0xffffff)
		return compactPaperBrush
	case 0x002B: // WM_DRAWITEM for the primary save button
		if wparam == compactSave {
			item := (*compactDrawItem)(unsafe.Pointer(lparam))
			brush, color := compactBlueBrush, uintptr(0xffffff)
			if item.State&4 != 0 {
				brush, color = compactPanelBrush, 0xa08b7d
			}
			compactUser32.NewProc("FillRect").Call(item.DC, uintptr(unsafe.Pointer(&item.Rect)), brush)
			compactGDI32.NewProc("SetBkMode").Call(item.DC, 1)
			compactGDI32.NewProc("SetTextColor").Call(item.DC, color)
			old, _, _ := compactGDI32.NewProc("SelectObject").Call(item.DC, e.font)
			label := compactText(item.Window)
			compactUser32.NewProc("DrawTextW").Call(item.DC, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label))), ^uintptr(0), uintptr(unsafe.Pointer(&item.Rect)), 0x0001|0x0004|0x0020)
			compactGDI32.NewProc("SelectObject").Call(item.DC, old)
			if item.State&0x10 != 0 {
				compactUser32.NewProc("DrawFocusRect").Call(item.DC, uintptr(unsafe.Pointer(&item.Rect)))
			}
			return 1
		}
	case 0x02E0: // WM_DPICHANGED: resize controls, font and minimum together.
		e.fitPanelToDPI()
		return 0
	case 0x0005:
		e.layoutPanel()
		return 0
	case 0x0113:
		if wparam == 1 {
			compactUser32.NewProc("KillTimer").Call(e.panel, 1)
			e.persistDraft()
		}
		return 0
	case 0x0010:
		e.hidePanel()
		return 0
	case 0x0111:
		id, notification := int(wparam&0xffff), int((wparam>>16)&0xffff)
		if id == compactMode && notification == 1 {
			e.selectMode(e.currentDraft().Mode)
			e.scheduleDraft()
			if e.currentDraft().Mode == "task" {
				e.statusText("任务模式：选择项目，第一行作标题")
			} else {
				e.statusText("速记模式：保存为现场记录")
			}
			return 0
		}
		if id == compactBody && notification == 0x0300 || id == compactProjectID && notification == 1 {
			e.scheduleDraft()
			return 0
		}
		if notification == 0 {
			switch id {
			case compactSave:
				e.saveNote()
			case compactRefresh:
				e.refreshPanel()
			case compactClose:
				e.hidePanel()
			case compactFull:
				e.open(true)
			case compactRestore:
				result, _, _ := compactUser32.NewProc("MessageBoxW").Call(e.panel, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("用服务端正文草稿替换当前内容？"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("恢复草稿"))), 0x00000001|0x00000020)
				if result == 1 {
					e.loadDraft(true)
				}
			}
		}
		return 0
	}
	r, _, _ := compactDefProc.Call(e.panel, uintptr(message), wparam, lparam)
	return r
}
