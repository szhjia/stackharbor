package runner

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

const execGateMode = "__engine_exec_gate"

// RunExecGate handles the internal child mode before normal CLI setup. FD3 is
// an anonymous parent-owned release pipe. EOF never authorizes execution.
func RunExecGate(args []string) (handled bool, code int) {
	if len(args) == 0 || args[0] != execGateMode {
		return false, 0
	}
	if len(args) < 3 {
		return true, 125
	}
	gate := os.NewFile(3, "engine-exec-release")
	if gate == nil {
		return true, 125
	}
	var release [1]byte
	_, err := io.ReadFull(gate, release[:])
	gate.Close()
	if err != nil || release[0] != 1 {
		return true, 125
	}
	if err := syscall.Exec(args[1], args[2:], os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "cannot execute gated service:", err)
		return true, 126
	}
	return true, 0
}
