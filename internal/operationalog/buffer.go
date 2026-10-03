package operationalog

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry contains only fields from a JSON log line after the logger has applied
// redaction. Sequence is local to a process and is used for bounded paging.
type Entry struct {
	Sequence     uint64    `json:"sequence"`
	Time         time.Time `json:"time"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	InstanceID   string    `json:"instanceId"`
	SessionID    string    `json:"sessionId"`
	Component    string    `json:"component"`
	Operation    string    `json:"operation"`
	RequestID    string    `json:"requestId"`
	TraceID      string    `json:"traceId"`
	Status       *int      `json:"status,omitempty"`
	DurationMS   *int64    `json:"durationMs,omitempty"`
	Method       string    `json:"method,omitempty"`
	RequestClass string    `json:"requestClass,omitempty"`
	JobID        string    `json:"jobId,omitempty"`
	Attempt      *int      `json:"attempt,omitempty"`
}

type Filter struct {
	From, To                     time.Time
	InstanceID, Level, Component string
	RequestID, TraceID, Keyword  string
	SessionID                    string
	RollingFrom, RollingTo       bool
	MaxWindow                    time.Duration
	Before                       uint64
	After                        uint64
	Forward                      bool
	Limit                        int
}

type Page struct {
	Items                      []Entry `json:"items"`
	Next                       uint64  `json:"nextSequence,omitempty"`
	Earliest, Latest, Position uint64
	Gap, HasMore               bool
	InvalidWindow              bool
	Capacity                   int
	Components                 []string
	ComponentsTruncated        bool
}

// Buffer is a process-local, memory-bounded view of redacted operational logs.
// It is intentionally not a durable or cluster-wide log backend.
type Buffer struct {
	mu        sync.RWMutex
	entries   []Entry
	search    []string
	count     int
	next      uint64
	partial   []byte
	discard   bool
	cursorKey [32]byte
}

func NewBuffer(lines int) *Buffer {
	if lines < 1 {
		return nil
	}
	b := &Buffer{entries: make([]Entry, lines), search: make([]string, lines)}
	_, _ = rand.Read(b.cursorKey[:])
	return b
}

// Write receives already-redacted JSON lines from slog's output writer.
func (b *Buffer) Write(p []byte) (int, error) {
	if b == nil {
		return len(p), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := p
	for len(remaining) > 0 {
		newline := -1
		for i, c := range remaining {
			if c == '\n' {
				newline = i
				break
			}
		}
		if newline < 0 {
			if !b.discard && len(b.partial)+len(remaining) <= 16*1024 {
				b.partial = append(b.partial, remaining...)
			} else {
				b.partial = nil
				// Keep discarding this line across writes until its newline.
				b.discard = true
			}
			break
		}
		if !b.discard && len(b.partial)+newline <= 16*1024 {
			b.partial = append(b.partial, remaining[:newline]...)
			b.appendLine(b.partial)
		}
		b.partial = nil
		b.discard = false
		remaining = remaining[newline+1:]
	}
	return len(p), nil
}

func (b *Buffer) appendLine(line []byte) {
	var value struct {
		Time                                                     time.Time `json:"time"`
		Level                                                    string    `json:"level"`
		Message                                                  string    `json:"msg"`
		InstanceID                                               string    `json:"instanceId"`
		SessionID                                                string    `json:"sessionId"`
		Component                                                string    `json:"component"`
		Operation                                                string    `json:"operation"`
		RequestID                                                string    `json:"requestId"`
		TraceID                                                  string    `json:"traceId"`
		Status, DurationMS, Method, RequestClass, JobID, Attempt json.RawMessage
	}
	if json.Unmarshal(line, &value) != nil || value.Time.IsZero() || value.InstanceID == "" {
		return
	}
	b.next++
	index := int((b.next - 1) % uint64(len(b.entries)))
	entry := Entry{Sequence: b.next, Time: value.Time, Level: value.Level, Message: value.Message,
		InstanceID: value.InstanceID, SessionID: value.SessionID, Component: value.Component, Operation: value.Operation,
		RequestID: value.RequestID, TraceID: value.TraceID}
	entry.Status = boundedInt(value.Status, 100, 599)
	entry.Attempt = boundedInt(value.Attempt, 0, 1000000)
	if number := boundedInt(value.DurationMS, 0, 86400000); number != nil {
		duration := int64(*number)
		entry.DurationMS = &duration
	}
	entry.Method = allowedString(value.Method, "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE")
	entry.RequestClass = allowedString(value.RequestClass, "management", "oci", "maven", "raw", "conan", "npm", "pypi", "go", "cargo", "health", "metrics", "other")
	var jobID string
	if json.Unmarshal(value.JobID, &jobID) == nil && validOpaqueID(jobID) {
		entry.JobID = jobID
	}
	b.entries[index] = entry
	b.search[index] = string(line)
	if b.count < len(b.entries) {
		b.count++
	}
}

func (b *Buffer) Query(ctx context.Context, filter Filter) Page {
	page := Page{Items: make([]Entry, 0, filter.Limit)}
	if b == nil || filter.Limit < 1 {
		return page
	}
	if !b.readLock(ctx) {
		return page
	}
	defer b.mu.RUnlock()
	// Rolling bounds belong to this locked snapshot, not HTTP parsing time.
	snapshotTime := time.Now().UTC()
	if filter.RollingTo {
		filter.To = snapshotTime
	}
	if filter.RollingFrom {
		filter.From = snapshotTime.Add(-time.Hour)
	}
	if filter.MaxWindow > 0 && (filter.From.After(filter.To) || filter.To.Sub(filter.From) > filter.MaxWindow) {
		page.InvalidWindow = true
		return page
	}
	page.Capacity = len(b.entries)
	page.Latest = b.next
	page.Position = b.next
	if b.count > 0 {
		page.Earliest = b.next - uint64(b.count) + 1
	}
	page.Gap = filter.Forward && page.Earliest > 0 && filter.After < page.Earliest-1
	components := make(map[string]bool)
	for i := 0; i < b.count; i++ {
		entry := b.entries[int((b.next-uint64(i)-1)%uint64(len(b.entries)))]
		if (filter.InstanceID == "" || entry.InstanceID == filter.InstanceID) && (filter.SessionID == "" || entry.SessionID == filter.SessionID) && validComponent(entry.Component) {
			components[entry.Component] = true
		}
	}
	for component := range components {
		page.Components = append(page.Components, component)
	}
	sort.Strings(page.Components)
	if len(page.Components) > 100 {
		page.Components = page.Components[:100]
		page.ComponentsTruncated = true
	}
	if filter.Forward {
		page.Position = filter.After
	}
	for i := 0; i < b.count; i++ {
		if ctx.Err() != nil {
			break
		}
		sequence := b.next - uint64(i)
		if filter.Forward {
			sequence = page.Earliest + uint64(i)
			if sequence <= filter.After {
				continue
			}
		}
		if filter.Before != 0 && sequence >= filter.Before {
			continue
		}
		index := int((sequence - 1) % uint64(len(b.entries)))
		entry := b.entries[index]
		excluded := entry.Time.Before(filter.From) || entry.Time.After(filter.To) ||
			(filter.InstanceID != "" && filter.InstanceID != entry.InstanceID) ||
			(filter.SessionID != "" && filter.SessionID != entry.SessionID) ||
			(filter.Level != "" && !strings.EqualFold(filter.Level, entry.Level)) ||
			(filter.Component != "" && filter.Component != entry.Component) ||
			(filter.RequestID != "" && filter.RequestID != entry.RequestID) ||
			(filter.TraceID != "" && filter.TraceID != entry.TraceID) ||
			(filter.Keyword != "" && !strings.Contains(strings.ToLower(b.search[index]), strings.ToLower(filter.Keyword)))
		if excluded {
			if filter.Forward {
				page.Position = sequence
			}
			continue
		}
		if len(page.Items) == filter.Limit {
			if filter.Forward {
				page.HasMore = true
			} else {
				page.Next = page.Items[len(page.Items)-1].Sequence
			}
			break
		}
		page.Items = append(page.Items, cloneEntry(entry))
		if filter.Forward {
			page.Position = sequence
		}
	}
	return page
}

func cloneEntry(entry Entry) Entry {
	if entry.Status != nil {
		value := *entry.Status
		entry.Status = &value
	}
	if entry.DurationMS != nil {
		value := *entry.DurationMS
		entry.DurationMS = &value
	}
	if entry.Attempt != nil {
		value := *entry.Attempt
		entry.Attempt = &value
	}
	return entry
}

// A contended writer must not make a two-second query budget wait indefinitely.
// TryRLock avoids a helper goroutine that could outlive a canceled request.
func (b *Buffer) readLock(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if b.mu.TryRLock() {
		return true
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if b.mu.TryRLock() {
				return true
			}
		}
	}
}

func boundedInt(raw json.RawMessage, minimum, maximum int) *int {
	var n int
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &n) != nil || n < minimum || n > maximum {
		return nil
	}
	return &n
}

func allowedString(raw json.RawMessage, values ...string) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, allowed := range values {
		if value == allowed {
			return value
		}
	}
	return ""
}

func validOpaqueID(value string) bool {
	if value == "" || len(value) > 128 || value == "[REDACTED]" {
		return false
	}
	for _, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

func validComponent(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for _, c := range value {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
