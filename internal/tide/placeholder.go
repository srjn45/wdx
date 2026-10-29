package tide

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/srjn45/wdx/internal/style"
)

// keptInputs is how many of its last inputs a placeholder shows.
const keptInputs = 6

// placeholder stands in for a pane's content until tide shows real ones:
// it shows its name, its size and the last input it got, so the layout's
// sizes and where input goes can be seen.
type placeholder struct {
	name   string
	inputs []string
}

// input records what the pane got: a key, a click, a paste.
func (p *placeholder) input(s string) {
	p.inputs = append(p.inputs, s)
	if len(p.inputs) > keptInputs {
		p.inputs = p.inputs[1:]
	}
}

func (p *placeholder) draw(buf uv.Screen, area uv.Rectangle, st style.Style) {
	inputs := "nothing yet"
	if len(p.inputs) > 0 {
		inputs = strings.Join(p.inputs, "  ")
	}
	text := st.Name(p.name) + "\n" +
		st.Muted(fmt.Sprintf("%d×%d", area.Dx(), area.Dy())) + "\n" +
		st.Muted("got: ") + inputs
	uv.NewStyledString(text).Draw(buf, area)
}
