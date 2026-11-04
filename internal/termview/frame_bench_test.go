package termview

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/screen"
)

// BenchmarkPaneFrame is what a frame of a pane costs tide, before Bubble
// Tea's own diff of it: the screen rendered to styled lines by the vt
// (Model.Content), and those lines drawn into tide's buffer, which parses
// them back into cells (internal/tide/follow.go). The content is a
// recording of Claude Code made at 120x40; the larger sizes show how the
// cost grows with the cells, the recording filling their top-left.
func BenchmarkPaneFrame(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join("..", "aca", "testdata", "claude_code", "2.1.314", "background-prompts", "raw.out"))
	if err != nil {
		b.Skip(err)
	}
	for _, size := range []struct{ cols, rows int }{{120, 40}, {200, 60}, {300, 80}} {
		s := screen.New(size.cols, size.rows, nil)
		s.Write(raw)
		area := uv.Rect(0, 0, size.cols, size.rows)
		buf := uv.NewScreenBuffer(size.cols, size.rows)
		name := fmt.Sprintf("%dx%d", size.cols, size.rows)
		b.Run(name+"/render", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = s.Render()
			}
		})
		rendered := s.Render()
		b.Run(name+"/draw", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				uv.NewStyledString(rendered).Draw(buf, area)
			}
		})
		b.Run(name+"/frame", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				uv.NewStyledString(s.Render()).Draw(buf, area)
			}
		})
		s.Close()
	}
}
