package control

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestTerminalCloseJournalAndEngineOperationSharePublicationBoundary(t *testing.T) {
	journalObserved := make(chan Operation, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	c := coordinator(&testBackend{fingerprint: "x"}, Options{CommitClose: func(ctx context.Context, op Operation) error {
		if _, bounded := ctx.Deadline(); !bounded {
			return errors.New("unbounded close publication")
		}
		// The writer has made its terminal journal observable but has not yet
		// returned from durable publication (for example, directory fsync).
		journalObserved <- op
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	op := submit(t, c, plan(t, c, "close"), "close")
	var journal Operation
	select {
	case journal = <-journalObserved:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal publication never began")
	}
	// This deterministic lock probe proves engine-facing Operations cannot
	// read the old running record at the journal-publication boundary.
	if c.mu.TryLock() {
		c.mu.Unlock()
		t.Fatal("terminal journal visible while operation readers can still observe running")
	}
	readStarted := make(chan struct{})
	readResult := make(chan []Operation, 1)
	go func() { close(readStarted); readResult <- c.Operations() }()
	<-readStarted
	unblock()
	select {
	case operations := <-readResult:
		if len(operations) != 1 || operations[0].ID != op.ID || operations[0].State != "succeeded" || !reflect.DeepEqual(operations[0], journal) {
			t.Fatalf("journal and engine operation disagree: journal=%+v operations=%+v", journal, operations)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("engine operation query did not finish after publication")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseJournalCommitFailureCannotPublishSuccess(t *testing.T) {
	notified := make(chan Operation, 1)
	c := coordinator(&testBackend{fingerprint: "x"}, Options{CommitClose: func(ctx context.Context, op Operation) error { return errors.New("terminal journal publish failed") }, OnClosed: func(op Operation) { notified <- op }})
	op := submit(t, c, plan(t, c, "close"), "close")
	select {
	case final := <-notified:
		if final.ID != op.ID || final.State != "failed" || code(final.Error) != "close_finalization_failed" || len(final.Results) != 1 || final.Results[0].State != "failed" {
			t.Fatal(final)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close commit failure never completed")
	}
	if err := c.Close(context.Background()); code(err) != "close_finalization_failed" {
		t.Fatal(err)
	}
}
