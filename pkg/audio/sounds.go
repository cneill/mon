package audio

import (
	"github.com/gopxl/beep/v2"
)

type Sound struct {
	Name   string
	Format beep.Format
	Buffer *beep.Buffer
}
