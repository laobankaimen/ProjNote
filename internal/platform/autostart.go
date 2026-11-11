package platform

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
)

// AutoStartManager owns only this application's current-user login entry. It
// never changes the entry until SetEnabled is called explicitly.
type AutoStartManager struct {
	mu         sync.Mutex
	executable string
	dataDir    string
	command    string
	backend    autoStartBackend
}

type autoStartEntry struct {
	exists, enabled, valid bool
	command                string
}

type autoStartBackend interface {
	read() (autoStartEntry, error)
	write(command string) error
	remove() error
	path() string
}

func NewAutoStartManager(executable, dataDir string) (*AutoStartManager, error) {
	for name, value := range map[string]string{"程序": executable, "数据目录": dataDir} {
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("%s必须使用绝对路径", name)
		}
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("%s路径不能包含换行或控制字符", name)
		}
	}
	executable, dataDir = filepath.Clean(executable), filepath.Clean(dataDir)
	backend, command, err := newAutoStartBackend(executable, dataDir)
	if err != nil {
		return nil, err
	}
	return &AutoStartManager{executable: executable, dataDir: dataDir, command: command, backend: backend}, nil
}

func (m *AutoStartManager) Status() (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status()
}

func (m *AutoStartManager) status() (map[string]any, error) {
	result := map[string]any{
		"supported": m.backend != nil, "enabled": false, "matches_current": false,
		"needs_repair": false, "entry_path": "", "executable": m.executable,
		"data_dir": m.dataDir, "message": "当前系统暂不支持自动登记登录启动项",
	}
	if m.backend == nil {
		return result, nil
	}
	result["entry_path"] = m.backend.path()
	entry, err := m.backend.read()
	if err != nil {
		return nil, fmt.Errorf("读取登录启动项失败：%w", err)
	}
	matches := entry.exists && entry.valid && entry.command == m.command
	result["enabled"] = entry.exists && entry.enabled
	result["matches_current"] = matches
	result["needs_repair"] = entry.exists && (!matches || !entry.enabled)
	result["registered_command"] = entry.command
	switch {
	case !entry.exists:
		result["message"] = "尚未启用登录后自动启动"
	case !entry.valid:
		result["message"] = "启动项配置异常，请修复后使用"
	case !matches:
		result["message"] = "启动项指向其他程序或数据目录，请修复以使用当前目录"
	case !entry.enabled:
		result["message"] = "启动项已被停用，可以重新启用"
	default:
		result["message"] = "已登记当前用户登录后启动，后台运行且不打开浏览器"
		if strings.HasPrefix(m.backend.path(), `HKEY_CURRENT_USER\`) {
			result["message"] = "已登记当前用户登录启动项；如在系统任务管理器中禁用，需在系统中重新启用"
		}
	}
	return result, nil
}

func (m *AutoStartManager) SetEnabled(enabled bool) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backend == nil {
		return nil, errors.New("当前系统暂不支持自动登记登录启动项")
	}
	var err error
	if enabled {
		err = m.backend.write(m.command)
	} else {
		err = m.backend.remove()
	}
	if err != nil {
		return nil, fmt.Errorf("更新登录启动项失败：%w", err)
	}
	return m.status()
}

// desktopExecArgument first quotes an Exec argument, then escapes the desktop
// string value. The launcher performs these two unescaping stages in reverse.
// https://specifications.freedesktop.org/desktop-entry/latest/exec-variables.html
func desktopExecArgument(value string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, ch := range value {
		switch ch {
		case '\\', '"', '`', '$':
			quoted.WriteByte('\\')
			quoted.WriteRune(ch)
		case '%':
			quoted.WriteString("%%")
		default:
			quoted.WriteRune(ch)
		}
	}
	quoted.WriteByte('"')
	return strings.ReplaceAll(quoted.String(), `\`, `\\`)
}

// windowsAutoStartCommand is a REG_SZ command line, without cmd.exe or variable
// expansion. Run values are limited to 260 UTF-16 code units by Windows.
func windowsAutoStartCommand(executable, dataDir string) (string, error) {
	if strings.ContainsRune(executable, '"') || strings.ContainsRune(dataDir, '"') {
		return "", errors.New("Windows 路径不能包含双引号")
	}
	command := quoteWindowsAutoStartArgument(executable) + " --data-dir " + quoteWindowsAutoStartArgument(dataDir)
	if len(utf16.Encode([]rune(command))) > 260 {
		return "", errors.New("登录启动命令超过 Windows 的 260 字符限制，请使用较短的程序和数据目录路径")
	}
	return command, nil
}

func quoteWindowsAutoStartArgument(value string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	backslashes := 0
	for _, ch := range value {
		if ch == '\\' {
			backslashes++
			continue
		}
		if ch == '"' {
			quoted.WriteString(strings.Repeat(`\`, backslashes*2+1))
		} else {
			quoted.WriteString(strings.Repeat(`\`, backslashes))
		}
		backslashes = 0
		quoted.WriteRune(ch)
	}
	quoted.WriteString(strings.Repeat(`\`, backslashes*2))
	quoted.WriteByte('"')
	return quoted.String()
}
