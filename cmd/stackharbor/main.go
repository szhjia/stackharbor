package main

import (
	"context"
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/cli"
	"github.com/szhjia/stackharbor/internal/runner"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__engine_export_environment" {
		output := os.NewFile(3, "engine-environment")
		if output == nil {
			os.Exit(125)
		}
		err := json.NewEncoder(output).Encode(os.Environ())
		output.Close()
		if err != nil {
			os.Exit(125)
		}
		os.Exit(0)
	}
	if handled, code := runner.RunExecGate(os.Args[1:]); handled {
		os.Exit(code)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
