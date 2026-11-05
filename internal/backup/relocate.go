package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RelocationTarget allows only an empty, independent local directory. Resolve
// existing ancestors so symlink aliases cannot put the destination in source.
func RelocationTarget(source, target string) (string, error) {
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("目标数据目录必须是绝对路径")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", err
	}
	target = filepath.Clean(target)
	resolved := target
	tail := []string{}
	for {
		if _, err = os.Lstat(resolved); err == nil {
			resolved, err = filepath.EvalSymlinks(resolved)
			if err != nil {
				return "", err
			}
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		tail = append(tail, filepath.Base(resolved))
		parent := filepath.Dir(resolved)
		if parent == resolved {
			return "", fmt.Errorf("找不到目标目录的现有父目录")
		}
		resolved = parent
	}
	for n := len(tail) - 1; n >= 0; n-- {
		resolved = filepath.Join(resolved, tail[n])
	}
	target = resolved
	nested := func(base, name string) bool {
		rel, e := filepath.Rel(base, name)
		return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if nested(source, target) || nested(target, source) {
		return "", fmt.Errorf("新旧数据目录不能相同或互相包含")
	}
	entries, err := os.ReadDir(target)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil && len(entries) > 0 {
		return "", fmt.Errorf("目标目录必须为空，原有内容不会被覆盖")
	}
	return target, nil
}
