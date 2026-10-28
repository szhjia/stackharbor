package model

import "testing"

func TestOnlyExplicitManagedResourcesJoinAllActions(t *testing.T) {
	no := false
	cases := []struct {
		service Service
		want    bool
	}{
		{Service{Kind: "service"}, true},
		{Service{Kind: "service", Control: "observe"}, false},
		{Service{Kind: "service", AllActions: &no}, false},
		{Service{Kind: "task"}, false},
		{Service{Kind: "resource", Resource: &ResourceSpec{AllActions: false}}, false},
		{Service{Kind: "resource", Resource: &ResourceSpec{AllActions: true}}, true},
	}
	for _, tc := range cases {
		if got := tc.service.InAllActions(); got != tc.want {
			t.Fatalf("%+v: got %v, want %v", tc.service, got, tc.want)
		}
	}
}
