package webui

import "embed"

// Assets contains every resource needed to run without a network connection.
//
//go:embed all:dist
var Assets embed.FS
