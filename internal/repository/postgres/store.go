// Package postgres implements the repository contracts on PostgreSQL, storing row values
// as JSONB keyed by column id.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so repositories work the same
// inside and outside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults
}

// PoolOptions tunes the connection pool.
type PoolOptions struct {
	MaxConnections   int32
	StatementTimeout time.Duration
}

// NewPool connects to Postgres and verifies the connection.
func NewPool(ctx context.Context, databaseURL string, options PoolOptions) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}
	if options.MaxConnections > 0 {
		config.MaxConns = options.MaxConnections
	}
	if options.StatementTimeout > 0 {
		// Guards against runaway queries on very large trackers.
		config.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(options.StatementTimeout.Milliseconds(), 10)
	}
	config.ConnConfig.RuntimeParams["application_name"] = "tracker-mcp"

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	return pool, nil
}

// Store is the Postgres implementation of repository.Store.
type Store struct {
	pool *pgxpool.Pool
	repositorySet
}

var _ repository.Store = (*Store)(nil)

// NewStore wraps a connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, repositorySet: newRepositorySet(pool)}
}

// WithinTransaction implements repository.Store.
func (s *Store) WithinTransaction(ctx context.Context, fn func(repos repository.Repositories) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(newRepositorySet(tx))
	})
}

type repositorySet struct {
	trackers *trackerRepository
	columns  *columnRepository
	rows     *rowRepository
}

func newRepositorySet(db querier) repositorySet {
	return repositorySet{
		trackers: &trackerRepository{db: db},
		columns:  &columnRepository{db: db},
		rows:     &rowRepository{db: db},
	}
}

func (r repositorySet) Trackers() repository.TrackerRepository { return r.trackers }
func (r repositorySet) Columns() repository.ColumnRepository   { return r.columns }
func (r repositorySet) Rows() repository.RowRepository         { return r.rows }

const pgUniqueViolation = "23505"

// translateError maps driver errors to repository sentinels.
func translateError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return fmt.Errorf("%w: %s", repository.ErrDuplicateName, pgErr.ConstraintName)
	}
	return err
}
