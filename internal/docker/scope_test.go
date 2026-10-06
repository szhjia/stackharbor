package docker

import (
	"reflect"
	"testing"
)

func TestComposeScopeArgs(t *testing.T) {
	s := ComposeScope{Files: []string{"/x/base file.yml", "/x/local.yml"}, ProjectDirectory: "/x", EnvFiles: []string{"/x/one.env", "/x/two.env"}, Project: "demo"}
	want := []string{"compose", "-f", "/x/base file.yml", "-f", "/x/local.yml", "--project-directory", "/x", "--env-file", "/x/one.env", "--env-file", "/x/two.env", "-p", "demo"}
	if !reflect.DeepEqual(s.Args(), want) {
		t.Fatalf("args: %v", s.Args())
	}
	b := s
	b.Files = []string{"/x/local.yml", "/x/base file.yml"}
	if b.Key() == s.Key() {
		t.Fatal("order lost")
	}
}
