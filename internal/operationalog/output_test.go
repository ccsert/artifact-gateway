package operationalog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type outputProbe struct {
	bytes.Buffer
	active     atomic.Int32
	overlapped atomic.Bool
	writes     atomic.Int32
	flushes    atomic.Int32
	closes     atomic.Int32
}

func (p *outputProbe) enter() func() {
	if p.active.Add(1) != 1 {
		p.overlapped.Store(true)
	}
	return func() { p.active.Add(-1) }
}

func (p *outputProbe) Write(data []byte) (int, error) {
	defer p.enter()()
	p.writes.Add(1)
	return p.Buffer.Write(data)
}

func (p *outputProbe) Flush() error {
	defer p.enter()()
	p.flushes.Add(1)
	return nil
}

func (p *outputProbe) Close() error {
	defer p.enter()()
	p.closes.Add(1)
	return nil
}

func TestOutputBorrowsWriterWithoutImplicitCleanup(t *testing.T) {
	probe := &outputProbe{}
	output := NewOutput(probe, nil, nil)
	if n, err := output.Write([]byte("synthetic-event\n")); err != nil || n != len("synthetic-event\n") {
		t.Fatalf("write = %d, %v", n, err)
	}
	if err := output.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if probe.flushes.Load() != 0 || probe.closes.Load() != 0 {
		t.Fatal("borrowed writer was implicitly flushed or closed")
	}
	if n, err := output.Write([]byte("borrowed-output-open\n")); n != len("borrowed-output-open\n") || err != nil {
		t.Fatalf("borrowed write after no-op close = %d, %v", n, err)
	}
	if err := output.Flush(); err != nil {
		t.Fatalf("borrowed flush after no-op close = %v", err)
	}
	// Both the wrapper and its borrowed destination remain usable.
	if _, err := probe.Write([]byte("borrowed-writer-open\n")); err != nil {
		t.Fatal(err)
	}
}

func TestOutputKeepsJSONAndLocalBufferBehavior(t *testing.T) {
	var expected, actual bytes.Buffer
	buffer := NewBuffer(1)
	output := NewOutput(io.MultiWriter(&actual, buffer), nil, nil)
	record := slog.NewRecord(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), slog.LevelInfo, "synthetic-runtime-event", 0)
	record.AddAttrs(slog.String("requestId", "synthetic-request"), slog.String("secretToken", "synthetic-hidden-value"))
	for _, writer := range []io.Writer{&expected, output} {
		if err := NewLogger(writer, "synthetic-node", "synthetic-session").Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Bytes(), expected.Bytes()) || strings.Contains(actual.String(), "synthetic-hidden-value") {
		t.Fatalf("managed JSON differs or leaks marker: %s", actual.String())
	}
	page := buffer.Query(context.Background(), Filter{From: record.Time.Add(-time.Second), To: record.Time.Add(time.Second), Limit: 1})
	if len(page.Items) != 1 || page.Items[0].Message != "synthetic-runtime-event" || page.Items[0].RequestID != "synthetic-request" || page.Items[0].SessionID != "synthetic-session" {
		t.Fatalf("local buffer = %#v", page)
	}
}

type outputWriterFunc func([]byte) (int, error)

func (f outputWriterFunc) Write(data []byte) (int, error) { return f(data) }

func TestOutputPreservesWriteErrorsAndPartialCounts(t *testing.T) {
	writeErr := errors.New("synthetic write failure")
	output := NewOutput(outputWriterFunc(func([]byte) (int, error) { return 3, writeErr }), nil, nil)
	if n, err := output.Write([]byte("synthetic")); n != 3 || !errors.Is(err, writeErr) {
		t.Fatalf("write = %d, %v", n, err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOutputFlushAndCloseErrorsAreReturnedAndCleanupRunsOnce(t *testing.T) {
	flushErr := errors.New("synthetic flush failure")
	closeErr := errors.New("synthetic close failure")
	var flushes, closes atomic.Int32
	output := NewOutput(io.Discard, func() error { flushes.Add(1); return flushErr }, func() error { closes.Add(1); return closeErr })
	if err := output.Flush(); !errors.Is(err, flushErr) {
		t.Fatalf("flush = %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Go(func() { errs <- output.Close() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, flushErr) || !errors.Is(err, closeErr) {
			t.Fatalf("close lost cleanup errors: %v", err)
		}
	}
	if flushes.Load() != 2 || closes.Load() != 1 {
		t.Fatalf("cleanup calls = flush %d, close %d", flushes.Load(), closes.Load())
	}
	if n, err := output.Write([]byte("after-close\n")); n != 0 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed owned write = %d, %v", n, err)
	}
	if err := output.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed owned flush = %v", err)
	}
}

func TestBorrowedOutputCloseDoesNotWaitForBlockedWriter(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	output := NewOutput(outputWriterFunc(func(data []byte) (int, error) {
		close(entered)
		<-release
		return len(data), nil
	}), nil, nil)
	written := make(chan error, 1)
	go func() { _, err := output.Write([]byte("synthetic-event\n")); written <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("write did not enter")
	}
	closed := make(chan error, 1)
	go func() { closed <- output.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("borrowed cleanup waited for a blocked writer")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write did not finish after release")
	}
}

func TestOutputSerializesConcurrentWritesAndFlushes(t *testing.T) {
	probe := &outputProbe{}
	output := NewOutput(probe, probe.Flush, probe.Close)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				if _, err := output.Write([]byte("synthetic-event\n")); err != nil {
					t.Error(err)
				}
				if err := output.Flush(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if probe.overlapped.Load() || probe.writes.Load() != 1600 || probe.flushes.Load() != 1601 || probe.closes.Load() != 1 || bytes.Count(probe.Bytes(), []byte("synthetic-event\n")) != 1600 {
		t.Fatalf("output operations overlapped or disappeared: writes=%d flushes=%d closes=%d", probe.writes.Load(), probe.flushes.Load(), probe.closes.Load())
	}
}

func TestOutputCleanupWaitsForInFlightWrite(t *testing.T) {
	for _, operation := range []string{"flush", "close"} {
		t.Run(operation, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			callbacks := make(chan string, 2)
			output := NewOutput(outputWriterFunc(func(data []byte) (int, error) {
				close(entered)
				<-release
				return len(data), nil
			}), func() error { callbacks <- "flush"; return nil }, func() error { callbacks <- "close"; return nil })
			written := make(chan error, 1)
			go func() { _, err := output.Write([]byte("synthetic-event\n")); written <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("write did not enter")
			}
			started, finished := make(chan struct{}), make(chan error, 1)
			go func() {
				close(started)
				if operation == "flush" {
					finished <- output.Flush()
				} else {
					finished <- output.Close()
				}
			}()
			<-started
			select {
			case callback := <-callbacks:
				t.Fatalf("%s overlapped an unfinished write", callback)
			case <-time.After(20 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			for _, result := range []<-chan error{written, finished} {
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("output operation did not finish")
				}
			}
			expectCallback := func(want string) {
				t.Helper()
				select {
				case callback := <-callbacks:
					if callback != want {
						t.Fatalf("callback = %s, want %s", callback, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("%s callback did not run", want)
				}
			}
			expectCallback("flush")
			if operation == "close" {
				expectCallback("close")
			}
		})
	}
}
