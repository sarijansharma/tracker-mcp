// Package repository defines the persistence contracts the service layer depends on.
// Implementations (e.g. postgres) live in sub-packages, so storage can be swapped without
// touching business rules.
package repository

import (
	"context"
	"errors"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
)

var (
	// ErrNotFound is returned when the requested record does not exist.
	ErrNotFound = errors.New("record not found")
	// ErrDuplicateName is returned when a unique name constraint is violated.
	ErrDuplicateName = errors.New("duplicate name")
)

// NewTracker holds the fields needed to create a tracker.
type NewTracker struct {
	Name        string
	Description string
	CreatedBy   string
}

// TrackerRepository persists tracker metadata.
type TrackerRepository interface {
	Create(ctx context.Context, tracker NewTracker) (domain.Tracker, error)
	// Get loads a tracker (deleted or not) with its columns, or ErrNotFound.
	Get(ctx context.Context, trackerID int64) (domain.Tracker, error)
	// FindActiveByName matches case and whitespace insensitively, or returns ErrNotFound.
	FindActiveByName(ctx context.Context, name string) (domain.Tracker, error)
	FindDeletedByName(ctx context.Context, name string) ([]domain.TrackerSummary, error)
	// SearchSimilar returns active trackers whose names resemble name, best match first.
	SearchSimilar(ctx context.Context, name string, limit int) ([]domain.TrackerSummary, error)
	List(ctx context.Context, filter domain.TrackerListFilter) ([]domain.TrackerSummary, error)
	Summary(ctx context.Context, trackerID int64) (domain.TrackerSummary, error)
	Rename(ctx context.Context, trackerID int64, newName string) error
	SoftDelete(ctx context.Context, trackerID int64) error
	Restore(ctx context.Context, trackerID int64, name string) error
	// LockForUpdate serialises schema changes on one tracker within a transaction.
	LockForUpdate(ctx context.Context, trackerID int64) error
}

// ColumnRepository persists column definitions.
type ColumnRepository interface {
	Create(ctx context.Context, column domain.Column) (domain.Column, error)
	// Update saves name, description, required, options and default value.
	Update(ctx context.Context, column domain.Column) (domain.Column, error)
	Delete(ctx context.Context, columnID int64) error
	NextPosition(ctx context.Context, trackerID int64) (int, error)
}

// RowRepository persists tracker rows.
type RowRepository interface {
	Insert(ctx context.Context, trackerID int64, createdBy string, rows []domain.RowValues) ([]domain.Row, error)
	// Patch applies a partial update atomically, or returns ErrNotFound.
	Patch(ctx context.Context, patch domain.RowPatch) (domain.Row, error)
	GetByIDs(ctx context.Context, trackerID int64, rowIDs []int64) ([]domain.Row, error)
	Delete(ctx context.Context, trackerID int64, rowIDs []int64) ([]int64, error)
	Query(ctx context.Context, trackerID int64, query domain.RowQuery) (domain.RowPage, error)
	Summarize(ctx context.Context, trackerID int64, groupBy domain.Column, filter domain.RowFilter) ([]domain.GroupCount, error)

	CountRows(ctx context.Context, trackerID int64) (int64, error)
	CountRowsMissing(ctx context.Context, trackerID int64, storageKey string) (int64, error)
	// FillMissing sets value on every row where the key is empty, returning rows changed.
	FillMissing(ctx context.Context, trackerID int64, storageKey string, value any) (int64, error)
	// RemoveKey drops the key from every row, returning rows that had a value.
	RemoveKey(ctx context.Context, trackerID int64, storageKey string) (int64, error)
	// CountOptionUsage counts rows per option for a select column, for the given options.
	CountOptionUsage(ctx context.Context, trackerID int64, column domain.Column, options []string) (map[string]int64, error)
}

// Repositories groups the repositories that share one connection or transaction.
type Repositories interface {
	Trackers() TrackerRepository
	Columns() ColumnRepository
	Rows() RowRepository
}

// Store gives access to repositories and runs multi-step work atomically.
type Store interface {
	Repositories
	// WithinTransaction runs fn with repositories bound to one transaction; it commits when
	// fn returns nil and rolls back otherwise.
	WithinTransaction(ctx context.Context, fn func(repos Repositories) error) error
}
