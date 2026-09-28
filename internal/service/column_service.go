package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

// ColumnService changes a tracker's columns. Every change runs in a transaction holding a
// lock on the tracker, so two people editing the same tracker's columns are serialised.
type ColumnService struct {
	store repository.Store
}

func NewColumnService(store repository.Store) *ColumnService {
	return &ColumnService{store: store}
}

// AddColumnResult reports the new column and how many existing rows got its default.
type AddColumnResult struct {
	Tracker      domain.Tracker
	Column       domain.Column
	RowsBackfill int64
}

func (s *ColumnService) Add(ctx context.Context, ref TrackerRef, spec domain.ColumnSpec) (AddColumnResult, error) {
	column, err := spec.Validate()
	if err != nil {
		return AddColumnResult{}, err
	}
	var result AddColumnResult
	err = s.withLockedTracker(ctx, ref, func(repos repository.Repositories, tracker domain.Tracker) error {
		if len(tracker.Columns) >= domain.MaxColumnsPerTracker {
			return domain.InvalidInputf("Tracker %q already has the maximum of %d columns.", tracker.Name, domain.MaxColumnsPerTracker)
		}
		if existing, err := tracker.FindColumn(column.Name); err == nil {
			return domain.AlreadyExistsf("Tracker %q already has a column named %q.", tracker.Name, existing.Name)
		}
		if column.Required && column.DefaultValue == nil {
			rowCount, err := repos.Rows().CountRows(ctx, tracker.ID)
			if err != nil {
				return err
			}
			if rowCount > 0 {
				return domain.Conflictf("Tracker %q already has %d rows, which would have no value for the new required column %q. "+
					"Provide a default_value to fill them, or add the column as optional.", tracker.Name, rowCount, column.Name)
			}
		}
		position, err := repos.Columns().NextPosition(ctx, tracker.ID)
		if err != nil {
			return err
		}
		column.TrackerID, column.Position = tracker.ID, position
		created, err := repos.Columns().Create(ctx, column)
		if err != nil {
			return err
		}
		if created.DefaultValue != nil {
			if result.RowsBackfill, err = repos.Rows().FillMissing(ctx, tracker.ID, created.StorageKey(), created.DefaultValue); err != nil {
				return err
			}
		}
		tracker.Columns = append(tracker.Columns, created)
		result.Tracker, result.Column = tracker, created
		return nil
	})
	return result, err
}

// ColumnChanges lists the edits to apply to a column; nil / empty fields are left alone.
type ColumnChanges struct {
	Description        *string
	Required           *bool
	AddOptions         []string
	RemoveOptions      []string
	DefaultValue       any
	RemoveDefaultValue bool
}

func (c ColumnChanges) isEmpty() bool {
	return c.Description == nil && c.Required == nil && len(c.AddOptions) == 0 && len(c.RemoveOptions) == 0 &&
		c.DefaultValue == nil && !c.RemoveDefaultValue
}

// UpdateColumnResult reports the updated column and how many rows were backfilled.
type UpdateColumnResult struct {
	Column       domain.Column
	RowsBackfill int64
}

func (s *ColumnService) Update(ctx context.Context, ref TrackerRef, columnName string, changes ColumnChanges) (UpdateColumnResult, error) {
	if changes.isEmpty() {
		return UpdateColumnResult{}, domain.InvalidInputf("Nothing to change: pass description, required, add_options, remove_options, default_value or remove_default_value.")
	}
	if changes.DefaultValue != nil && changes.RemoveDefaultValue {
		return UpdateColumnResult{}, domain.InvalidInputf("Pass either default_value or remove_default_value, not both.")
	}
	var result UpdateColumnResult
	err := s.withLockedTracker(ctx, ref, func(repos repository.Repositories, tracker domain.Tracker) error {
		column, err := tracker.FindColumn(columnName)
		if err != nil {
			return err
		}
		if err := applyDescriptionChange(&column, changes); err != nil {
			return err
		}
		if err := applyOptionChanges(ctx, repos, tracker, &column, changes); err != nil {
			return err
		}
		if err := applyDefaultChange(&column, changes); err != nil {
			return err
		}
		if _, err := domain.NormalizeValue(column, column.DefaultValue); err != nil {
			return domain.Conflictf("The default value of %q would no longer be valid (%v). Set a new default_value or remove_default_value.", column.Name, err)
		}
		if changes.Required != nil {
			column.Required = *changes.Required
		}
		if column.Required {
			if result.RowsBackfill, err = ensureNoMissingValues(ctx, repos, tracker, column); err != nil {
				return err
			}
		}
		result.Column, err = repos.Columns().Update(ctx, column)
		return err
	})
	return result, err
}

func applyDescriptionChange(column *domain.Column, changes ColumnChanges) error {
	if changes.Description == nil {
		return nil
	}
	description := strings.TrimSpace(*changes.Description)
	if len(description) > domain.MaxColumnDescriptionLength {
		return domain.InvalidInputf("The description is too long (max %d characters).", domain.MaxColumnDescriptionLength)
	}
	column.Description = description
	return nil
}

// applyOptionChanges adds/removes select options. Removing an option still used by rows is
// refused, because those rows would silently end up holding an invalid value.
func applyOptionChanges(ctx context.Context, repos repository.Repositories, tracker domain.Tracker, column *domain.Column, changes ColumnChanges) error {
	if len(changes.AddOptions) == 0 && len(changes.RemoveOptions) == 0 {
		return nil
	}
	if !column.Type.HasOptions() {
		return domain.InvalidInputf("Column %q is a %s column; only single_select and multi_select columns have options.", column.Name, column.Type)
	}

	toRemove := make([]string, 0, len(changes.RemoveOptions))
	for _, raw := range changes.RemoveOptions {
		option, found := column.FindOption(raw)
		if !found {
			return domain.NotFoundf("Column %q has no option %q (options: %s).", column.Name, domain.CleanName(raw), strings.Join(column.Options, ", ")).
				WithSuggestions(domain.QuoteAll(domain.SuggestSimilar(raw, column.Options, 2))...)
		}
		toRemove = append(toRemove, option)
	}
	if len(toRemove) > 0 {
		usage, err := repos.Rows().CountOptionUsage(ctx, tracker.ID, *column, toRemove)
		if err != nil {
			return err
		}
		if len(usage) > 0 {
			return domain.Conflictf("Can't remove options that rows still use (%s). Change those rows first, e.g. with query_rows + update_row.",
				describeUsage(usage))
		}
	}

	remaining := make([]string, 0, len(column.Options)+len(changes.AddOptions))
	for _, option := range column.Options {
		if !containsFold(toRemove, option) {
			remaining = append(remaining, option)
		}
	}
	for _, raw := range changes.AddOptions {
		if _, exists := column.FindOption(raw); exists {
			continue // adding an option that already exists is a no-op, not an error
		}
		remaining = append(remaining, raw)
	}
	options, err := domain.CleanOptions(column.Name, remaining)
	if err != nil {
		return err
	}
	if len(options) == 0 {
		return domain.InvalidInputf("Column %q must keep at least one option.", column.Name)
	}
	column.Options = options
	return nil
}

func applyDefaultChange(column *domain.Column, changes ColumnChanges) error {
	switch {
	case changes.RemoveDefaultValue:
		column.DefaultValue = nil
	case changes.DefaultValue != nil:
		value, err := domain.NormalizeValue(*column, changes.DefaultValue)
		if err != nil {
			return domain.InvalidInputf("Default value for column %q is invalid: %v", column.Name, err)
		}
		column.DefaultValue = value
	}
	return nil
}

// ensureNoMissingValues makes a required column hold: it fills empty rows with the default,
// or refuses when there is no default to fill them with.
func ensureNoMissingValues(ctx context.Context, repos repository.Repositories, tracker domain.Tracker, column domain.Column) (int64, error) {
	missing, err := repos.Rows().CountRowsMissing(ctx, tracker.ID, column.StorageKey())
	if err != nil || missing == 0 {
		return 0, err
	}
	if column.DefaultValue == nil {
		return 0, domain.Conflictf("%d rows have no value for %q, so it can't be required yet. Set a default_value to fill them, "+
			"or fill those rows first (query_rows with operator is_empty finds them).", missing, column.Name)
	}
	return repos.Rows().FillMissing(ctx, tracker.ID, column.StorageKey(), column.DefaultValue)
}

func (s *ColumnService) Rename(ctx context.Context, ref TrackerRef, columnName, newName string) (domain.Column, error) {
	name, err := domain.ValidateName("new column name", newName, domain.MaxColumnNameLength)
	if err != nil {
		return domain.Column{}, err
	}
	if domain.IsReservedFieldName(name) {
		return domain.Column{}, domain.InvalidInputf("%q is a built-in field name (%s); please choose another name.",
			name, strings.Join(domain.ReservedFieldNames, ", "))
	}
	var renamed domain.Column
	err = s.withLockedTracker(ctx, ref, func(repos repository.Repositories, tracker domain.Tracker) error {
		column, err := tracker.FindColumn(columnName)
		if err != nil {
			return err
		}
		if other, err := tracker.FindColumn(name); err == nil && other.ID != column.ID {
			return domain.AlreadyExistsf("Tracker %q already has a column named %q.", tracker.Name, other.Name)
		}
		column.Name = name
		// Values are keyed by column id, so a rename never touches row data.
		renamed, err = repos.Columns().Update(ctx, column)
		return err
	})
	if errors.Is(err, repository.ErrDuplicateName) {
		return domain.Column{}, domain.AlreadyExistsf("The tracker already has a column named %q.", name)
	}
	return renamed, err
}

// RemoveColumnResult reports what removing a column did, or would do when not confirmed.
type RemoveColumnResult struct {
	Column           domain.Column
	RowsWithValues   int64
	Confirmed        bool
	RemainingColumns []string
}

// Remove deletes a column and its values from every row. Without confirm it only previews.
func (s *ColumnService) Remove(ctx context.Context, ref TrackerRef, columnName string, confirm bool) (RemoveColumnResult, error) {
	var result RemoveColumnResult
	err := s.withLockedTracker(ctx, ref, func(repos repository.Repositories, tracker domain.Tracker) error {
		column, err := tracker.FindColumn(columnName)
		if err != nil {
			return err
		}
		if len(tracker.Columns) == 1 {
			return domain.Conflictf("%q is the only column of tracker %q; add another column before removing it, or delete the tracker.", column.Name, tracker.Name)
		}
		result.Column = column
		for _, other := range tracker.Columns {
			if other.ID != column.ID {
				result.RemainingColumns = append(result.RemainingColumns, other.Name)
			}
		}
		if !confirm {
			total, err := repos.Rows().CountRows(ctx, tracker.ID)
			if err != nil {
				return err
			}
			missing, err := repos.Rows().CountRowsMissing(ctx, tracker.ID, column.StorageKey())
			result.RowsWithValues = total - missing
			return err
		}
		if result.RowsWithValues, err = repos.Rows().RemoveKey(ctx, tracker.ID, column.StorageKey()); err != nil {
			return err
		}
		result.Confirmed = true
		return repos.Columns().Delete(ctx, column.ID)
	})
	return result, err
}

func (s *ColumnService) withLockedTracker(ctx context.Context, ref TrackerRef, fn func(repository.Repositories, domain.Tracker) error) error {
	return s.store.WithinTransaction(ctx, func(repos repository.Repositories) error {
		tracker, err := resolveActiveTracker(ctx, repos, ref)
		if err != nil {
			return err
		}
		if err := repos.Trackers().LockForUpdate(ctx, tracker.ID); err != nil {
			return err
		}
		// Re-read after locking so we act on the latest column set.
		if tracker, err = repos.Trackers().Get(ctx, tracker.ID); err != nil {
			return err
		}
		return fn(repos, tracker)
	})
}

func describeUsage(usage map[string]int64) string {
	options := make([]string, 0, len(usage))
	for option := range usage {
		options = append(options, option)
	}
	sort.Strings(options)
	parts := make([]string, len(options))
	for i, option := range options {
		parts[i] = fmt.Sprintf("%q is used by %d rows", option, usage[option])
	}
	return strings.Join(parts, ", ")
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
