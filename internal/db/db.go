// Package db implements migrations and the query layer. It is the only package allowed to import pgx.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema holds every Vitamux table (deploy/sql/roles.sql creates it).
const Schema = "vitamux"

// Role is a database role from deploy/sql/roles.sql.
type Role string

const (
	// OwnerRole owns the schema and is used only by `vitamux migrate`.
	OwnerRole Role = "vitamux_owner"
	// AppRole is the DML-only role used by `vitamux serve`.
	AppRole Role = "vitamux_app"
)

// Open connects with every session switched to role and the Vitamux schema. The login user
// must be role itself or a member of it, so a single development superuser still runs the
// app with least privilege.
func Open(ctx context.Context, url string, role Role) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("parse database url: invalid connection string") // detail may echo the password
	}
	setup := "SET ROLE " + pgx.Identifier{string(role)}.Sanitize() + "; SET search_path = " + Schema
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, setup)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect as %s: %w", role, err)
	}
	return pool, nil
}
