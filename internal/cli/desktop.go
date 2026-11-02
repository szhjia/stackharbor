package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/szhjia/stackharbor/internal/desktop"
)

func runDesktop(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	fs.SetOutput(errOut)
	help := fs.Bool("help", false, "help")
	fs.BoolVar(help, "h", false, "help")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprintln(out, "Usage: stackharbor desktop\nOpen the installed desktop console; existing project sessions are preserved.")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errOut, "desktop accepts no positional arguments")
		return 2
	}
	if err := desktop.Launch(ctx, false); err != nil {
		fmt.Fprintln(errOut, "Desktop:", err)
		return 1
	}
	fmt.Fprintln(out, "Opening StackHarbor desktop.")
	return 0
}

func runDesktopBackend(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("desktop-backend", flag.ContinueOnError)
	fs.SetOutput(errOut)
	ns := fs.String("namespace", "", "registry namespace")
	version := fs.String("expected-version", "", "parent build version")
	protocol := fs.Int("protocol", 0, "desktop protocol")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *ns == "" || terminal(in) {
		fmt.Fprintln(errOut, "desktop-backend requires a namespace and a parent pipe")
		return 2
	}
	if err := desktop.RunBackend(ctx, desktop.BackendOptions{Namespace: *ns, ExpectedVersion: *version, ProtocolVersion: *protocol}, in, out); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
