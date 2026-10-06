package control

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestResourceLocksAcrossProcesses(t *testing.T) {
	if os.Getenv("SH_RESOURCE_LOCK_CHILD") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		_, err := AcquireResourceLocks(ctx, os.Getenv("SH_RESOURCE_LOCK_NAMESPACE"), []string{"same"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("child did not block: %v", err)
		}
		return
	}
	namespace := t.TempDir()
	release, err := AcquireResourceLocks(context.Background(), namespace, []string{"same", "same"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestResourceLocksAcrossProcesses$")
	cmd.Env = append(os.Environ(), "SH_RESOURCE_LOCK_CHILD=1", "SH_RESOURCE_LOCK_NAMESPACE="+namespace)
	if out, err := cmd.CombinedOutput(); err != nil {
		release()
		t.Fatalf("%s: %v", out, err)
	}
	release()
	release()
	next, err := AcquireResourceLocks(context.Background(), namespace, []string{"same"})
	if err != nil {
		t.Fatal(err)
	}
	next()
}
func TestScopedResourceLockBorrowSurvivesOwnerRelease(t *testing.T) {
	ns := t.TempDir()
	ctx, release, err := WithResourceLocks(context.Background(), ns, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	borrowed, done, err := WithResourceLocks(InheritResourceLocks(context.Background(), ctx), ns, []string{"key"})
	if err != nil || borrowed == nil {
		t.Fatal(err)
	}
	release()
	deadline, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = AcquireResourceLocks(deadline, ns, []string{"key"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("owner released borrowed lock", err)
	}
	done()
	next, err := AcquireResourceLocks(context.Background(), ns, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	next()
}

func TestBorrowedScopeSupportsNestedCallAfterOwnerReturns(t *testing.T) {
	ns := t.TempDir()
	owner, release, err := WithResourceLocks(context.Background(), ns, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	borrowed, done, err := WithResourceLocks(owner, ns, []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	release()
	bounded, cancel := context.WithTimeout(borrowed, 40*time.Millisecond)
	defer cancel()
	_, nested, err := WithResourceLocks(bounded, ns, []string{"key"})
	if err != nil {
		done()
		t.Fatal("live borrower could not nest after owner returned", err)
	}
	nested()
	done()
}
