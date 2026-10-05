package tui

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"time"
)

func openURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("Invalid browser URL")
	}
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "open"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, program, raw).Run()
}
