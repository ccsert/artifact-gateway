package pgstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// This driver isolates a network failure during advisory unlock. Ordinary
// SQL errors do not tell database/sql to evict a still-locked physical session.
type unlockFailureDriver struct{ opened, closed atomic.Int64 }

func (d *unlockFailureDriver) Open(string) (driver.Conn, error) {
	d.opened.Add(1)
	return &unlockFailureConn{owner: d}, nil
}

type unlockFailureConn struct {
	owner *unlockFailureDriver
	execs int
}

func (c *unlockFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (c *unlockFailureConn) Close() error              { c.owner.closed.Add(1); return nil }
func (c *unlockFailureConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }
func (c *unlockFailureConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.execs++
	if c.execs > 1 {
		return nil, errors.New("synthetic unlock network failure")
	}
	return driver.RowsAffected(1), nil
}
func (c *unlockFailureConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &booleanRow{}, nil
}

type booleanRow struct{ read bool }

func (*booleanRow) Columns() []string { return []string{"valid"} }
func (*booleanRow) Close() error      { return nil }
func (r *booleanRow) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = true
	return nil
}

func TestPublicationFenceUnlockFailureDiscardsPhysicalSession(t *testing.T) {
	d := &unlockFailureDriver{}
	name := "migration-unlock-" + uuid.NewString()
	sql.Register(name, d)
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	release, err := New(db).Fence(ctx, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	release()
	if d.closed.Load() != 1 {
		t.Fatalf("failed unlock returned a physical session to the pool: closed=%d", d.closed.Load())
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if d.opened.Load() != 2 {
		t.Fatalf("next borrower reused a potentially locked session: opens=%d", d.opened.Load())
	}
}
