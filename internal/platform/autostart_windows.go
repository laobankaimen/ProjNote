//go:build windows

package platform

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const autoStartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoStartRunValue = "ProjNote"

type registryAutoStartBackend struct{}

func newAutoStartBackend(executable, dataDir string) (autoStartBackend, string, error) {
	command, err := windowsAutoStartCommand(executable, dataDir)
	if err != nil {
		return nil, "", err
	}
	return registryAutoStartBackend{}, command, nil
}

func (registryAutoStartBackend) path() string {
	return `HKEY_CURRENT_USER\` + autoStartRunKey + `\` + autoStartRunValue
}

func (registryAutoStartBackend) read() (autoStartEntry, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, autoStartRunKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return autoStartEntry{}, nil
	}
	if err != nil {
		return autoStartEntry{}, err
	}
	defer key.Close()
	command, valueType, err := key.GetStringValue(autoStartRunValue)
	if errors.Is(err, registry.ErrNotExist) {
		return autoStartEntry{}, nil
	}
	if errors.Is(err, registry.ErrUnexpectedType) {
		return autoStartEntry{exists: true, enabled: true}, nil
	}
	if err != nil {
		return autoStartEntry{}, err
	}
	return autoStartEntry{exists: true, enabled: true, valid: valueType == registry.SZ && command != "", command: command}, nil
}

func (registryAutoStartBackend) write(command string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autoStartRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(autoStartRunValue, command)
}

func (registryAutoStartBackend) remove() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, autoStartRunKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	err = key.DeleteValue(autoStartRunValue)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}
