//go:build linux

package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func processStat(start string) string {
	return "123 (a name with ) parentheses) S " + strings.Repeat("0 ", 18) + start + " 0 0"
}

func TestLinuxBootEpochSurvivesClockCorrectionsAcrossProcesses(t *testing.T) {
	if root := os.Getenv("SH_EPOCH_PROC"); root != "" {
		value, err := linuxBootEpoch(context.Background(), root, os.Getenv("SH_EPOCH_DIR"))
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Print(value)
		os.Exit(0)
	}
	root, clock := t.TempDir(), filepath.Join(t.TempDir(), "clock")
	boot := filepath.Join(root, "sys/kernel/random")
	if err := os.MkdirAll(boot, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(boot, "boot_id"), "01234567-89ab-cdef-0123-456789abcdef\n")
	write(filepath.Join(root, "stat"), "btime 1700000000\n")
	// Simultaneous first callers must publish one complete epoch.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			value, err := linuxBootEpoch(context.Background(), root, clock)
			if err != nil || value != 1700000000 {
				t.Errorf("concurrent epoch %d: %v", value, err)
			}
		})
	}
	wg.Wait()
	for _, shifted := range []string{"btime 1699999999\n", "btime 1700000001\n"} {
		write(filepath.Join(root, "stat"), shifted)
		cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxBootEpochSurvivesClockCorrectionsAcrossProcesses$")
		cmd.Env = append(os.Environ(), "SH_EPOCH_PROC="+root, "SH_EPOCH_DIR="+clock)
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "1700000000" {
			t.Fatalf("clock correction changed identity: %q %v", out, err)
		}
	}
	// A different boot must not reuse a previous boot's time anchor.
	write(filepath.Join(boot, "boot_id"), "11234567-89ab-cdef-0123-456789abcdef\n")
	if value, err := linuxBootEpoch(context.Background(), root, clock); err != nil || value != 1700000001 {
		t.Fatalf("new boot reused old epoch: %d %v", value, err)
	}
	if err := os.Chmod(clock, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := linuxBootEpoch(context.Background(), root, clock); err == nil {
		t.Fatal("writable clock directory accepted")
	}
	if err := os.Chmod(clock, 0700); err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(clock, "11234567-89ab-cdef-0123-456789abcdef")
	if err := os.Chmod(anchor, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := linuxBootEpoch(context.Background(), root, clock); err == nil {
		t.Fatal("nonprivate clock file accepted")
	}
	if err := os.Chmod(anchor, 0600); err != nil {
		t.Fatal(err)
	}
	write(anchor, "unavailable")
	if _, err := linuxBootEpoch(context.Background(), root, clock); err == nil {
		t.Fatal("corrupt clock file accepted")
	}
	if err := os.Remove(anchor); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "stat"), anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := linuxBootEpoch(context.Background(), root, clock); err == nil {
		t.Fatal("symlink clock file accepted")
	}
}

func TestLinuxBirthIdentityUsesKernelEpochAndTicks(t *testing.T) {
	value, err := linuxCreatedMillis(processStat("12345"), "cpu 1 2 3\nbtime 1700000000\n", 100)
	if err != nil || value != 1700000123450 {
		t.Fatalf("%d %v", value, err)
	}
	for _, tc := range []struct {
		stat, system string
		ticks        uint64
	}{
		{"bad", "btime 1700000000", 100}, {"1 (x) S", "btime 1700000000", 100},
		{processStat("bad"), "btime 1700000000", 100}, {processStat("12345"), "btime -1", 100},
		{processStat("12345"), "cpu 1 2 3", 100}, {processStat("12345"), "btime 1700000000", 0},
	} {
		if _, err := linuxCreatedMillis(tc.stat, tc.system, tc.ticks); err == nil {
			t.Fatal("unverified birth accepted", tc)
		}
	}
}

func TestLinuxBirthIdentityStableAcrossProcesses(t *testing.T) {
	if pid := os.Getenv("SH_IDENTITY_PID"); pid != "" {
		n, _ := strconv.ParseInt(pid, 10, 32)
		value, err := CreatedMillis(context.Background(), int32(n))
		if err != nil {
			os.Exit(2)
		}
		fmt.Print(value)
		os.Exit(0)
	}
	pid := int32(os.Getpid())
	want, err := CreatedMillis(context.Background(), pid)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxBirthIdentityStableAcrossProcesses$")
		cmd.Env = append(os.Environ(), fmt.Sprintf("SH_IDENTITY_PID=%d", pid))
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != strconv.FormatInt(want, 10) {
			t.Fatalf("identity drift: want %d, got %s, %v", want, out, err)
		}
	}
}
