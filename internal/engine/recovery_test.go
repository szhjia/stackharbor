package engine

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/szhjia/stackharbor/internal/control"
	"github.com/szhjia/stackharbor/internal/model"
)

func interruptedMutation(t *testing.T, launching bool) (*Server, string, string, string) {
	t.Helper()
	s := testServer(t)
	root, ns := registered(t), privateTemp(t)
	key := workspaceKey(ns, root)
	s.state.entries[key] = &workspaceState{Record: WorkspaceRecord{Root: root, Namespace: ns, State: "offline"}, Attempts: map[string]attempt{}}
	journal := workspaceJournal{s.state, key}
	if err := journal.BeforeSubmit(control.PlanRequest{Action: "all-start", Targets: []string{"demo/service/api"}}); err != nil {
		t.Fatal(err)
	}
	if launching {
		if _, err := journal.Intent(model.Service{ID: "demo/service/api"}); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := readState(s.state.directory)
	if err != nil {
		t.Fatal(err)
	}
	s.state = loaded
	if !loaded.entries[key].RecoveryRequired {
		t.Fatal("interrupted operation did not require recovery")
	}
	return s, root, ns, key
}

func TestRecoveryAcknowledgementClearsDurableIntent(t *testing.T) {
	for _, launching := range []bool{false, true} {
		t.Run(fmt.Sprintf("launching=%t", launching), func(t *testing.T) {
			s, root, ns, key := interruptedMutation(t, launching)
			r := httptest.NewRequest("GET", "/engine/v1/recovery", nil)
			plan, err := s.recoveryPlan(r, root, ns)
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"root":%q,"namespace":%q,"action":"acknowledge","plan_id":%q,"reviewed_unknown":true}`, root, ns, plan.PlanID)
			if _, err := s.recovery(httptest.NewRecorder(), httptest.NewRequest("POST", "/engine/v1/recovery", strings.NewReader(body))); err != nil {
				t.Fatal(err)
			}
			loaded, err := readState(s.state.directory)
			if err != nil {
				t.Fatal(err)
			}
			entry := loaded.entries[key]
			if entry.Mutation || entry.MutationAction != "" || len(entry.MutationTargets) != 0 || len(entry.Attempts) != 0 || entry.RecoveryRequired || entry.Record.State != "offline" || len(entry.Record.Issues) != 0 {
				t.Fatalf("acknowledged recovery retained durable intent: %+v", entry)
			}
		})
	}
}

func TestRecoveryRejectedAcknowledgementPreservesIntent(t *testing.T) {
	for _, reason := range []string{"unreviewed", "stale_plan", "active_session"} {
		t.Run(reason, func(t *testing.T) {
			s, root, ns, key := interruptedMutation(t, true)
			plan, err := s.recoveryPlan(httptest.NewRequest("GET", "/engine/v1/recovery", nil), root, ns)
			if err != nil {
				t.Fatal(err)
			}
			if reason == "stale_plan" {
				plan.PlanID = "stale"
			}
			if reason == "active_session" {
				s.hosts[key] = &hosted{}
				defer delete(s.hosts, key)
			}
			body := fmt.Sprintf(`{"root":%q,"namespace":%q,"action":"acknowledge","plan_id":%q,"reviewed_unknown":%t}`, root, ns, plan.PlanID, reason != "unreviewed")
			if _, err := s.recovery(httptest.NewRecorder(), httptest.NewRequest("POST", "/engine/v1/recovery", strings.NewReader(body))); err == nil {
				t.Fatal("unsafe acknowledgment accepted")
			}
			loaded, err := readState(s.state.directory)
			if err != nil {
				t.Fatal(err)
			}
			entry := loaded.entries[key]
			if !entry.Mutation || entry.MutationAction != "all-start" || len(entry.MutationTargets) != 1 || len(entry.Attempts) != 1 || !entry.RecoveryRequired {
				t.Fatalf("rejected recovery erased intent: %+v", entry)
			}
		})
	}
}
