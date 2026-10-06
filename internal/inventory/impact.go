package inventory

import (
	"sort"
	"strings"
)

type ImpactConsumer struct {
	SessionID   string   `json:"session_id"`
	WorkspaceID string   `json:"workspace_id"`
	Root        string   `json:"root"`
	NodeIDs     []string `json:"node_ids"`
}
type Impact struct {
	Resources []Resource       `json:"resources"`
	Blockers  []ImpactConsumer `json:"blockers"`
	Partial   bool             `json:"partial"`
}

// SharedImpact describes physical resources touched by the approved scope.
// Only reachable, active, session-owned consumers in another session block stop
// or restart. Partial coverage is explicit, never evidence of no consumers.
func SharedImpact(inv Inventory, sessionID, action string, affected []string) Impact {
	out := Impact{Resources: []Resource{}, Blockers: []ImpactConsumer{}, Partial: inv.Partial}
	selected := map[string]bool{}
	for _, id := range affected {
		selected[id] = true
	}
	sessions := map[string]Session{}
	for _, s := range inv.Sessions {
		sessions[s.Identity.SessionID] = s
		if !s.Available {
			out.Partial = true
		}
	}
	blockers := map[string]*ImpactConsumer{}
	enforce := action == "stop" || action == "restart" || strings.HasSuffix(action, "-stop") || strings.HasSuffix(action, "-restart")
	for _, r := range inv.Resources {
		touched := false
		for _, ref := range r.References {
			if ref.SessionID == sessionID {
				for _, n := range ref.Nodes {
					if selected[n.ID] && n.Role == "owner" {
						touched = true
					}
				}
			}
		}
		if !touched {
			continue
		}
		out.Resources = append(out.Resources, r)
		out.Partial = out.Partial || !r.IdentityKnown
		if !enforce {
			continue
		}
		for _, ref := range r.References {
			s, ok := sessions[ref.SessionID]
			if ref.SessionID == sessionID || !ok || !s.Available {
				continue
			}
			for _, n := range ref.Nodes {
				if n.Role != "consumer" || n.Ownership != "session" || !activeConsumer(n.State) {
					continue
				}
				b := blockers[ref.SessionID]
				if b == nil {
					b = &ImpactConsumer{SessionID: ref.SessionID, WorkspaceID: ref.WorkspaceID, Root: s.Root, NodeIDs: []string{}}
					blockers[ref.SessionID] = b
				}
				found := false
				for _, id := range b.NodeIDs {
					found = found || id == n.ID
				}
				if !found {
					b.NodeIDs = append(b.NodeIDs, n.ID)
				}
			}
		}
	}
	for _, b := range blockers {
		sort.Strings(b.NodeIDs)
		out.Blockers = append(out.Blockers, *b)
	}
	sort.Slice(out.Blockers, func(i, j int) bool { return out.Blockers[i].SessionID < out.Blockers[j].SessionID })
	return out
}
func activeConsumer(state string) bool {
	switch state {
	case "waiting", "starting", "running", "started", "checking", "running-task", "verifying", "stopping", "unknown":
		return true
	}
	return false
}
