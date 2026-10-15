package agctl

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	defaultMaxPages = 20
)

// serverPage is the cursor page shape the management API uses for
// repositories and Groups: items plus the token of the next page.
type serverPage[T any] struct {
	Items         []T     `json:"items"`
	NextPageToken *string `json:"nextPageToken"`
}

// cursorList reads a server-paginated collection: one page by default, a
// later page with --cursor, or every page with --all within --max-pages and
// one overall --timeout.
type cursorList[T any] struct {
	// path is the collection endpoint; command is the agctl command that
	// continues the listing (for the human hint), noun the plural it lists.
	// A command whose continuation depends on its arguments leaves command
	// empty and the hint asks to re-run it with --cursor.
	path    string
	command string
	noun    string
	render  func(io.Writer, []T) error

	// request, when set, builds the endpoint and its filters from the
	// command's arguments; it runs before any request, so its errors are
	// usage errors. scope, when set, extracts the page's context fields.
	request func(env *commandContext) (string, url.Values, error)
	scope   func(raw json.RawMessage) map[string]any

	pageSize int
	maxPages int
	cursor   string
	all      bool
}

func (l *cursorList[T]) setFlags(fs *flag.FlagSet) {
	fs.IntVar(&l.pageSize, "page-size", defaultPageSize, l.noun+" per page (1-200)")
	fs.StringVar(&l.cursor, "cursor", "", "continue a listing from the nextCursor of an earlier page")
	fs.BoolVar(&l.all, "all", false, "follow cursors until the end, within --max-pages and --timeout")
	fs.IntVar(&l.maxPages, "max-pages", defaultMaxPages, "page budget for --all; reaching it exits 3 with the pages read")
}

func (l *cursorList[T]) run(ctx context.Context, env *commandContext) error {
	if l.pageSize < 1 || l.pageSize > maxPageSize {
		return &UsageError{Message: fmt.Sprintf("--page-size %d is out of range", l.pageSize), Hint: "use a page size from 1 to 200"}
	}
	if l.maxPages < 1 {
		return &UsageError{Message: fmt.Sprintf("--max-pages %d must be positive", l.maxPages), Hint: "pass --max-pages with a page budget such as 20"}
	}
	if l.all && l.cursor != "" {
		return &UsageError{Message: "--all and --cursor cannot be combined", Hint: "--all starts from the first page; use --cursor alone to read one later page"}
	}
	path, filters := l.path, url.Values{}
	if l.request != nil {
		var err error
		if path, filters, err = l.request(env); err != nil {
			return err
		}
	}
	client, err := env.client()
	if err != nil {
		return err
	}
	// --all spends one overall time budget across its pages.
	if l.all {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, env.globals.timeout)
		defer cancel()
	}
	items := []T{}
	next := l.cursor
	pages := 0
	var scope map[string]any
	for {
		query := url.Values{}
		for key, values := range filters {
			query[key] = values
		}
		query.Set("pageSize", strconv.Itoa(l.pageSize))
		if next != "" {
			query.Set("pageToken", next)
		}
		var page serverPage[T]
		err := l.read(ctx, client, path, query, &page, &scope)
		if err != nil {
			if pages == 0 {
				return err
			}
			return l.partial(items, next, pages, scope,
				fmt.Sprintf("listing stopped after %d pages: %s", pages, err.Error()),
				"the items read so far are reported; re-run with --cursor to continue from nextCursor")
		}
		pages++
		items = append(items, page.Items...)
		next = ""
		if page.NextPageToken != nil {
			next = *page.NextPageToken
		}
		if !l.all || next == "" {
			break
		}
		if pages >= l.maxPages {
			return l.partial(items, next, pages, scope,
				fmt.Sprintf("--all reached its budget of %d pages before the end of the listing", pages),
				"raise --max-pages, or continue with --cursor from nextCursor")
		}
	}
	payload, meta := l.result(items, next, pages, scope)
	return env.render(payload, meta, func(w io.Writer) error {
		if err := l.render(w, items); err != nil {
			return err
		}
		if next != "" {
			if l.command != "" {
				_, _ = fmt.Fprintf(env.stderr, "more %s: %s --cursor %s\n", l.noun, l.command, next)
			} else {
				_, _ = fmt.Fprintf(env.stderr, "more %s: re-run the same command with --cursor %s\n", l.noun, next)
			}
		}
		return nil
	})
}

// read fetches one page; with a scope extractor it also keeps the page's
// context fields (the last page read wins).
func (l *cursorList[T]) read(ctx context.Context, client *apiClient, path string, query url.Values, page *serverPage[T], scope *map[string]any) error {
	if l.scope == nil {
		return client.getJSON(ctx, path, query, page)
	}
	var raw json.RawMessage
	if err := client.getJSON(ctx, path, query, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, page); err != nil {
		return &APIError{Code: "invalid_response", HTTPStatus: 200, Message: "server returned an unreadable JSON response"}
	}
	*scope = l.scope(raw)
	return nil
}

func (l *cursorList[T]) result(items []T, next string, pages int, scope map[string]any) (listPayload[T], *envelopeMeta) {
	payload := newListPayload(items)
	if next != "" {
		payload.NextCursor = &next
	}
	complete := next == ""
	return payload, &envelopeMeta{Pagination: &paginationMeta{Mode: "cursor", PageSize: l.pageSize, Pages: pages, Complete: &complete}, Scope: scope}
}

func (l *cursorList[T]) partial(items []T, next string, pages int, scope map[string]any, message, hint string) error {
	payload, meta := l.result(items, next, pages, scope)
	return &resultError{
		err:   &PartialError{Message: message, Hint: hint},
		data:  payload,
		meta:  meta,
		human: func(w io.Writer) error { return l.render(w, items) },
	}
}
