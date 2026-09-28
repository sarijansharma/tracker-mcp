package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

type columnRepository struct {
	db querier
}

const columnFieldsSQL = `id, tracker_id, name, type, description, required, options, default_value, position, created_at, updated_at`

func (r *columnRepository) Create(ctx context.Context, column domain.Column) (domain.Column, error) {
	options, defaultValue, err := encodeColumnJSON(column)
	if err != nil {
		return domain.Column{}, err
	}
	created, err := scanColumn(r.db.QueryRow(ctx, `
		INSERT INTO tracker_columns (tracker_id, name, type, description, required, options, default_value, position)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8)
		RETURNING `+columnFieldsSQL,
		column.TrackerID, column.Name, string(column.Type), column.Description, column.Required,
		options, defaultValue, column.Position))
	if err != nil {
		return domain.Column{}, translateError(err)
	}
	return created, nil
}

func (r *columnRepository) Update(ctx context.Context, column domain.Column) (domain.Column, error) {
	options, defaultValue, err := encodeColumnJSON(column)
	if err != nil {
		return domain.Column{}, err
	}
	updated, err := scanColumn(r.db.QueryRow(ctx, `
		UPDATE tracker_columns
		SET name = $2, description = $3, required = $4, options = $5::jsonb, default_value = $6::jsonb, updated_at = now()
		WHERE id = $1
		RETURNING `+columnFieldsSQL,
		column.ID, column.Name, column.Description, column.Required, options, defaultValue))
	if err != nil {
		return domain.Column{}, translateError(err)
	}
	return updated, nil
}

func (r *columnRepository) Delete(ctx context.Context, columnID int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM tracker_columns WHERE id = $1`, columnID)
	if err != nil {
		return translateError(err)
	}
	if tag.RowsAffected() == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *columnRepository) NextPosition(ctx context.Context, trackerID int64) (int, error) {
	var position int
	err := r.db.QueryRow(ctx, `SELECT COALESCE(max(position) + 1, 0) FROM tracker_columns WHERE tracker_id = $1`, trackerID).
		Scan(&position)
	return position, translateError(err)
}

func listColumns(ctx context.Context, db querier, trackerID int64) ([]domain.Column, error) {
	rows, err := db.Query(ctx, `SELECT `+columnFieldsSQL+` FROM tracker_columns WHERE tracker_id = $1 ORDER BY position, id`, trackerID)
	if err != nil {
		return nil, fmt.Errorf("loading columns: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Column, error) {
		return scanColumn(row)
	})
}

func scanColumn(row pgx.Row) (domain.Column, error) {
	var (
		column       domain.Column
		columnType   string
		options      []byte
		defaultValue []byte
	)
	err := row.Scan(&column.ID, &column.TrackerID, &column.Name, &columnType, &column.Description, &column.Required,
		&options, &defaultValue, &column.Position, &column.CreatedAt, &column.UpdatedAt)
	if err != nil {
		return domain.Column{}, err
	}
	column.Type = domain.ColumnType(columnType)
	if err := json.Unmarshal(options, &column.Options); err != nil {
		return domain.Column{}, fmt.Errorf("decoding options of column %d: %w", column.ID, err)
	}
	if len(defaultValue) > 0 {
		if err := json.Unmarshal(defaultValue, &column.DefaultValue); err != nil {
			return domain.Column{}, fmt.Errorf("decoding default of column %d: %w", column.ID, err)
		}
	}
	return column, nil
}

// encodeColumnJSON returns the options array and nullable default value as JSON text.
func encodeColumnJSON(column domain.Column) (options string, defaultValue *string, err error) {
	opts := column.Options
	if opts == nil {
		opts = []string{}
	}
	optionsJSON, err := json.Marshal(opts)
	if err != nil {
		return "", nil, fmt.Errorf("encoding options: %w", err)
	}
	if column.DefaultValue == nil {
		return string(optionsJSON), nil, nil
	}
	defaultJSON, err := json.Marshal(column.DefaultValue)
	if err != nil {
		return "", nil, fmt.Errorf("encoding default value: %w", err)
	}
	encoded := string(defaultJSON)
	return string(optionsJSON), &encoded, nil
}
