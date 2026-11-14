package cli

import (
	"context"
	"flag"
	"fmt"
	"github.com/szhjia/stackharbor/internal/engine"
	"github.com/szhjia/stackharbor/internal/web"
	"io"
)

func runWeb(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(errOut)
	port := fs.Int("port", 16800, "loopback port (0 selects an available port)")
	devOrigin := fs.String("dev-origin", "", "explicit exact 127.0.0.1 Vite development origin")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	help := fs.Bool("help", false, "help")
	fs.BoolVar(help, "h", false, "help")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprintln(out, "Usage: stackharbor web [--port 0..65535] [--no-open] [--dev-origin http://127.0.0.1:5173]")
		return 0
	}
	if fs.NArg() != 0 || *port < 0 || *port > 65535 {
		fmt.Fprintln(errOut, "web accepts --port 0..65535 and --no-open")
		return 2
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			explicit = true
		}
	})
	options := web.Options{Port: *port, PortExplicit: explicit, NoOpen: *noOpen, DevelopmentOrigin: *devOrigin, Out: out, ErrOut: errOut}
	if engine.ManagedEnabled() {
		namespace, err := web.ResolveNamespace("")
		if err != nil {
			return controlFailure(errOut, err)
		}
		client, err := engine.Ensure(ctx)
		if err != nil {
			return controlFailure(errOut, err)
		}
		defer client.Close()
		options.Namespace = namespace
		options.WorkspaceHandler = client.WorkspaceProxy(namespace)
	}
	if err := web.Run(ctx, options); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
