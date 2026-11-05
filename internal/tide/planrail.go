package tide

// The rail draws a plan's DAG at the left of its tasks, the way git log
// --graph draws commits: one row per task in DAG order, and connector rows
// where edges split from a task or join into one. docs/PLANS.md, Step 8.

// maxRailLanes is the most lanes the rail is drawn with; past it the tasks
// are a plain list.
const maxRailLanes = 6

// railTask is what the rail needs of a task: its id and the tasks it waits
// for.
type railTask struct {
	id    string
	after []string
}

// railEdge is the edge from a task to one that waits for it.
type railEdge struct {
	from, to string
}

// railCell is one column of a rail's row. A lane takes two: the first holds
// its line or the task's node, the second what joins it to the lane at its
// right.
type railCell struct {
	r rune // a box-drawing rune or a space; a space where node is set
	// node marks the cell holding the task's glyph, which the tab draws.
	node bool
	// edges are those the cell draws, so that the tab can style the edges of
	// the task under the cursor.
	edges []railEdge
}

// railRow is a task's row, or a connector's when task is empty.
type railRow struct {
	task  string
	cells []railCell
}

// layoutRail lays out the rail of tasks, given in DAG order: each task's
// after names tasks before it. A lane carries one edge down from the task
// that unblocks to the one that waits. A task's node is in the leftmost lane
// arriving at it, the others joining it on a connector row before it; a task
// with none takes the leftmost free lane. The first task it unblocks keeps
// its lane, the others opening the leftmost free lanes to its right on a
// connector row after it. A lane that ends leaves a gap, nothing shifts, and
// a connector's line drawn across a lane crosses it with ┼. Every row has
// the same width, two cells a lane. It returns false past maxRailLanes lanes,
// when the rail is left out.
func layoutRail(tasks []railTask) ([]railRow, bool) {
	unblocks := map[string][]string{}
	for _, t := range tasks {
		for _, a := range t.after {
			unblocks[a] = append(unblocks[a], t.id)
		}
	}

	var (
		rows  []railRow
		lanes []*railEdge // the edge each lane carries; nil where it is free
	)
	freeLane := func(from int) int {
		for l := from; l < len(lanes); l++ {
			if lanes[l] == nil {
				return l
			}
		}
		lanes = append(lanes, nil)
		return len(lanes) - 1
	}

	for _, t := range tasks {
		var arriving []int
		for l, e := range lanes {
			if e != nil && e.to == t.id {
				arriving = append(arriving, l)
			}
		}
		node := 0
		if len(arriving) == 0 {
			node = freeLane(0)
		} else {
			node = arriving[0]
		}
		if len(arriving) > 1 {
			rows = append(rows, connectorRow(lanes, node, arriving[1:], '┴', '╯'))
			for _, l := range arriving[1:] {
				lanes[l] = nil
			}
		}
		rows = append(rows, taskRow(lanes, t.id, node))
		lanes[node] = nil

		next := unblocks[t.id]
		if len(next) == 0 {
			continue
		}
		lanes[node] = &railEdge{t.id, next[0]}
		if len(next) > 1 {
			var opened []int
			for _, to := range next[1:] {
				l := freeLane(node + 1)
				lanes[l] = &railEdge{t.id, to}
				opened = append(opened, l)
			}
			rows = append(rows, connectorRow(lanes, node, opened, '┬', '╮'))
		}
	}

	if len(lanes) > maxRailLanes {
		return nil, false
	}
	for i := range rows {
		for len(rows[i].cells) < 2*len(lanes) {
			rows[i].cells = append(rows[i].cells, railCell{r: ' '})
		}
	}
	return rows, true
}

// laneCells draws the lanes as they pass a row: a line where a lane carries
// an edge.
func laneCells(lanes []*railEdge) []railCell {
	cells := make([]railCell, 2*len(lanes))
	for l, e := range lanes {
		cells[2*l] = railCell{r: ' '}
		cells[2*l+1] = railCell{r: ' '}
		if e != nil {
			cells[2*l] = railCell{r: '│', edges: []railEdge{*e}}
		}
	}
	return cells
}

func taskRow(lanes []*railEdge, task string, node int) railRow {
	cells := laneCells(lanes)
	cells[2*node] = railCell{r: ' ', node: true}
	if e := lanes[node]; e != nil {
		cells[2*node].edges = []railEdge{*e}
	}
	return railRow{task: task, cells: cells}
}

// connectorRow draws the lanes of others, all right of node, splitting from
// or joining into node's lane: tee where one meets the line and does not end
// it, corner where the last one does.
func connectorRow(lanes []*railEdge, node int, others []int, tee, corner rune) railRow {
	cells := laneCells(lanes)
	last := others[len(others)-1]
	// The line from node to a lane draws that lane's edge, so every cell up
	// to a lane draws its edge too.
	lineEdges := func(col int) []railEdge {
		var edges []railEdge
		for _, l := range others {
			if 2*l >= col {
				edges = append(edges, *lanes[l])
			}
		}
		return edges
	}
	isOther := map[int]bool{}
	for _, l := range others {
		isOther[l] = true
	}

	cells[2*node] = railCell{r: '├', edges: append([]railEdge{*lanes[node]}, lineEdges(0)...)}
	for col := 2*node + 1; col <= 2*last; col++ {
		edges := lineEdges(col)
		l := col / 2
		switch {
		case col%2 == 1:
			cells[col] = railCell{r: '─', edges: edges}
		case l == last:
			cells[col] = railCell{r: corner, edges: edges}
		case isOther[l]:
			cells[col] = railCell{r: tee, edges: edges}
		case lanes[l] != nil:
			cells[col] = railCell{r: '┼', edges: append([]railEdge{*lanes[l]}, edges...)}
		default:
			cells[col] = railCell{r: '─', edges: edges}
		}
	}
	return railRow{cells: cells}
}
