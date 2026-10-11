package webui

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.css app.js i18n.js
var files embed.FS

func FS() fs.FS { return files }

func Index() ([]byte, error) { return files.ReadFile("index.html") }
