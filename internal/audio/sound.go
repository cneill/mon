package audio

import (
	"embed"

	"github.com/gopxl/beep/v2"
)

//go:embed assets/*.wav
var builtinAssets embed.FS

type HookType string

const (
	HookCommitNew      HookType = "commit_new"
	HookFileNew        HookType = "file_new"
	HookFileDelete     HookType = "file_delete"
	HookPackageNew     HookType = "package_new"
	HookPackageUpgrade HookType = "package_upgrade"
	HookPackageDelete  HookType = "package_delete"
)

type Sound struct {
	Name   string
	Hook   HookType
	Format beep.Format
	Buffer *beep.Buffer
}
