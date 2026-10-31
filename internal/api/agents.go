package api

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
