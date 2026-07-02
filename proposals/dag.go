package proposals

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateDAG checks the task list for duplicate IDs, unknown deps, and cycles.
// Returns an error describing the first problem encountered.
func ValidateDAG(tasks []Task) error {
	if len(tasks) == 0 {
		return fmt.Errorf("empty task list")
	}

	ids := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.ID == "" {
			return fmt.Errorf("task with empty ID")
		}
		if ids[t.ID] {
			return fmt.Errorf("duplicate task ID: %s", t.ID)
		}
		ids[t.ID] = true
	}

	// All deps must reference known task IDs; no self-deps.
	for _, t := range tasks {
		for _, d := range t.Deps {
			if d == t.ID {
				return fmt.Errorf("task %s depends on itself", t.ID)
			}
			if !ids[d] {
				return fmt.Errorf("task %s depends on unknown task %s", t.ID, d)
			}
		}
	}

	if cycle := findCycle(tasks); cycle != nil {
		return fmt.Errorf("cycle detected: %s", strings.Join(cycle, " -> "))
	}

	return nil
}

// findCycle returns a cycle path if one exists, or nil if the graph is acyclic.
// Uses Kahn's algorithm: nodes with zero unresolved in-degree are peeled off;
// any remaining nodes participate in a cycle.
func findCycle(tasks []Task) []string {
	inDegree := make(map[string]int, len(tasks))
	adj := make(map[string][]string, len(tasks))

	for _, t := range tasks {
		inDegree[t.ID] = len(t.Deps)
		for _, d := range t.Deps {
			adj[d] = append(adj[d], t.ID)
		}
	}

	// Initialize queue with zero-in-degree nodes (sorted for deterministic order).
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++

		next := adj[cur]
		sort.Strings(next)
		for _, n := range next {
			inDegree[n]--
			if inDegree[n] == 0 {
				queue = append(queue, n)
			}
		}
	}

	if visited == len(tasks) {
		return nil
	}

	// Remaining nodes (inDegree > 0) are in a cycle. Walk one to materialize it.
	var remaining []string
	for id, deg := range inDegree {
		if deg > 0 {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)

	// Build subgraph adjacency among remaining nodes, then walk forward until we loop.
	subAdj := make(map[string][]string)
	for _, id := range remaining {
		for _, t := range tasks {
			if t.ID != id {
				continue
			}
			for _, d := range t.Deps {
				if inDegree[d] > 0 {
					subAdj[id] = append(subAdj[id], d)
				}
			}
		}
	}

	// Start walking from remaining[0], following dep edges (toward what we depend on).
	return walkCycle(remaining[0], subAdj)
}

// walkCycle follows adjacency from `start` until it revisits a node, then
// returns the cycle slice (start...cycle-close).
func walkCycle(start string, adj map[string][]string) []string {
	order := []string{start}
	seen := map[string]int{start: 0}
	cur := start

	for {
		nexts := adj[cur]
		if len(nexts) == 0 {
			// No outgoing edge — shouldn't happen if a cycle truly exists, but
			// fall back to just returning the visited path.
			return order
		}
		next := nexts[0]
		if idx, ok := seen[next]; ok {
			// Cycle closes at `next`. Return the slice from idx to end + next.
			cycle := append([]string{}, order[idx:]...)
			cycle = append(cycle, next)
			return cycle
		}
		seen[next] = len(order)
		order = append(order, next)
		cur = next
	}
}

// ComputeReadyTasks returns pointers to tasks that are pending and whose deps
// are all approved. Order matches the input slice order.
func ComputeReadyTasks(tasks []Task) []*Task {
	approved := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.Status == TaskApproved {
			approved[t.ID] = true
		}
	}

	var ready []*Task
	for i := range tasks {
		if tasks[i].Status != TaskPending {
			continue
		}
		ok := true
		for _, d := range tasks[i].Deps {
			if !approved[d] {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, &tasks[i])
		}
	}
	return ready
}

// RecalculateAfterApproval returns the IDs of tasks that newly become ready
// after `approvedID` transitions to approved. A task newly becomes ready if
// it is still pending and now has all deps approved (some were not before).
func RecalculateAfterApproval(tasks []Task, approvedID string) []string {
	approved := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.Status == TaskApproved || t.ID == approvedID {
			approved[t.ID] = true
		}
	}

	var newly []string
	for _, t := range tasks {
		if t.Status != TaskPending {
			continue
		}
		allApproved := true
		for _, d := range t.Deps {
			if !approved[d] {
				allApproved = false
				break
			}
		}
		// Only include tasks that actually depend on approvedID (otherwise they
		// were already ready before this approval and would double-report).
		if allApproved && dependsOn(t, approvedID) {
			newly = append(newly, t.ID)
		}
	}
	return newly
}

func dependsOn(t Task, id string) bool {
	for _, d := range t.Deps {
		if d == id {
			return true
		}
	}
	return false
}

// FindTask returns a pointer to the task with the given ID, or nil.
func FindTask(tasks []Task, id string) *Task {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
}

// AllApproved reports whether every task is in an approved state.
func AllApproved(tasks []Task) bool {
	for _, t := range tasks {
		if t.Status != TaskApproved {
			return false
		}
	}
	return true
}

// HasUnrecoverable reports whether any task is failed or cancelled.
func HasUnrecoverable(tasks []Task) bool {
	for _, t := range tasks {
		if t.Status == TaskFailed || t.Status == TaskCancelled {
			return true
		}
	}
	return false
}

// ResetStaleRunning transitions any task left in "running" (e.g. after a crash)
// back to "ready" so the loop will pick it up again. Returns the IDs reset.
func ResetStaleRunning(tasks []Task) []string {
	var reset []string
	for i := range tasks {
		if tasks[i].Status == TaskRunning {
			tasks[i].Status = TaskReady
			reset = append(reset, tasks[i].ID)
		}
	}
	return reset
}
