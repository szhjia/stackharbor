package inventory

import (
	"crypto/sha256"
	"fmt"
	"github.com/szhjia/stackharbor/internal/control"
	"sort"
	"time"
)

type NodeReference struct {
	Role      string `json:"role"`
	ID        string `json:"id"`
	State     string `json:"state"`
	Ownership string `json:"ownership"`
}
type Reference struct {
	Nodes       []NodeReference `json:"nodes"`
	SessionID   string          `json:"session_id"`
	WorkspaceID string          `json:"workspace_id"`
	NodeIDs     []string        `json:"node_ids"`
}
type Resource struct {
	ID                string         `json:"id"`
	Kind              string         `json:"kind"`
	IdentityKnown     bool           `json:"identity_known"`
	PID               int32          `json:"pid,omitempty"`
	CreatedUnixMillis *int64         `json:"created_unix_millis,omitempty"`
	ContainerID       string         `json:"container_id,omitempty"`
	EndpointIdentity  string         `json:"endpoint_identity,omitempty"`
	Metric            control.Metric `json:"metric"`
	References        []Reference    `json:"references"`
}

// Process RSS and Docker working-set bytes are different measures and remain separate.
type MetricTotal struct {
	Count            int        `json:"count"`
	KnownMemoryCount int        `json:"known_memory_count"`
	KnownCPUCount    int        `json:"known_cpu_count"`
	MemoryBytes      *uint64    `json:"memory_bytes"`
	CPUPercent       *float64   `json:"cpu_percent"`
	Partial          bool       `json:"partial"`
	SampledAt        *time.Time `json:"sampled_at"`
}
type Totals struct {
	Processes  MetricTotal `json:"processes"`
	Containers MetricTotal `json:"containers"`
}

func projectResources(sessions []Session) []Resource {
	resources := map[string]*Resource{}
	for index, s := range sessions {
		if s.Snapshot == nil {
			continue
		}
		ordinal := 0
		add := func(local string, r Resource) {
			ordinal++
			key := r.ID
			if !r.IdentityKnown {
				key = fmt.Sprintf("unknown:%d:%s:%d", index, r.Kind, ordinal)
				r.ID = key
			}
			if resources[key] == nil {
				copy := r
				copy.References = []Reference{}
				resources[key] = &copy
			} else if r.Metric.SampledAt != nil && (resources[key].Metric.SampledAt == nil || r.Metric.SampledAt.After(*resources[key].Metric.SampledAt)) {
				resources[key].Metric = r.Metric
			}
			ref := resourceReference(s, local)
			merged := false
			for i := range resources[key].References {
				existing := &resources[key].References[i]
				if existing.SessionID == ref.SessionID && existing.WorkspaceID == ref.WorkspaceID {
					for _, node := range ref.Nodes {
						found := false
						for _, old := range existing.Nodes {
							found = found || old.ID == node.ID
						}
						if !found {
							existing.Nodes = append(existing.Nodes, node)
							existing.NodeIDs = append(existing.NodeIDs, node.ID)
						}
					}
					merged = true
					break
				}
			}
			if !merged {
				resources[key].References = append(resources[key].References, ref)
			}
		}
		for _, p := range s.Snapshot.Processes {
			known := p.PID > 0 && p.CreatedUnixMillis != nil && *p.CreatedUnixMillis > 0
			key := fmt.Sprintf("process:%d", p.PID)
			if known {
				key += fmt.Sprintf(":%d", *p.CreatedUnixMillis)
			}
			add(p.ID, Resource{ID: key, Kind: "process", IdentityKnown: known, PID: p.PID, CreatedUnixMillis: p.CreatedUnixMillis, Metric: p.Metric})
		}
		for _, c := range s.Snapshot.Containers {
			known := c.ID != "" && c.EndpointIdentity != ""
			key := fmt.Sprintf("container:%x", sha256.Sum256([]byte(c.EndpointIdentity+"\x00"+c.ID)))
			add(c.ResourceRef, Resource{ID: key, Kind: "container", IdentityKnown: known, ContainerID: c.ID, EndpointIdentity: c.EndpointIdentity, Metric: c.Metric})
		}
	}
	out := []Resource{}
	for _, r := range resources {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func projectTotals(resources []Resource) Totals {
	out := Totals{}
	for _, r := range resources {
		t := &out.Processes
		if r.Kind == "container" {
			t = &out.Containers
		}
		t.Count++
		t.Partial = t.Partial || !r.IdentityKnown || r.Metric.Partial
		if r.Metric.Known && r.Metric.RSSBytes != nil {
			if t.MemoryBytes == nil {
				v := uint64(0)
				t.MemoryBytes = &v
			}
			*t.MemoryBytes += *r.Metric.RSSBytes
			t.KnownMemoryCount++
		} else {
			t.Partial = true
		}
		if r.Metric.CPUPercent != nil {
			if t.CPUPercent == nil {
				v := 0.0
				t.CPUPercent = &v
			}
			*t.CPUPercent += *r.Metric.CPUPercent
			t.KnownCPUCount++
		} else {
			t.Partial = true
		}
		if r.Metric.SampledAt != nil && (t.SampledAt == nil || r.Metric.SampledAt.Before(*t.SampledAt)) {
			v := *r.Metric.SampledAt
			t.SampledAt = &v
		}
	}
	return out
}

// Consumers are derived exclusively from declared edges in this session snapshot.
func resourceReference(s Session, local string) Reference {
	ref := Reference{SessionID: s.Identity.SessionID, WorkspaceID: s.Identity.WorkspaceID, NodeIDs: []string{}, Nodes: []NodeReference{}}
	owners := map[string]bool{}
	nodes := map[string]control.Node{}
	for _, node := range s.Snapshot.Nodes {
		nodes[node.ID] = node
		for _, resource := range node.ResourceRefs {
			if resource == local {
				owners[node.ID] = true
			}
		}
	}
	var consumes func(string, map[string]bool) bool
	consumes = func(id string, seen map[string]bool) bool {
		if owners[id] {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, dep := range nodes[id].DependsOn {
			if consumes(dep, seen) {
				return true
			}
		}
		return false
	}
	for _, node := range s.Snapshot.Nodes {
		if consumes(node.ID, map[string]bool{}) {
			role := "consumer"
			if owners[node.ID] {
				role = "owner"
			}
			ref.NodeIDs = append(ref.NodeIDs, node.ID)
			ref.Nodes = append(ref.Nodes, NodeReference{ID: node.ID, State: node.State, Ownership: node.Ownership, Role: role})
		}
	}
	sort.Strings(ref.NodeIDs)
	sort.Slice(ref.Nodes, func(i, j int) bool { return ref.Nodes[i].ID < ref.Nodes[j].ID })
	return ref
}

// Project derives physical resources and totals from presentation sessions.
// Retained unreachable/stale snapshots remain visible with partial provenance.
func Project(out Inventory) Inventory {
	// Collection-level failure remains visible even with zero session rows.
	out.Partial = out.Partial || out.CollectionError != nil
	out.Resources = projectResources(out.Sessions)
	for _, s := range out.Sessions {
		if !s.Available || s.Stale {
			out.Partial = true
		}
	}
	for i := range out.Resources {
		for _, ref := range out.Resources[i].References {
			for _, s := range out.Sessions {
				if ref.SessionID == s.Identity.SessionID && (!s.Available || s.Stale) {
					out.Resources[i].Metric.Partial = true
				}
			}
		}
	}
	out.Totals = projectTotals(out.Resources)
	out.Totals.Processes.Partial = out.Totals.Processes.Partial || out.Partial
	out.Totals.Containers.Partial = out.Totals.Containers.Partial || out.Partial
	out.Partial = out.Partial || out.Totals.Processes.Partial || out.Totals.Containers.Partial
	return out
}
