//go:build windows

package platform

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	taskNoticeClassOnce sync.Once
	taskNoticeClassErr  error
	taskNoticeInstance  uintptr
	taskNoticeClass     = windows.StringToUTF16Ptr("ProjNoteTaskNotificationV1")
	taskNoticeCallback  = syscall.NewCallback(taskNoticeWindowProc)
)

func TaskNotificationStatus() map[string]any {
	return map[string]any{"supported": true, "description": "到时弹出桌面任务提醒，20秒后自动收起，也可主动关闭；程序需保持后台运行。"}
}

func registerTaskNoticeClass() error {
	taskNoticeClassOnce.Do(func() {
		taskNoticeInstance, _, _ = compactKernel32.NewProc("GetModuleHandleW").Call(0)
		cursor, _, _ := compactUser32.NewProc("LoadCursorW").Call(0, 32512)
		class := compactWNDClass{Size: uint32(unsafe.Sizeof(compactWNDClass{})), Proc: taskNoticeCallback, Instance: taskNoticeInstance, Cursor: cursor, Background: 16, ClassName: taskNoticeClass}
		if ok, _, err := compactRegister.Call(uintptr(unsafe.Pointer(&class))); ok == 0 {
			taskNoticeClassErr = fmt.Errorf("任务提醒窗口注册失败: %v", err)
		}
	})
	return taskNoticeClassErr
}

// Each notice owns a short-lived UI thread. Delivery is acknowledged after the
// window is visible, so an unattended notice cannot block the next due task.
func NotifyTask(ctx context.Context, title, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := ctx.Err(); err != nil {
			ready <- err
			return
		}
		awareness := compactUser32.NewProc("SetThreadDpiAwarenessContext")
		if awareness.Find() == nil {
			if previous, _, _ := awareness.Call(^uintptr(3)); previous != 0 {
				defer awareness.Call(previous)
			}
		}
		if err := registerTaskNoticeClass(); err != nil {
			ready <- err
			return
		}
		text := func(value string) *uint16 {
			return windows.StringToUTF16Ptr(strings.ReplaceAll(value, "\x00", ""))
		}
		const style = 0x80000000 | 0x00C00000 | 0x00080000   // popup, caption, system close
		const exStyle = 0x00000008 | 0x00000080 | 0x08000000 // topmost, tool window, no focus theft
		hwnd, _, createErr := compactCreate.Call(exStyle, uintptr(unsafe.Pointer(taskNoticeClass)), uintptr(unsafe.Pointer(text(title))), style, 0, 0, 440, 245, 0, 0, taskNoticeInstance, 0)
		if hwnd == 0 {
			ready <- fmt.Errorf("任务提醒窗口创建失败: %v", createErr)
			return
		}
		done := make(chan struct{})
		defer close(done)
		var font uintptr
		defer func() {
			if exists, _, _ := compactUser32.NewProc("IsWindow").Call(hwnd); exists != 0 {
				compactDestroy.Call(hwnd)
			}
			// Destroying before a message loop starts also posts WM_QUIT.
			// Remove it before releasing this thread for another native window.
			var quit compactMSG
			compactUser32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&quit)), 0, 0x0012, 0x0012, 1)
			if font != 0 {
				compactGDI32.NewProc("DeleteObject").Call(font)
			}
		}()
		font = compactCreateSizedFont(14, compactWindowDPI(hwnd))
		dpi := compactWindowDPI(hwnd)
		scale := func(n int32) uintptr { return uintptr(compactScale(n, dpi)) }
		client := compactRect{Right: compactScale(420, dpi), Bottom: compactScale(205, dpi)}
		adjust := compactUser32.NewProc("AdjustWindowRectExForDpi")
		if adjust.Find() == nil {
			adjust.Call(uintptr(unsafe.Pointer(&client)), style, 0, exStyle, uintptr(dpi))
		} else {
			compactUser32.NewProc("AdjustWindowRectEx").Call(uintptr(unsafe.Pointer(&client)), style, 0, exStyle)
		}
		_, work := compactAreasForWindow(hwnd)
		width, height := client.Right-client.Left, client.Bottom-client.Top
		x, y := work.Right-width-compactScale(18, dpi), work.Bottom-height-compactScale(18, dpi)
		x, y = max(work.Left, x), max(work.Top, y)
		compactSetPos.Call(hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0x0010)
		createControl := func(class, value string, id uintptr, childStyle uintptr, x, y, width, height int32) error {
			child, _, err := compactCreate.Call(0, uintptr(unsafe.Pointer(text(class))), uintptr(unsafe.Pointer(text(value))), 0x40000000|0x10000000|childStyle, scale(x), scale(y), scale(width), scale(height), hwnd, id, taskNoticeInstance, 0)
			if child == 0 {
				return fmt.Errorf("任务提醒内容创建失败: %v", err)
			}
			if font != 0 {
				compactSend(child, 0x0030, font, 1)
			}
			return nil
		}
		// A read-only multiline control preserves long task names and lets the
		// user scroll, rather than silently clipping important reminder text.
		if err := createControl("EDIT", strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"), 2, 0x0004|0x0040|0x0800|0x00200000, 16, 14, 388, 135); err != nil {
			ready <- err
			return
		}
		if err := createControl("BUTTON", "关闭", 1, 0x00010000, 314, 162, 90, 30); err != nil {
			ready <- err
			return
		}
		if timer, _, err := compactUser32.NewProc("SetTimer").Call(hwnd, 1, 20000, 0); timer == 0 {
			ready <- fmt.Errorf("任务提醒自动收起设置失败: %v", err)
			return
		}
		if err := ctx.Err(); err != nil {
			ready <- err
			return
		}
		compactUser32.NewProc("ShowWindow").Call(hwnd, 4)
		compactUser32.NewProc("UpdateWindow").Call(hwnd)
		if visible, _, _ := compactUser32.NewProc("IsWindowVisible").Call(hwnd); visible == 0 {
			ready <- fmt.Errorf("任务提醒窗口未能显示")
			return
		}
		ready <- nil
		go func() {
			select {
			case <-ctx.Done():
				compactPost.Call(hwnd, 0x0010, 0, 0)
			case <-done:
			}
		}()
		var msg compactMSG
		for {
			result, _, _ := compactUser32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(result) <= 0 {
				return
			}
			if handled, _, _ := compactUser32.NewProc("IsDialogMessageW").Call(hwnd, uintptr(unsafe.Pointer(&msg))); handled != 0 {
				continue
			}
			compactUser32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			compactUser32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func taskNoticeWindowProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case 0x0010: // WM_CLOSE
		compactDestroy.Call(hwnd)
		return 0
	case 0x0111: // WM_COMMAND
		if wparam&0xffff == 1 || wparam&0xffff == 2 && (wparam>>16)&0xffff == 0 { // close button / Esc
			compactDestroy.Call(hwnd)
			return 0
		}
	case 0x0113: // WM_TIMER
		if wparam == 1 {
			compactDestroy.Call(hwnd)
			return 0
		}
	case 0x0002: // WM_DESTROY
		compactUser32.NewProc("KillTimer").Call(hwnd, 1)
		compactUser32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	result, _, _ := compactDefProc.Call(hwnd, uintptr(message), wparam, lparam)
	return result
}
