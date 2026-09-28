package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

type trackerRepository struct {
	db querier
}

const trackerColumnsSQL = `id, name, description, created_by, created_at, updated_at, deleted_at`

// summarySelectSQL returns tracker summaries with column and row counts.
const summarySelectSQL = `
SELECT t.id, t.name, t.description, t.created_by, t.created_at, t.updated_at, t.deleted_at,
       (SELECT count(*) FROM tracker_columns c WHERE c.tracker_id = t.id) AS column_count,
       (SELECT count(*) FROM tracker_rows r WHERE r.tracker_id = t.id)    AS row_count
FROM trackers t`

func (r *trackerRepository) Create(ctx context.Context, tracker repository.NewTracker) (domain.Tracker, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO trackers (name, description, created_by) VALUES ($1, $2, $3)
		RETURNING `+trackerColumnsSQL, tracker.Name, tracker.Description, tracker.CreatedBy)
	created, err := scanTracker(row)
	if err != nil {
		return domain.Tracker{}, translateError(err)
	}
	return created, nil
}

func (r *trackerRepository) Get(ctx context.Context, trackerID int64) (domain.Tracker, error) {
	tracker, err := scanTracker(r.db.QueryRow(ctx, `SELECT `+trackerColumnsSQL+` FROM trackers WHERE id = $1`, trackerID))
	if err != nil {
		return domain.Tracker{}, translateError(err)
	}
	return r.withColumns(ctx, tracker)
}

func (r *trackerRepository) FindActiveByName(ctx context.Context, name string) (domain.Tracker, error) {
	tracker, err := scanTracker(r.db.QueryRow(ctx, `
		SELECT `+trackerColumnsSQL+` FROM trackers
		WHERE lower(name) = lower($1) AND deleted_at IS NULL`, name))
	if err != nil {
		return domain.Tracker{}, translateError(err)
	}
	return r.withColumns(ctx, tracker)
}

func (r *trackerRepository) FindDeletedByName(ctx context.Context, name string) ([]domain.TrackerSummary, error) {
	return r.querySummaries(ctx, summarySelectSQL+`
		WHERE lower(t.name) = lower($1) AND t.deleted_at IS NOT NULL
		ORDER BY t.deleted_at DESC`, name)
}

func (r *trackerRepository) SearchSimilar(ctx context.Context, name string, limit int) ([]domain.TrackerSummary, error) {
	// Trigram similarity catches typos ("Intervew"); strpos catches partial names ("interview").
	return r.querySummaries(ctx, summarySelectSQL+`
		WHERE t.deleted_at IS NULL
		  AND (similarity(lower(t.name), lower($1)) > 0.2
		       OR strpos(lower(t.name), lower($1)) > 0
		       OR strpos(lower($1), lower(t.name)) > 0)
		ORDER BY similarity(lower(t.name), lower($1)) DESC, t.created_at DESC
		LIMIT $2`, name, limit)
}

func (r *trackerRepository) List(ctx context.Context, filter domain.TrackerListFilter) ([]domain.TrackerSummary, error) {
	return r.querySummaries(ctx, summarySelectSQL+`
		WHERE ($1 OR t.deleted_at IS NULL)
		  AND ($2 = '' OR strpos(lower(t.name), lower($2)) > 0 OR similarity(lower(t.name), lower($2)) > 0.2)
		ORDER BY t.deleted_at IS NOT NULL, lower(t.name), t.created_at`, filter.IncludeDeleted, filter.Search)
}

func (r *trackerRepository) Summary(ctx context.Context, trackerID int64) (domain.TrackerSummary, error) {
	summaries, err := r.querySummaries(ctx, summarySelectSQL+` WHERE t.id = $1`, trackerID)
	if err != nil {
		return domain.TrackerSummary{}, err
	}
	if len(summaries) == 0 {
		return domain.TrackerSummary{}, repository.ErrNotFound
	}
	return summaries[0], nil
}

func (r *trackerRepository) Rename(ctx context.Context, trackerID int64, newName string) error {
	return r.execOne(ctx, `UPDATE trackers SET name = $2, updated_at = now() WHERE id = $1`, trackerID, newName)
}

func (r *trackerRepository) SoftDelete(ctx context.Context, trackerID int64) error {
	return r.execOne(ctx, `UPDATE trackers SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL`, trackerID)
}

func (r *trackerRepository) Restore(ctx context.Context, trackerID int64, name string) error {
	return r.execOne(ctx, `UPDATE trackers SET deleted_at = NULL, name = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NOT NULL`, trackerID, name)
}

func (r *trackerRepository) LockForUpdate(ctx context.Context, trackerID int64) error {
	var id int64
	err := r.db.QueryRow(ctx, `SELECT id FROM trackers WHERE id = $1 FOR UPDATE`, trackerID).Scan(&id)
	return translateError(err)
}

func (r *trackerRepository) execOne(ctx context.Context, sql string, args ...any) error {
	tag, err := r.db.Exec(ctx, sql, args...)
	if err != nil {
		return translateError(err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *trackerRepository) withColumns(ctx context.Context, tracker domain.Tracker) (domain.Tracker, error) {
	columns, err := listColumns(ctx, r.db, tracker.ID)
	if err != nil {
		return domain.Tracker{}, err
	}
	tracker.Columns = columns
	return tracker, nil
}

func (r *trackerRepository) querySummaries(ctx context.Context, sql string, args ...any) ([]domain.TrackerSummary, error) {
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("querying trackers: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TrackerSummary, error) {
		var s domain.TrackerSummary
		err := row.Scan(&s.ID, &s.Name, &s.Description, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt,
			&s.ColumnCount, &s.RowCount)
		return s, err
	})
}

func scanTracker(row pgx.Row) (domain.Tracker, error) {
	var t domain.Tracker
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt)
	return t, err
}
