package tide

import (
	"slices"
	"strings"
	"testing"
)

// drawRail draws rows one a line, the rail with ● for a node, then | and
// the task's id, so that the rail's width shows.
func drawRail(rows []railRow) string {
	var b strings.Builder
	for _, row := range rows {
		for _, c := range row.cells {
			if c.node {
				b.WriteRune('●')
			} else {
				b.WriteRune(c.r)
			}
		}
		b.WriteString("|")
		b.WriteString(row.task)
		b.WriteString("\n")
	}
	return b.String()
}

func task(id string, after ...string) railTask {
	return railTask{id: id, after: after}
}

func TestLayoutRail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		tasks []railTask
		want  string
	}{
		{
			name:  "chain",
			tasks: []railTask{task("a"), task("b", "a"), task("c", "b")},
			want: "" +
				"● |a\n" +
				"● |b\n" +
				"● |c\n",
		},
		{
			name:  "split that never joins",
			tasks: []railTask{task("a"), task("b", "a"), task("c", "a"), task("d", "a")},
			want: "" +
				"●     |a\n" +
				"├─┬─╮ |\n" +
				"● │ │ |b\n" +
				"  ● │ |c\n" +
				"    ● |d\n",
		},
		{
			name:  "diamond",
			tasks: []railTask{task("a"), task("b", "a"), task("c", "a"), task("d", "b", "c")},
			want: "" +
				"●   |a\n" +
				"├─╮ |\n" +
				"● │ |b\n" +
				"│ ● |c\n" +
				"├─╯ |\n" +
				"●   |d\n",
		},
		{
			name:  "edge passing rows",
			tasks: []railTask{task("a"), task("b", "a"), task("c"), task("d", "c"), task("e", "a")},
			want: "" +
				"●   |a\n" +
				"├─╮ |\n" +
				"● │ |b\n" +
				"● │ |c\n" +
				"● │ |d\n" +
				"  ● |e\n",
		},
		{
			name:  "two roots",
			tasks: []railTask{task("a"), task("b"), task("c", "a", "b")},
			want: "" +
				"●   |a\n" +
				"│ ● |b\n" +
				"├─╯ |\n" +
				"●   |c\n",
		},
		{
			name: "lines crossing a lane",
			tasks: []railTask{
				task("schema"), task("oidc", "schema"), task("docs", "schema"),
				task("callback", "oidc"), task("button", "oidc"), task("e2e", "callback", "button"),
			},
			want: "" +
				"●     |schema\n" +
				"├─╮   |\n" +
				"● │   |oidc\n" +
				"├─┼─╮ |\n" +
				"│ ● │ |docs\n" +
				"●   │ |callback\n" +
				"│   ● |button\n" +
				"├───╯ |\n" +
				"●     |e2e\n",
		},
		{
			name:  "split right of a free lane",
			tasks: []railTask{task("a"), task("b", "a"), task("c", "a"), task("d", "c"), task("e", "c")},
			want: "" +
				"●     |a\n" +
				"├─╮   |\n" +
				"● │   |b\n" +
				"  ●   |c\n" +
				"  ├─╮ |\n" +
				"  ● │ |d\n" +
				"    ● |e\n",
		},
		{
			name: "6 lanes",
			tasks: []railTask{
				task("a"), task("b", "a"), task("c", "a"), task("d", "a"), task("e", "a"), task("f", "a"), task("g", "a"),
			},
			want: "" +
				"●           |a\n" +
				"├─┬─┬─┬─┬─╮ |\n" +
				"● │ │ │ │ │ |b\n" +
				"  ● │ │ │ │ |c\n" +
				"    ● │ │ │ |d\n" +
				"      ● │ │ |e\n" +
				"        ● │ |f\n" +
				"          ● |g\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows, ok := layoutRail(tt.tasks)
			if !ok {
				t.Fatal("rail left out")
			}
			if got := drawRail(rows); got != tt.want {
				t.Errorf("rail:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestLayoutRailLeftOutPast6Lanes(t *testing.T) {
	t.Parallel()
	tasks := []railTask{task("a")}
	for _, id := range []string{"b", "c", "d", "e", "f", "g", "h"} {
		tasks = append(tasks, task(id, "a"))
	}
	if rows, ok := layoutRail(tasks); ok {
		t.Errorf("rail of 7 lanes drawn:\n%s", drawRail(rows))
	}
}

// TestLayoutRailEdges checks the edges each cell draws, which the tab styles
// for the task under the cursor: in the diamond, those of a→c.
func TestLayoutRailEdges(t *testing.T) {
	t.Parallel()
	rows, _ := layoutRail([]railTask{task("a"), task("b", "a"), task("c", "a"), task("d", "b", "c")})
	ac := railEdge{"a", "c"}
	var b strings.Builder
	for _, row := range rows {
		for _, c := range row.cells {
			switch {
			case slices.Contains(c.edges, ac):
				b.WriteRune('#')
			default:
				b.WriteRune('.')
			}
		}
		b.WriteString("\n")
	}
	want := "" +
		"....\n" + // a
		"###.\n" + // ├─╮: the split carries a→c to its lane
		"..#.\n" + // b, with a→c passing
		"..#.\n" + // c, its node where it ends
		"....\n" +
		"....\n"
	if got := b.String(); got != want {
		t.Errorf("cells drawing a→c:\n%s\nwant:\n%s", got, want)
	}
}
