//go:build !linux && !windows

package platform

func newAutoStartBackend(executable, dataDir string) (autoStartBackend, string, error) {
	return nil, "", nil
}
