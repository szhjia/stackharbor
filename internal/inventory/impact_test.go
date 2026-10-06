package inventory

import (
	"github.com/szhjia/stackharbor/internal/control"
	"testing"
)

func TestSharedImpactPolicy(t *testing.T) {
	inv := Inventory{Partial: true, Sessions: []Session{{Identity: control.Identity{SessionID: "other", WorkspaceID: "w2"}, Root: "/other", Available: true}}, Resources: []Resource{{ID: "physical", References: []Reference{{SessionID: "self", Nodes: []NodeReference{{ID: "db", Role: "owner"}}}, {SessionID: "other", WorkspaceID: "w2", Nodes: []NodeReference{{ID: "app", Role: "consumer", Ownership: "session", State: "running"}, {ID: "db", Role: "owner", Ownership: "session", State: "available"}}}}}}}
	impact := SharedImpact(inv, "self", "stop", []string{"db"})
	if !impact.Partial || len(impact.Blockers) != 1 || impact.Blockers[0].Root != "/other" || len(impact.Resources) != 1 {
		t.Fatalf("%+v", impact)
	}
	for _, action := range []string{"start", "release", "close"} {
		if len(SharedImpact(inv, "self", action, []string{"db"}).Blockers) != 0 {
			t.Fatal(action)
		}
	}
	inv.Sessions[0].Available = false
	if len(SharedImpact(inv, "self", "restart", []string{"db"}).Blockers) != 0 {
		t.Fatal("unreachable claimed known")
	}
	inv.Sessions[0].Available = true
	inv.Resources[0].References[1].Nodes[0].Ownership = "observed"
	if len(SharedImpact(inv, "self", "stop", []string{"db"}).Blockers) != 0 {
		t.Fatal("observed consumer blocked")
	}
	inv.Resources[0].References[1].Nodes[0].Ownership = "session"
	inv.Resources[0].References[1].Nodes[0].State = "stopped"
	if len(SharedImpact(inv, "self", "stop", []string{"db"}).Blockers) != 0 {
		t.Fatal("stopped consumer blocked")
	}
}

func TestStoppingConsumerDoesNotClaimResourceMutation(t *testing.T) {
	inv := Inventory{Resources: []Resource{{ID: "db", IdentityKnown: true, References: []Reference{{SessionID: "self", Nodes: []NodeReference{{ID: "db", Role: "owner"}, {ID: "api", Role: "consumer"}}}}}}}
	if impact := SharedImpact(inv, "self", "stop", []string{"api"}); len(impact.Resources) != 0 {
		t.Fatalf("consumer stop does not stop its dependency: %+v", impact)
	}
}
