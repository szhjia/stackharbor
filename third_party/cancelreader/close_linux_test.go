//go:build linux

package cancelreader

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

type gatedEpollFile struct {
	File
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *gatedEpollFile) Fd() uintptr {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return f.File.Fd()
}

type observedEpollCloseFile struct {
	File
	closed chan struct{}
}

func (f *observedEpollCloseFile) Close() error {
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
	defer reader.Close()
	r := reader.(*epollCancelReader)
	f := &gatedEpollFile{File: in, entered: make(chan struct{}), release: make(chan struct{})}
	pipeClosed := make(chan struct{})
	r.file = f
	r.cancelSignalReader = &observedEpollCloseFile{File: r.cancelSignalReader, closed: pipeClosed}
	var release sync.Once
	defer release.Do(func() { close(f.release) })
	readDone := make(chan error, 1)
	go func() { _, err := r.Read(make([]byte, 1)); readDone <- err }()
	// Hold the reader in descriptor dispatch after the kernel wait returns.
	if _, err := out.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not reach descriptor dispatch")
	}
	const closers = 8
	closed := make(chan error, closers)
	for i := 0; i < closers; i++ {
		go func() { closed <- r.Close() }()
	}
	select {
	case <-pipeClosed:
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
	for i := 0; i < closers; i++ {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal("concurrent close:", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not complete")
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal("repeated close:", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, ErrCanceled) {
		t.Fatal("read after close:", err)
	}
}
