//go:build !embed

package ui

import (
	"io/fs"
	"os"
)

func assetFS() fs.FS { return os.DirFS("web/dist") }
