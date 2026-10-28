package docker

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
)

func TestAdoptedContainerUsesPinnedIdentityAndNeverRunsCompose(t *testing.T) {
	ctx := context.Background()
	m := NewAdopted("/tmp", "wordverse", []string{"api"})
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	current := id
	commands := [][]string{}
	m.run = func(_ context.Context, args ...string) ([]byte, error) {
		commands = append(commands, append([]string{}, args...))
		switch args[0] {
		case "info":
			return []byte(`"daemon-one"`), nil
		case "ps":
			return []byte(current + " api\n"), nil
		case "inspect":
			return []byte(`[{"Id":"` + current + `","Name":"/wordverse-api-1","Config":{"Image":"app","Labels":{"com.docker.compose.project":"wordverse","com.docker.compose.service":"api"}},"State":{"Status":"running","Health":{"Status":"healthy"}}}]`), nil
		case "container":
			return nil, nil
		}
		return nil, errors.New("unexpected Docker command")
	}
	if err := m.Pin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Action(ctx, "restart", []string{"api"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, args := range commands {
		if args[0] == "compose" {
			t.Fatal("adopted container invoked Compose")
		}
		if slices.Equal(args, []string{"container", "restart", "--time", "15", id}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing exact-ID restart: %v", commands)
	}
	current = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	before := len(commands)
	if err := m.Action(ctx, "stop", []string{"api"}); err == nil {
		t.Fatal("replacement container accepted")
	}
	for _, args := range commands[before:] {
		if args[0] == "container" {
			t.Fatal("mutated replacement container")
		}
	}
}

func TestAdoptedContainerRefusesMissingOrAmbiguousIdentity(t *testing.T) {
	ctx := context.Background()
	m := NewAdopted("/tmp", "wordverse", []string{"api"})
	listing := ""
	m.run = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "info" {
			return []byte(`"daemon-one"`), nil
		}
		return []byte(listing), nil
	}
	if err := m.Pin(ctx); err == nil {
		t.Fatal("missing container accepted")
	}
	listing = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa api\nbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb api\n"
	if err := m.Pin(ctx); err == nil {
		t.Fatal("scaled service accepted")
	}
	listing = "not-a-container-id api\n"
	if err := m.Pin(ctx); err == nil {
		t.Fatal("invalid container ID accepted")
	}
}

func TestAdoptedContainerRefusesDaemonChangeBeforeMutation(t *testing.T) {
	m := NewAdopted(t.TempDir(), "fixture", []string{"api"})
	daemon := "daemon-one"
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writes := 0
	m.run = func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "info":
			return []byte(`"` + daemon + `"`), nil
		case "ps":
			return []byte(id + " api\n"), nil
		case "inspect":
			return []byte(`[{"Id":"` + id + `","Config":{"Labels":{"com.docker.compose.project":"fixture","com.docker.compose.service":"api"}},"State":{"Status":"running"}}]`), nil
		case "container":
			writes++
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	if err := m.Pin(context.Background()); err != nil {
		t.Fatal(err)
	}
	daemon = "daemon-two"
	for _, action := range []string{"start", "stop", "restart"} {
		if err := m.Action(context.Background(), action, []string{"api"}); err == nil {
			t.Fatalf("%s accepted another daemon", action)
		}
	}
	if _, err := m.Binding(context.Background(), []string{"api"}); err == nil || writes != 0 {
		t.Fatalf("changed daemon bound or mutated: writes=%d err=%v", writes, err)
	}
}

func TestAdoptedLiveObservation(t *testing.T) {
	project, service := os.Getenv("STACKHARBOR_TEST_ADOPT_PROJECT"), os.Getenv("STACKHARBOR_TEST_ADOPT_SERVICE")
	if project == "" || service == "" {
		t.Skip("set STACKHARBOR_TEST_ADOPT_PROJECT and STACKHARBOR_TEST_ADOPT_SERVICE for a read-only live check")
	}
	m := NewAdopted(t.TempDir(), project, []string{service})
	ctx := context.Background()
	if err := m.Pin(ctx); err != nil {
		t.Fatal(err)
	}
	rows, reason := m.Observe(ctx)
	if reason != "" || len(rows) != 1 || !fullContainerID.MatchString(rows[0].ID) {
		t.Fatalf("adopted observation failed: %v %v", rows, reason)
	}
	if _, err := m.Binding(ctx, []string{service}); err != nil {
		t.Fatal(err)
	}
}
