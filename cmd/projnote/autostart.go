package main

import (
	"os"

	"projnote/internal/platform"
	"projnote/internal/server"
)

func configureAutoStart(app *server.Server, dataDir string) {
	executable, err := os.Executable()
	var manager *platform.AutoStartManager
	if err == nil {
		manager, err = platform.NewAutoStartManager(executable, dataDir)
	}
	if err != nil {
		// A broken user autostart configuration must not stop the application.
		app.AutoStartStatus = func() (map[string]any, error) { return nil, err }
		app.SetAutoStart = func(bool) (map[string]any, error) { return nil, err }
		return
	}
	app.AutoStartStatus = manager.Status
	app.SetAutoStart = manager.SetEnabled
}
