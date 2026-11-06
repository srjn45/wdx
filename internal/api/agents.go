package api

import (
	"fmt"
	"strconv"
)

// FlattenAgents returns the agents in list and all their children, each
// before its children.
func FlattenAgents(list []Agent) []Agent {
	var flat []Agent
	for _, a := range list {
		flat = append(flat, a)
		if a.Children != nil {
			flat = append(flat, FlattenAgents(*a.Children)...)
		}
	}
	return flat
}

// TokenCount writes n tokens short, rounded down: the number itself under a
// thousand (850), in thousands under a million (150k), and in millions with one
// decimal from there (1.5M, and 2M with no .0). The wdx doc CONTEXT_SIZE.
func TokenCount(n int64) string {
	switch {
	case n < 1_000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strconv.FormatInt(n/1_000, 10) + "k"
	}
	tenths := n / 100_000
	if tenths%10 == 0 {
		return strconv.FormatInt(tenths/10, 10) + "M"
	}
	return fmt.Sprintf("%d.%dM", tenths/10, tenths%10)
}
