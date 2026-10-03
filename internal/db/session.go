package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Listen calls fn with the payload of every notification on channel until ctx ends (nil) or
// the connection fails (the error). It uses a dedicated connection outside the pool, so a
// long-lived LISTEN never holds a pool slot.
func (d *DB) Listen(ctx context.Context, channel string, fn func(payload string)) error {
	c, err := d.dedicated(ctx)
	if err != nil {
		return err
	}
	defer closeConn(c)
	if _, err := c.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return ctxOr(ctx, err)
	}
	for {
		n, err := c.WaitForNotification(ctx)
		if err != nil {
			return ctxOr(ctx, err)
		}
		fn(n.Payload)
	}
}

// SessionLock is a session-level advisory lock held on a dedicated connection. PostgreSQL
// releases it when that connection ends, so a crashed holder never keeps it.
type SessionLock struct{ conn *pgx.Conn }

// TryLock takes the advisory lock key without waiting. It returns nil, nil when another
// session holds it.
func (d *DB) TryLock(ctx context.Context, key int64) (*SessionLock, error) {
	c, err := d.dedicated(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := c.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil || !ok {
		closeConn(c)
		return nil, err
	}
	return &SessionLock{c}, nil
}

// Held fails when the lock's connection is gone; the lock is then already released.
func (l *SessionLock) Held(ctx context.Context) error { return l.conn.Ping(ctx) }

// Release ends the session, which releases the lock.
func (l *SessionLock) Release() { closeConn(l.conn) }

// dedicated opens a connection with the pool's settings and session setup (role, schema).
func (d *DB) dedicated(ctx context.Context) (*pgx.Conn, error) {
	cfg := d.pool.Config()
	c, err := pgx.ConnectConfig(ctx, cfg.ConnConfig.Copy())
	if err != nil {
		return nil, err
	}
	if cfg.AfterConnect != nil {
		if err := cfg.AfterConnect(ctx, c); err != nil {
			closeConn(c)
			return nil, err
		}
	}
	return c, nil
}

func closeConn(c *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Close(ctx)
}

// ctxOr reports a cancelled ctx as a clean stop instead of the I/O error it caused.
func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
