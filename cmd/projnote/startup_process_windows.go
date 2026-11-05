//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var startupKernel32 = windows.NewLazySystemDLL("kernel32.dll")

func prepareDesktopProcess() error {
	// A GUI launch stays detached. Only explicitly requested terminal operations
	// attach to the caller's console; never hide, free or reconfigure its console.
	if maintenanceConsoleRequested(os.Args[1:]) && !hasStartupConsole() {
		if attached, _, _ := startupKernel32.NewProc("AttachConsole").Call(^uintptr(0)); attached != 0 {
			if output, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
				os.Stdout, os.Stderr = output, output
			}
		}
	}
	return nil
}
func hasStartupConsole() bool {
	hwnd, _, _ := startupKernel32.NewProc("GetConsoleWindow").Call()
	return hwnd != 0
}
func startDesktopBackground(string, desktopBackgroundOptions) (bool, error) { return false, nil }
func desktopServiceReady(string)                                            {}
func desktopProcessDone(error)                                              {}
func presentStartupFailure(message string) {
	if hasStartupConsole() {
		return
	}
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("ProjNote 启动失败")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
