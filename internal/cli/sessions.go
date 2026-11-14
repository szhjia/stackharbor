package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/szhjia/stackharbor/internal/supervisor"
)

func runSessions(ctx context.Context, root string, jsonMode bool, focus string, in io.Reader, out, errOut io.Writer) int {
	if root != "" {
		var err error
		root, err = filepath.Abs(root)
		if err == nil {
			root, err = filepath.EvalSymlinks(root)
		}
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	}
	all, err := supervisor.ListSessions()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	sessions := []supervisor.SessionInfo{}
	for _, s := range all {
		if root == "" || s.Root == root {
			sessions = append(sessions, s)
		}
	}
	if focus != "" {
		pid, numeric := strconv.Atoi(focus)
		focusRoot := ""
		if filepath.IsAbs(focus) || strings.HasPrefix(focus, ".") {
			focusRoot, err = filepath.Abs(focus)
			if err == nil {
				focusRoot, err = filepath.EvalSymlinks(focusRoot)
			}
			if err != nil {
				return controlFailure(errOut, err)
			}
		}
		matches := []supervisor.SessionInfo{}
		for _, s := range sessions {
			if s.SessionID == focus || focusRoot != "" && s.Root == focusRoot {
				matches = append(matches, s)
			}
		}
		byPID := len(matches) == 0 && numeric == nil
		if byPID {
			for _, s := range sessions {
				if s.PID == pid {
					matches = append(matches, s)
				}
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(errOut, "No active StackHarbor session matches %q; run stackharbor sessions\n", focus)
			return 1
		}
		for _, s := range matches {
			if byPID && s.OwnerKind == "engine" {
				fmt.Fprintln(errOut, "The engine PID is shared; select a workspace root or session ID with --focus")
				return 2
			}
		}
		if len(matches) != 1 {
			fmt.Fprintln(errOut, "Session selection is ambiguous; use a workspace root or session ID")
			return 2
		}
		s := matches[0]
		if s.OwnerKind == "engine" {
			return attachSession(ctx, s, "", in, out, errOut)
		}
		if err := focusTerminal(ctx, s); err != nil {
			fmt.Fprintf(errOut, "Could not locate session %s (PID %d, TTY %q): %s\n", s.SessionID, s.PID, s.TTY, err)
			return 1
		}
		fmt.Fprintf(out, "Located existing session: %q (PID %d, TTY %q)\n", s.Root, s.PID, s.TTY)
		return 0
	}
	if jsonMode {
		if err := json.NewEncoder(out).Encode(sessions); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}
	if len(sessions) == 0 {
		fmt.Fprintln(out, "No active StackHarbor sessions.")
		return 0
	}
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "SESSION\tPID\tTTY\tTERMINAL\tSTARTED\tWORKSPACE")
	for _, s := range sessions {
		started := "—"
		if !s.StartedAt.IsZero() {
			started = s.StartedAt.Local().Format("2006-01-02 15:04:05")
		}
		pid := "—"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", displaySessionField(s.SessionID), pid, displaySessionField(s.TTY), displaySessionField(s.Terminal), started, displaySessionField(s.Root))
	}
	if err := table.Flush(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "Attach to a managed workspace: stackharbor sessions --focus SESSION_ID (or ROOT)")
	fmt.Fprintln(out, "Locate a legacy window: stackharbor sessions --focus PID (macOS Terminal)")
	return 0
}

func displaySessionField(value string) string {
	if value == "" {
		return "—"
	}
	// Quote paths and metadata so terminal controls and tabs cannot alter the table.
	return strconv.Quote(value)
}

func locateConflict(ctx context.Context, conflict *supervisor.SessionConflict, out, errOut io.Writer) int {
	fmt.Fprintln(errOut, conflict)
	s := conflict.Session
	if s.PID <= 0 {
		fmt.Fprintln(errOut, "The owner could not be identified; check the existing terminal windows.")
		return 1
	}
	if err := focusTerminal(ctx, s); err != nil {
		fmt.Fprintln(errOut, "Automatic window location unavailable:", err)
		return 1
	}
	fmt.Fprintf(out, "Located the existing session (PID %d, TTY %q).\n", s.PID, s.TTY)
	return 0
}

// The TTY is passed as an argument, never interpolated into AppleScript source.
const focusTerminalScript = `on run argv
    set targetTTY to item 1 of argv
    if not (application id "com.apple.Terminal" is running) then error "Terminal is not running"
    tell application id "com.apple.Terminal"
        repeat with candidateWindow in windows
            repeat with candidateTab in tabs of candidateWindow
                if tty of candidateTab is targetTTY then
                    set selected of candidateTab to true
                    set miniaturized of candidateWindow to false
                    set index of candidateWindow to 1
                    activate
                    return "located"
                end if
            end repeat
        end repeat
    end tell
    error "No Terminal tab matches the session TTY"
end run`

func focusTerminal(ctx context.Context, s supervisor.SessionInfo) error {
	if s.OwnerKind == "engine" {
		return fmt.Errorf("the engine has no owning terminal; use stackharbor attach --root %q", s.Root)
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("window location currently supports macOS Terminal; use the listed PID and TTY")
	}
	if s.Terminal != "" && s.Terminal != "Apple_Terminal" {
		return fmt.Errorf("window location currently supports macOS Terminal; session terminal is %q", s.Terminal)
	}
	if !strings.HasPrefix(s.TTY, "/dev/") {
		return fmt.Errorf("session has no observable terminal TTY")
	}
	if s.PID <= 0 || s.ProcessCreatedMillis <= 0 {
		return fmt.Errorf("session process identity could not be verified")
	}
	// Recheck the occupied lock and process identity before selecting any window.
	active, err := supervisor.ListSessions()
	if err != nil {
		return err
	}
	found := false
	for _, current := range active {
		if current.Root == s.Root && current.SessionID == s.SessionID && current.PID == s.PID && current.ProcessCreatedMillis == s.ProcessCreatedMillis && current.TTY == s.TTY {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("session ended or changed; refresh stackharbor sessions")
	}
	budget, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(budget, "osascript", "-e", focusTerminalScript, s.TTY).CombinedOutput()
	if err != nil {
		if budget.Err() != nil {
			return fmt.Errorf("Terminal automation timed out or was cancelled")
		}
		return fmt.Errorf("Terminal automation failed: %s", strconv.Quote(strings.TrimSpace(string(b))))
	}
	if strings.TrimSpace(string(b)) != "located" {
		return fmt.Errorf("Terminal did not confirm a matching tab")
	}
	return nil
}
