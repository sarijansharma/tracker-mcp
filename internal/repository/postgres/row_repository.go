package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

type rowRepository struct {
	db querier
}

const rowFieldsSQL = `id, tracker_id, data, created_by, created_at, updated_at`

func (r *rowRepository) Insert(ctx context.Context, trackerID int64, createdBy string, values []domain.RowValues) ([]domain.Row, error) {
	batch := &pgx.Batch{}
	for _, rowValues := range values {
		encoded, err := json.Marshal(rowValues)
		if err != nil {
			return nil, fmt.Errorf("encoding row: %w", err)
		}
		batch.Queue(`INSERT INTO tracker_rows (tracker_id, data, created_by) VALUES ($1, $2::jsonb, $3)
			RETURNING `+rowFieldsSQL, trackerID, string(encoded), createdBy)
	}
	results := r.db.SendBatch(ctx, batch)
	inserted := make([]domain.Row, 0, len(values))
	for range values {
		row, err := scanRow(results.QueryRow())
		if err != nil {
			_ = results.Close() // the scan error is the one worth reporting
			return nil, translateError(err)
		}
		inserted = append(inserted, row)
	}
	if err := results.Close(); err != nil {
		return nil, translateError(err)
	}
	return inserted, nil
}

func (r *rowRepository) Patch(ctx context.Context, patch domain.RowPatch) (domain.Row, error) {
	set := patch.Set
	if set == nil {
		set = domain.RowValues{}
	}
	encoded, err := json.Marshal(set)
	if err != nil {
		return domain.Row{}, fmt.Errorf("encoding row patch: %w", err)
	}
	unset := patch.Unset
	if unset == nil {
		unset = []string{}
	}
	// A single statement merge keeps concurrent edits to different fields from clobbering
	// each other: nobody reads-modifies-writes the whole document.
	row, err := scanRow(r.db.QueryRow(ctx, `
		UPDATE tracker_rows
		SET data = (data || $3::jsonb) - $4::text[], updated_at = now()
		WHERE tracker_id = $1 AND id = $2
		RETURNING `+rowFieldsSQL, patch.TrackerID, patch.RowID, string(encoded), unset))
	if err != nil {
		return domain.Row{}, translateError(err)
	}
	return row, nil
}

func (r *rowRepository) GetByIDs(ctx context.Context, trackerID int64, rowIDs []int64) ([]domain.Row, error) {
	rows, err := r.db.Query(ctx, `SELECT `+rowFieldsSQL+` FROM tracker_rows
		WHERE tracker_id = $1 AND id = ANY($2::bigint[]) ORDER BY id`, trackerID, rowIDs)
	if err != nil {
		return nil, fmt.Errorf("loading rows: %w", err)
	}
	return collectRows(rows)
}

func (r *rowRepository) Delete(ctx context.Context, trackerID int64, rowIDs []int64) ([]int64, error) {
	rows, err := r.db.Query(ctx, `DELETE FROM tracker_rows WHERE tracker_id = $1 AND id = ANY($2::bigint[]) RETURNING id`,
		trackerID, rowIDs)
	if err != nil {
		return nil, fmt.Errorf("deleting rows: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func (r *rowRepository) Query(ctx context.Context, trackerID int64, query domain.RowQuery) (domain.RowPage, error) {
	args := &sqlArgs{}
	where, err := buildWhere(trackerID, query.Filter, args)
	if err != nil {
		return domain.RowPage{}, err
	}

	var page domain.RowPage
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM tracker_rows WHERE `+where, args.values...).
		Scan(&page.TotalMatching); err != nil {
		return domain.RowPage{}, fmt.Errorf("counting rows: %w", err)
	}
	if page.TotalMatching == 0 {
		page.Rows = []domain.Row{}
		return page, nil
	}

	orderBy := buildOrderBy(query.Sorts, args)
	sql := fmt.Sprintf(`SELECT %s FROM tracker_rows WHERE %s ORDER BY %s LIMIT %s OFFSET %s`,
		rowFieldsSQL, where, orderBy, args.add(query.Limit), args.add(query.Offset))
	rows, err := r.db.Query(ctx, sql, args.values...)
	if err != nil {
		return domain.RowPage{}, fmt.Errorf("querying rows: %w", err)
	}
	page.Rows, err = collectRows(rows)
	return page, err
}

func (r *rowRepository) Summarize(ctx context.Context, trackerID int64, groupBy domain.Column, filter domain.RowFilter) ([]domain.GroupCount, error) {
	args := &sqlArgs{}
	where, err := buildWhere(trackerID, filter, args)
	if err != nil {
		return nil, err
	}
	key := args.add(groupBy.StorageKey()) + "::text"

	var sql string
	if groupBy.Type.ValueKind() == domain.ValueKindOptionList {
		// A row tagged ["Go", "SQL"] counts once towards each tag.
		sql = fmt.Sprintf(`SELECT selected.option, count(*) FROM tracker_rows
			LEFT JOIN LATERAL jsonb_array_elements_text(data->%s) AS selected(option) ON true
			WHERE %s GROUP BY 1 ORDER BY 2 DESC, 1 NULLS LAST LIMIT %s`, key, where, args.add(domain.MaxSummaryGroups+1))
	} else {
		sql = fmt.Sprintf(`SELECT data->>%s, count(*) FROM tracker_rows
			WHERE %s GROUP BY 1 ORDER BY 2 DESC, 1 NULLS LAST LIMIT %s`, key, where, args.add(domain.MaxSummaryGroups+1))
	}
	rows, err := r.db.Query(ctx, sql, args.values...)
	if err != nil {
		return nil, fmt.Errorf("summarising rows: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GroupCount, error) {
		var group domain.GroupCount
		err := row.Scan(&group.Value, &group.Count)
		return group, err
	})
}

func (r *rowRepository) CountRows(ctx context.Context, trackerID int64) (int64, error) {
	var count int64
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM tracker_rows WHERE tracker_id = $1`, trackerID).Scan(&count)
	return count, translateError(err)
}

func (r *rowRepository) CountRowsMissing(ctx context.Context, trackerID int64, storageKey string) (int64, error) {
	var count int64
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM tracker_rows WHERE tracker_id = $1 AND data->($2::text) IS NULL`,
		trackerID, storageKey).Scan(&count)
	return count, translateError(err)
}

func (r *rowRepository) FillMissing(ctx context.Context, trackerID int64, storageKey string, value any) (int64, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("encoding fill value: %w", err)
	}
	tag, err := r.db.Exec(ctx, `UPDATE tracker_rows SET data = data || jsonb_build_object($2::text, $3::jsonb)
		WHERE tracker_id = $1 AND data->($2::text) IS NULL`, trackerID, storageKey, string(encoded))
	if err != nil {
		return 0, translateError(err)
	}
	return tag.RowsAffected(), nil
}

func (r *rowRepository) RemoveKey(ctx context.Context, trackerID int64, storageKey string) (int64, error) {
	tag, err := r.db.Exec(ctx, `UPDATE tracker_rows SET data = data - $2::text
		WHERE tracker_id = $1 AND data->($2::text) IS NOT NULL`, trackerID, storageKey)
	if err != nil {
		return 0, translateError(err)
	}
	return tag.RowsAffected(), nil
}

func (r *rowRepository) CountOptionUsage(ctx context.Context, trackerID int64, column domain.Column, options []string) (map[string]int64, error) {
	var sql string
	if column.Type.ValueKind() == domain.ValueKindOptionList {
		sql = `SELECT selected.option, count(*) FROM tracker_rows,
			LATERAL jsonb_array_elements_text(data->($2::text)) AS selected(option)
			WHERE tracker_id = $1 AND selected.option = ANY($3::text[]) GROUP BY 1`
	} else {
		sql = `SELECT data->>($2::text), count(*) FROM tracker_rows
			WHERE tracker_id = $1 AND data->>($2::text) = ANY($3::text[]) GROUP BY 1`
	}
	rows, err := r.db.Query(ctx, sql, trackerID, column.StorageKey(), options)
	if err != nil {
		return nil, fmt.Errorf("counting option usage: %w", err)
	}
	defer rows.Close()
	usage := make(map[string]int64)
	for rows.Next() {
		var option string
		var count int64
		if err := rows.Scan(&option, &count); err != nil {
			return nil, err
		}
		usage[option] = count
	}
	return usage, rows.Err()
}

func collectRows(rows pgx.Rows) ([]domain.Row, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Row, error) {
		return scanRow(row)
	})
}

func scanRow(row pgx.Row) (domain.Row, error) {
	var (
		result domain.Row
		data   []byte
	)
	if err := row.Scan(&result.ID, &result.TrackerID, &data, &result.CreatedBy, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return domain.Row{}, err
	}
	if err := json.Unmarshal(data, &result.Values); err != nil {
		return domain.Row{}, fmt.Errorf("decoding row %d: %w", result.ID, err)
	}
	return result, nil
}

var _ repository.RowRepository = (*rowRepository)(nil)
