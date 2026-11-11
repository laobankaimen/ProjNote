//go:build windows

package platform

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	compactUser32      = windows.NewLazySystemDLL("user32.dll")
	compactGDI32       = windows.NewLazySystemDLL("gdi32.dll")
	compactKernel32    = windows.NewLazySystemDLL("kernel32.dll")
	compactRegister    = compactUser32.NewProc("RegisterClassExW")
	compactCreate      = compactUser32.NewProc("CreateWindowExW")
	compactDefProc     = compactUser32.NewProc("DefWindowProcW")
	compactPost        = compactUser32.NewProc("PostMessageW")
	compactDestroy     = compactUser32.NewProc("DestroyWindow")
	compactSetPos      = compactUser32.NewProc("SetWindowPos")
	compactGetRect     = compactUser32.NewProc("GetWindowRect")
	compactCursor      = compactUser32.NewProc("GetCursorPos")
	compactMonitor     = compactUser32.NewProc("MonitorFromWindow")
	compactMonitorInfo = compactUser32.NewProc("GetMonitorInfoW")
	compactDPI         = compactUser32.NewProc("GetDpiForWindow")
	compactWindowsMu   sync.Mutex
	compactWindows     = map[uintptr]*windowsCompact{}
	compactClassOnce   sync.Once
	compactClassErr    error
	compactInstance    uintptr
	compactPanelBrush  uintptr
	compactPaperBrush  uintptr
	compactBlueBrush   uintptr
	compactClassName   = windows.StringToUTF16Ptr("ProjNoteCompactEntryV1")
	compactCallback    = syscall.NewCallback(compactWindowProc)
)

type compactPoint struct{ X, Y int32 }
type compactRect struct{ Left, Top, Right, Bottom int32 }
type compactMonitorData struct {
	Size          uint32
	Monitor, Work compactRect
	Flags         uint32
}
type compactWNDClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}
type compactMSG struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   compactPoint
	Private uint32
}
type compactPaint struct {
	DC                   uintptr
	Erase                int32
	Rect                 compactRect
	Restore, Incremental int32
	Reserved             [32]byte
}
type windowsCompact struct {
	hwnd                                                      uintptr
	font, entryFont                                           uintptr
	fontDPI, panelDPI                                         uint32
	compactURL, fullURL                                       string
	done                                                      chan struct{}
	closeOnce                                                 sync.Once
	closing                                                   atomic.Bool
	entryPressed                                              bool
	dpi                                                       uint32
	client                                                    *compactClient
	panel                                                     uintptr
	controls                                                  map[int]uintptr
	projects                                                  []compactProject
	tasks                                                     []compactTask
	completing                                                bool
	draft                                                     compactDraft
	initialized, draftBusy, saving, dirty, conflict, suppress bool
	view                                                      compactViewState
	firstDraftRead, autoProject, projectsLoaded               bool
	pendingMu                                                 sync.Mutex
	pending                                                   []func()
	ctx                                                       context.Context
}

func compactRegisterClass() error {
	compactClassOnce.Do(func() {
		compactInstance, _, _ = compactKernel32.NewProc("GetModuleHandleW").Call(0)
		cursor, _, _ := compactUser32.NewProc("LoadCursorW").Call(0, 32512)
		compactPanelBrush, _, _ = compactGDI32.NewProc("CreateSolidBrush").Call(0xfaf6f3)
		compactPaperBrush, _, _ = compactGDI32.NewProc("CreateSolidBrush").Call(0xffffff)
		compactBlueBrush, _, _ = compactGDI32.NewProc("CreateSolidBrush").Call(0xce6524)
		class := compactWNDClass{Size: uint32(unsafe.Sizeof(compactWNDClass{})), Proc: compactCallback, Instance: compactInstance, Cursor: cursor, Background: 16, ClassName: compactClassName}
		result, _, err := compactRegister.Call(uintptr(unsafe.Pointer(&class)))
		if result == 0 {
			compactClassErr = fmt.Errorf("RegisterClassExW: %v", err)
		}
	})
	return compactClassErr
}
func startCompact(ctx context.Context, compactURL, fullURL string) (io.Closer, error) {
	type result struct {
		entry *windowsCompact
		err   error
	}
	ready := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// Keep monitor coordinates and all native controls in the same physical
		// pixel space; this UI thread alone opts in to per-monitor DPI handling.
		awareness := compactUser32.NewProc("SetThreadDpiAwarenessContext")
		if awareness.Find() == nil {
			if previous, _, _ := awareness.Call(^uintptr(3)); previous != 0 { // PER_MONITOR_AWARE_V2
				defer awareness.Call(previous)
			}
		}
		if err := compactRegisterClass(); err != nil {
			ready <- result{err: err}
			return
		}
		client, clientErr := newCompactClient(compactURL)
		if clientErr != nil {
			ready <- result{err: clientErr}
			return
		}
		entry := &windowsCompact{compactURL: compactURL, fullURL: fullURL, done: make(chan struct{}), client: client, controls: map[int]uintptr{}, ctx: ctx}
		hwnd, _, err := compactCreate.Call(0x00000008|0x00000080|0x08000000, uintptr(unsafe.Pointer(compactClassName)), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("ProjNote 便签入口"))), 0x80000000, 0, 0, compactEntryWidth, compactEntryHeight, 0, 0, compactInstance, 0)
		if hwnd == 0 {
			ready <- result{err: fmt.Errorf("CreateWindowExW: %v", err)}
			return
		}
		entry.hwnd = hwnd
		compactWindowsMu.Lock()
		compactWindows[hwnd] = entry
		compactWindowsMu.Unlock()
		entry.reposition()
		compactUser32.NewProc("ShowWindow").Call(hwnd, 4)
		compactUser32.NewProc("UpdateWindow").Call(hwnd)
		ready <- result{entry: entry}
		defer close(entry.done)
		defer func() { compactWindowsMu.Lock(); delete(compactWindows, hwnd); compactWindowsMu.Unlock() }()
		var message compactMSG
		for {
			value, _, e := compactUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
			if int32(value) <= 0 {
				if int32(value) < 0 {
					slog.Warn("compact window message loop", "error", e)
				}
				break
			}
			if entry.panel != 0 {
				if handled, _, _ := compactUser32.NewProc("IsDialogMessageW").Call(entry.panel, uintptr(unsafe.Pointer(&message))); handled != 0 {
					continue
				}
			}
			compactUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
			compactUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
		}
	}()
	select {
	case result := <-ready:
		if result.err != nil {
			return nil, result.err
		}
		go func() {
			select {
			case <-ctx.Done():
				_ = result.entry.Close()
			case <-result.entry.done:
			}
		}()
		return &compactEntry{Closer: result.entry, state: CompactState{Backend: "win32", Limitation: "Win32原生便签和今日任务；仅显式完整工作台命令打开浏览器，关闭入口不停止服务。"}}, nil
	case <-ctx.Done():
		go func() {
			result := <-ready
			if result.entry != nil {
				_ = result.entry.Close()
			}
		}()
		return nil, ctx.Err()
	}
}
func (e *windowsCompact) Close() error {
	e.closeOnce.Do(func() { e.closing.Store(true); compactPost.Call(e.hwnd, 0x0010, 0, 0) })
	select {
	case <-e.done:
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("桌面入口尚未响应关闭，主服务仍在运行")
	}
}
func (e *windowsCompact) Show() error {
	if e.closing.Load() {
		return errCompactClosed
	}
	select {
	case <-e.done:
		return errCompactClosed
	default:
	}
	// Native controls belong to the locked UI thread; only post a request here.
	if ok, _, err := compactPost.Call(e.hwnd, 0x8032, 0, 0); ok == 0 {
		return fmt.Errorf("原生便签无法重新显示，入口可能已关闭: %w", err)
	}
	return nil
}
func (e *windowsCompact) rect() compactRect {
	var rect compactRect
	compactGetRect.Call(e.hwnd, uintptr(unsafe.Pointer(&rect)))
	return rect
}
func compactAreasForWindow(hwnd uintptr) (compactGeometryRect, compactGeometryRect) {
	monitor, _, _ := compactMonitor.Call(hwnd, 2)
	info := compactMonitorData{Size: uint32(unsafe.Sizeof(compactMonitorData{}))}
	if ok, _, _ := compactMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		return geometryRect(info.Monitor), geometryRect(info.Work)
	}
	var area compactRect
	compactUser32.NewProc("SystemParametersInfoW").Call(48, 0, uintptr(unsafe.Pointer(&area)), 0)
	return geometryRect(area), geometryRect(area)
}
func (e *windowsCompact) monitorAreas() (compactGeometryRect, compactGeometryRect) {
	return compactAreasForWindow(e.hwnd)
}
func geometryRect(r compactRect) compactGeometryRect {
	return compactGeometryRect{r.Left, r.Top, r.Right, r.Bottom}
}
func (e *windowsCompact) workarea() compactGeometryRect {
	monitor, work := e.monitorAreas()
	return compactVisibleBounds(monitor, work)
}
func compactWindowDPI(hwnd uintptr) uint32 {
	if compactDPI.Find() == nil {
		if dpi, _, _ := compactDPI.Call(hwnd); dpi > 0 {
			return uint32(dpi)
		}
	}
	return compactBaseDPI
}
func compactCreateFont(dpi uint32) uintptr { return compactCreateSizedFont(15, dpi) }
func compactCreateSizedFont(size int32, dpi uint32) uintptr {
	fontName := windows.StringToUTF16Ptr("Microsoft YaHei UI")
	font, _, _ := compactGDI32.NewProc("CreateFontW").Call(uintptr(-compactScale(size, dpi)), 0, 0, 0, 500, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fontName)))
	return font
}
func (e *windowsCompact) updateEntryFont(dpi uint32) {
	if e.entryFont != 0 && e.dpi == dpi {
		return
	}
	if font := compactCreateSizedFont(24, dpi); font != 0 {
		old := e.entryFont
		e.entryFont, e.dpi = font, dpi
		if old != 0 {
			compactGDI32.NewProc("DeleteObject").Call(old)
		}
	}
}
func (e *windowsCompact) updatePanelFont(dpi uint32) {
	if e.font != 0 && e.fontDPI == dpi {
		return
	}
	font := compactCreateFont(dpi)
	if font == 0 {
		return
	}
	old := e.font
	e.font, e.fontDPI = font, dpi
	for _, control := range e.controls {
		compactSend(control, 0x0030, font, 1)
	}
	if list := e.controls[compactTasks]; list != 0 {
		compactSend(list, 0x01A0, 0, uintptr(compactScale(compactTaskRowHeight, dpi)))
	}
	if old != 0 {
		compactGDI32.NewProc("DeleteObject").Call(old)
	}
}
func (e *windowsCompact) reposition() {
	dpi := compactWindowDPI(e.hwnd)
	monitor, work := e.monitorAreas()
	rect := compactDockGeometry(monitor, work, dpi)
	compactSetPos.Call(e.hwnd, ^uintptr(0), uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.width()), uintptr(rect.height()), 0x0010)
	e.updateEntryFont(dpi)
	compactUser32.NewProc("InvalidateRect").Call(e.hwnd, 0, 1)
}
func (e *windowsCompact) showEntry() {
	if e.view.PanelVisible {
		return
	}
	e.reposition()
	compactUser32.NewProc("ShowWindow").Call(e.hwnd, 4)
}
func (e *windowsCompact) ShowEntry() error {
	if e.closing.Load() {
		return errCompactClosed
	}
	select {
	case <-e.done:
		return errCompactClosed
	default:
	}
	if ok, _, err := compactPost.Call(e.hwnd, 0x8033, 0, 0); ok == 0 {
		return fmt.Errorf("便签入口无法重新显示: %w", err)
	}
	return nil
}
func (e *windowsCompact) menu() {
	menu, _, _ := compactUser32.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		return
	}
	defer compactUser32.NewProc("DestroyMenu").Call(menu)
	for i, title := range []string{"打开完整工作台", "退出便签入口（服务继续）"} {
		compactUser32.NewProc("AppendMenuW").Call(menu, 0, uintptr(i+1), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))))
	}
	var point compactPoint
	compactCursor.Call(uintptr(unsafe.Pointer(&point)))
	compactUser32.NewProc("SetForegroundWindow").Call(e.hwnd)
	command, _, _ := compactUser32.NewProc("TrackPopupMenu").Call(menu, 0x0100|0x0002, uintptr(point.X), uintptr(point.Y), 0, e.hwnd, 0)
	switch command {
	case 1:
		e.open(true)
	case 2:
		compactPost.Call(e.hwnd, 0x0010, 0, 0)
	}
	compactPost.Call(e.hwnd, 0, 0, 0)
}
func (e *windowsCompact) open(full bool) {
	if full {
		go func() {
			if err := OpenBrowser(e.fullURL); err != nil {
				slog.Warn("full workbench browser failed", "error", err)
			}
		}()
		return
	}
	e.showPanel()
}
func compactWindowProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	compactWindowsMu.Lock()
	entry := compactWindows[hwnd]
	compactWindowsMu.Unlock()
	if entry == nil {
		result, _, _ := compactDefProc.Call(hwnd, uintptr(message), wparam, lparam)
		return result
	}
	if hwnd == entry.panel {
		return entry.panelProc(message, wparam, lparam)
	}
	switch message {
	case 0x8031:
		entry.runPending()
		return 0
	case 0x8032:
		if !entry.closing.Load() {
			entry.showPanel()
		}
		return 0
	case 0x8033:
		if !entry.closing.Load() {
			entry.showEntry()
		}
		return 0
	case 0x0021: // WM_MOUSEACTIVATE: preserve the user's focused editor.
		return 3 // MA_NOACTIVATE
	case 0x0201:
		entry.entryPressed = true
		compactUser32.NewProc("SetCapture").Call(hwnd)
		return 0
	case 0x0200: // Pointer motion never moves either native window.
		return 0
	case 0x0202:
		pressed := entry.entryPressed
		entry.entryPressed = false
		compactUser32.NewProc("ReleaseCapture").Call()
		x, y := int32(int16(lparam&0xffff)), int32(int16((lparam>>16)&0xffff))
		rect := entry.rect()
		if pressed && x >= 0 && y >= 0 && x < rect.Right-rect.Left && y < rect.Bottom-rect.Top {
			entry.showPanel()
		}
		return 0
	case 0x0215, 0x001F:
		entry.entryPressed = false
		if message == 0x001F {
			compactUser32.NewProc("ReleaseCapture").Call()
		}
		return 0
	case 0x007B:
		entry.entryPressed = false
		compactUser32.NewProc("ReleaseCapture").Call()
		entry.menu()
		return 0
	case 0x007E, 0x001A, 0x02E0:
		entry.reposition()
		if entry.panel != 0 {
			entry.fitPanelToDPI()
		}
		return 0
	case 0x000F:
		var paint compactPaint
		dc, _, _ := compactUser32.NewProc("BeginPaint").Call(hwnd, uintptr(unsafe.Pointer(&paint)))
		if dc != 0 {
			defer compactUser32.NewProc("EndPaint").Call(hwnd, uintptr(unsafe.Pointer(&paint)))
			var rect compactRect
			compactUser32.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&rect)))
			compactUser32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&rect)), compactBlueBrush)
			compactGDI32.NewProc("SetBkMode").Call(dc, 1)
			compactGDI32.NewProc("SetTextColor").Call(dc, 0xffffff)
			old := uintptr(0)
			if entry.entryFont != 0 {
				old, _, _ = compactGDI32.NewProc("SelectObject").Call(dc, entry.entryFont)
			}
			draw := func(label string, target compactRect) {
				compactUser32.NewProc("DrawTextW").Call(dc, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(label))), ^uintptr(0), uintptr(unsafe.Pointer(&target)), 0x0001|0x0004|0x0020)
			}
			draw("‹", rect)
			if old != 0 {
				compactGDI32.NewProc("SelectObject").Call(dc, old)
			}
		}
		return 0
	case 0x0010:
		entry.closing.Store(true)
		compactDestroy.Call(hwnd)
		return 0
	case 0x0002:
		if entry.panel != 0 {
			compactDestroy.Call(entry.panel)
			compactWindowsMu.Lock()
			delete(compactWindows, entry.panel)
			compactWindowsMu.Unlock()
			entry.panel = 0
		}
		for _, font := range []uintptr{entry.font, entry.entryFont} {
			if font != 0 {
				compactGDI32.NewProc("DeleteObject").Call(font)
			}
		}
		entry.font, entry.entryFont = 0, 0
		compactUser32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	result, _, _ := compactDefProc.Call(hwnd, uintptr(message), wparam, lparam)
	return result
}
