//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package cancelreader

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

type gatedCancelFile struct {
	File
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (f *gatedCancelFile) Fd() uintptr {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return f.File.Fd()
}

type observedCloseFile struct {
	File
	closed chan struct{}
}

func (f *observedCloseFile) Close() error {
	close(f.closed)
	return f.File.Close()
}

func TestCloseJoinsCanceledRead(t *testing.T) {
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer out.Close()
	reader, err := NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	r := reader.(*kqueueCancelReader)
	f := &gatedCancelFile{File: in, entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	r.file = f
	r.cancelSignalReader = &observedCloseFile{File: r.cancelSignalReader, closed: f.closed}
	var release sync.Once
	defer release.Do(func() { close(f.release) })
	readDone := make(chan error, 1)
	go func() { _, err := r.Read(make([]byte, 1)); readDone <- err }()
	// A byte wakes the kernel wait; the gated input descriptor marks the exact
	// dispatch boundary before Close can cancel and release the pipe.
	if _, err := out.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not reach descriptor dispatch")
	}
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	select {
	case <-f.closed:
		t.Error("Close released the cancellation pipe while Read was active")
	case <-time.After(50 * time.Millisecond):
	}
	release.Do(func() { close(f.release) })
	select {
	case err := <-readDone:
		if err != nil && !errors.Is(err, ErrCanceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not complete")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not complete")
	}
	if t.Failed() {
		return
	}
	if err := r.Close(); err != nil {
		t.Fatal("repeated close:", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrCanceled) {
		t.Fatal("read after close:", err)
	}
}
