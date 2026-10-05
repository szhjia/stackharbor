package config

import (
	"fmt"
	"testing"
)

func TestProtocolPreservesUserDefinedUnicodeNames(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := t.TempDir()
			path := put(t, root, "stackharbor.yaml", fmt.Sprintf(`version: %d
project: {id: app, name: 学习工程}
services:
  web:
    name: 网页服务
    run: {command: [echo, 用户文本]}
    ports: [{name: 本地网页, port: 5612}]
`, version))
			project, diagnostics := LoadProject(path, root)
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			if project.Name != "学习工程" || len(project.Services) != 1 {
				t.Fatal("user-defined project name was changed", project)
			}
			service := project.Services[0]
			if service.Name != "网页服务" || service.Command[1] != "用户文本" || service.Ports[0].Name != "本地网页" {
				t.Fatal("user-defined service text was changed", service)
			}
		})
	}
}
