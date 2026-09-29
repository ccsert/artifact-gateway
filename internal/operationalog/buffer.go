package operationalog

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Entry contains only fields from a JSON log line after the logger has applied
// redaction. Sequence is local to a process and is used for bounded paging.
type Entry struct {
	Sequence   uint64    `json:"sequence"`
	Time       time.Time `json:"time"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	InstanceID string    `json:"instanceId"`
	SessionID  string    `json:"sessionId"`
	Component  string    `json:"component"`
	Operation  string    `json:"operation"`
	RequestID  string    `json:"requestId"`
	TraceID    string    `json:"traceId"`
}

type Filter struct {
	From, To                     time.Time
	InstanceID, Level, Component string
	RequestID, TraceID, Keyword  string
	Before                       uint64
	Limit                        int
}

type Page struct {
	Items []Entry `json:"items"`
	Next  uint64  `json:"nextSequence,omitempty"`
}

// Buffer is a process-local, memory-bounded view of redacted operational logs.
// It is intentionally not a durable or cluster-wide log backend.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	search  []string
	count   int
	next    uint64
	partial []byte
}

func NewBuffer(lines int) *Buffer {
	if lines < 1 {
		return nil
	}
	return &Buffer{entries: make([]Entry, lines), search: make([]string, lines)}
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
			if len(b.partial)+len(remaining) <= 16*1024 {
				b.partial = append(b.partial, remaining...)
			} else {
				b.partial = nil
			}
			break
		}
		if len(b.partial)+newline <= 16*1024 {
			b.partial = append(b.partial, remaining[:newline]...)
			b.appendLine(b.partial)
		}
		b.partial = nil
		remaining = remaining[newline+1:]
	}
	return len(p), nil
}

func (b *Buffer) appendLine(line []byte) {
	var value struct {
		Time       time.Time `json:"time"`
		Level      string    `json:"level"`
		Message    string    `json:"msg"`
		InstanceID string    `json:"instanceId"`
		SessionID  string    `json:"sessionId"`
		Component  string    `json:"component"`
		Operation  string    `json:"operation"`
		RequestID  string    `json:"requestId"`
		TraceID    string    `json:"traceId"`
	}
	if json.Unmarshal(line, &value) != nil || value.Time.IsZero() || value.InstanceID == "" {
		return
	}
	b.next++
	index := int((b.next - 1) % uint64(len(b.entries)))
	b.entries[index] = Entry{Sequence: b.next, Time: value.Time, Level: value.Level, Message: value.Message,
		InstanceID: value.InstanceID, SessionID: value.SessionID, Component: value.Component, Operation: value.Operation,
		RequestID: value.RequestID, TraceID: value.TraceID}
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
	b.mu.RLock()
	defer b.mu.RUnlock()
	for i := 0; i < b.count; i++ {
		if ctx.Err() != nil {
			break
		}
		sequence := b.next - uint64(i)
		if filter.Before != 0 && sequence >= filter.Before {
			continue
		}
		index := int((sequence - 1) % uint64(len(b.entries)))
		entry := b.entries[index]
		if entry.Time.Before(filter.From) || entry.Time.After(filter.To) ||
			(filter.InstanceID != "" && filter.InstanceID != entry.InstanceID) ||
			(filter.Level != "" && !strings.EqualFold(filter.Level, entry.Level)) ||
			(filter.Component != "" && filter.Component != entry.Component) ||
			(filter.RequestID != "" && filter.RequestID != entry.RequestID) ||
			(filter.TraceID != "" && filter.TraceID != entry.TraceID) ||
			(filter.Keyword != "" && !strings.Contains(strings.ToLower(b.search[index]), strings.ToLower(filter.Keyword))) {
			continue
		}
		if len(page.Items) == filter.Limit {
			page.Next = page.Items[len(page.Items)-1].Sequence
			break
		}
		page.Items = append(page.Items, entry)
	}
	return page
}
