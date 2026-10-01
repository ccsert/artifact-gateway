package operationalog

import (
	"errors"
	"io"
	"sync"
)

// Output serializes writes with explicit flush and close callbacks. Nil
// callbacks leave the underlying writer borrowed: its methods are not detected
// or invoked automatically, and Close is a no-op. Callbacks must not call back
// into this Output.
// Operations are synchronous; Output does not interrupt a blocked destination.
type Output struct {
	mu       sync.Mutex
	writer   io.Writer
	flush    func() error
	close    func() error
	closed   bool
	closeErr error
}

// NewOutput wraps a writer without changing its write results or taking implicit
// ownership. Cleanup is performed only through the supplied callbacks.
func NewOutput(writer io.Writer, flush, close func() error) *Output {
	return &Output{writer: writer, flush: flush, close: close}
}

func (o *Output) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return 0, io.ErrClosedPipe
	}
	return o.writer.Write(data)
}

// Flush waits for an active write before invoking the flush callback.
func (o *Output) Flush() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return io.ErrClosedPipe
	}
	if o.flush != nil {
		return o.flush()
	}
	return nil
}

// Close is a no-op for a borrowed output with no callbacks. Otherwise it waits
// for an active write, flushes, and closes once. Close is attempted even when
// flush fails; all callers receive the same combined cleanup result. Further
// Write and Flush calls then return io.ErrClosedPipe.
func (o *Output) Close() error {
	if o.flush == nil && o.close == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.closeErr
	}
	o.closed = true
	var flushErr, closeErr error
	if o.flush != nil {
		flushErr = o.flush()
	}
	if o.close != nil {
		closeErr = o.close()
	}
	o.closeErr = errors.Join(flushErr, closeErr)
	return o.closeErr
}
