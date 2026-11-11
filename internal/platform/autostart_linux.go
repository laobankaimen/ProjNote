//go:build linux

package platform

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const autoStartDesktopName = "org.projnote.ProjNote.desktop"
const autoStartDesktopMarker = "X-ProjNote-Autostart=true"

type desktopAutoStartBackend struct{ entryPath string }

func newAutoStartBackend(executable, dataDir string) (autoStartBackend, string, error) {
	if strings.ContainsRune(executable, '=') {
		return nil, "", errors.New("桌面启动项的程序路径不能包含等号，请调整程序所在位置")
	}
	// GLib checks the executable's existence before expanding Exec field codes.
	// Even a correctly escaped %% in argv[0] therefore fails to load. Keep the
	// data argument fully escaped, but reject this unsupported launcher path.
	if strings.ContainsRune(executable, '%') {
		return nil, "", errors.New("Linux 登录启动的程序路径不能包含百分号，请将程序移到不含百分号的目录")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return nil, "", err
	}
	if !filepath.IsAbs(config) {
		return nil, "", errors.New("用户配置目录必须是绝对路径")
	}
	command := desktopExecArgument(executable) + " --data-dir " + desktopExecArgument(dataDir)
	return &desktopAutoStartBackend{entryPath: filepath.Join(config, "autostart", autoStartDesktopName)}, command, nil
}

func (b *desktopAutoStartBackend) path() string { return b.entryPath }

func (b *desktopAutoStartBackend) readContents() ([]byte, bool, error) {
	info, err := os.Lstat(b.entryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, true, errors.New("启动项必须是普通文件，不能是符号链接或目录")
	}
	if info.Size() > 32<<10 {
		return nil, true, errors.New("启动项文件过大，无法安全读取")
	}
	f, err := os.Open(b.entryPath)
	if err != nil {
		return nil, true, err
	}
	defer f.Close()
	contents, err := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	if len(contents) > 32<<10 {
		return nil, true, errors.New("启动项文件过大，无法安全读取")
	}
	return contents, true, err
}

func desktopEntryValues(contents []byte) (map[string]string, bool) {
	values := make(map[string]string)
	inEntry, foundEntry, valid := false, false, true
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			if inEntry && foundEntry {
				valid = false
			}
			foundEntry = foundEntry || inEntry
			continue
		}
		if !inEntry {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			valid = false
			continue
		}
		if _, duplicate := values[key]; duplicate {
			valid = false
		}
		values[key] = strings.TrimSpace(value)
	}
	return values, valid && foundEntry && scanner.Err() == nil
}

func (b *desktopAutoStartBackend) read() (autoStartEntry, error) {
	contents, exists, err := b.readContents()
	if err != nil || !exists {
		return autoStartEntry{}, err
	}
	values, valid := desktopEntryValues(contents)
	valid = valid && values["Type"] == "Application" && values["Name"] != "" && values["Exec"] != "" && values["X-ProjNote-Autostart"] == "true"
	// Unexpected launch restrictions or a shell/terminal wrapper require an
	// explicit repair; adding such a key must not silently look healthy.
	for _, key := range []string{"OnlyShowIn", "NotShowIn", "TryExec"} {
		valid = valid && values[key] == ""
	}
	valid = valid && (values["Terminal"] == "" || values["Terminal"] == "false")
	for _, key := range []string{"Hidden", "X-GNOME-Autostart-enabled"} {
		valid = valid && (values[key] == "" || values[key] == "true" || values[key] == "false")
	}
	enabled := values["Hidden"] != "true" && values["X-GNOME-Autostart-enabled"] != "false"
	return autoStartEntry{exists: true, enabled: enabled, valid: valid, command: values["Exec"]}, nil
}

func (b *desktopAutoStartBackend) checkOwned() error {
	contents, exists, err := b.readContents()
	if err != nil || !exists {
		return err
	}
	values, _ := desktopEntryValues(contents)
	if values["X-ProjNote-Autostart"] != "true" {
		return errors.New("同名启动项不属于 ProjNote，请先移除冲突文件")
	}
	return nil
}

func (b *desktopAutoStartBackend) write(command string) error {
	if err := b.checkOwned(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.entryPath), 0700); err != nil {
		return err
	}
	contents := "[Desktop Entry]\nType=Application\nName=ProjNote\nComment=ProjNote desktop entry\nExec=" + command + "\nTerminal=false\nHidden=false\nX-GNOME-Autostart-enabled=true\n" + autoStartDesktopMarker + "\n"
	f, err := os.CreateTemp(filepath.Dir(b.entryPath), ".projnote-autostart-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(contents); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = b.checkOwned(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), b.entryPath); err != nil {
		return fmt.Errorf("写入启动项：%w", err)
	}
	return nil
}

func (b *desktopAutoStartBackend) remove() error {
	if err := b.checkOwned(); err != nil {
		return err
	}
	err := os.Remove(b.entryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
