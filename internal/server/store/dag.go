package store

// EdgesAcyclic reports whether the directed edge set is acyclic.
// Isolated self-loops are already rejected by WorkflowEdge.Validate;
// this catches A→B→A and longer cycles.
func EdgesAcyclic(edges []WorkflowEdge) error {
	nodes := map[string]struct{}{}
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, e := range edges {
		from := e.FromJobID.String()
		to := e.ToJobID.String()
		nodes[from] = struct{}{}
		nodes[to] = struct{}{}
		if _, ok := indeg[from]; !ok {
			indeg[from] = 0
		}
		if _, ok := indeg[to]; !ok {
			indeg[to] = 0
		}
		adj[from] = append(adj[from], to)
		indeg[to]++
	}
	var queue []string
	for id := range nodes {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	seen := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		seen++
		for _, nxt := range adj[id] {
			indeg[nxt]--
			if indeg[nxt] == 0 {
				queue = append(queue, nxt)
			}
		}
	}
	if seen != len(nodes) {
		return ErrDAGCycle
	}
	return nil
}
