package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

// RowService adds, edits, deletes and queries tracker rows. Callers speak in column names;
// the service translates to storage keys and validates every value against its column.
type RowService struct {
	store repository.Store
	actor Actor
}

func NewRowService(store repository.Store, actor Actor) *RowService {
	return &RowService{store: store, actor: actor}
}

// NamedValues maps column names (as a person types them) to raw values.
type NamedValues map[string]any

// RowsResult pairs rows with the tracker they belong to, so values can be shown by name.
type RowsResult struct {
	Tracker domain.Tracker
	Rows    []domain.Row
}

// AddRows validates every row first and inserts all of them or none.
func (s *RowService) AddRows(ctx context.Context, ref TrackerRef, rows []NamedValues) (RowsResult, error) {
	if len(rows) == 0 {
		return RowsResult{}, domain.InvalidInputf("Pass at least one row to add.")
	}
	if len(rows) > domain.MaxRowsPerInsert {
		return RowsResult{}, domain.InvalidInputf("At most %d rows can be added per call (got %d); split them into batches.",
			domain.MaxRowsPerInsert, len(rows))
	}
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return RowsResult{}, err
	}

	problems := &domain.ValidationErrors{}
	prepared := make([]domain.RowValues, 0, len(rows))
	for i, input := range rows {
		values, rowProblems := prepareNewRow(tracker, input)
		for _, problem := range rowProblems {
			problems.Addf("row %d: %s", i+1, problem)
		}
		prepared = append(prepared, values)
	}
	if err := problems.AsError("Nothing was added because some rows have problems:"); err != nil {
		return RowsResult{}, err
	}

	inserted, err := s.store.Rows().Insert(ctx, tracker.ID, string(s.actor), prepared)
	if err != nil {
		return RowsResult{}, err
	}
	return RowsResult{Tracker: tracker, Rows: inserted}, nil
}

// prepareNewRow normalises one row, applies column defaults and checks required columns.
func prepareNewRow(tracker domain.Tracker, input NamedValues) (domain.RowValues, []string) {
	values, problems := normalizeNamedValues(tracker, input)
	for _, column := range tracker.Columns {
		if _, present := values[column.StorageKey()]; present {
			continue
		}
		if column.DefaultValue != nil {
			values[column.StorageKey()] = column.DefaultValue
			continue
		}
		if column.Required {
			problems = append(problems, "\""+column.Name+"\" is required")
		}
	}
	if len(values) == 0 && len(problems) == 0 {
		problems = append(problems, "it has no values")
	}
	return values, problems
}

// normalizeNamedValues maps names to columns and validates each value. Nil results (empty
// input) are left out.
func normalizeNamedValues(tracker domain.Tracker, input NamedValues) (domain.RowValues, []string) {
	values := make(domain.RowValues, len(input))
	var problems []string
	seen := make(map[int64]string, len(input))
	for _, name := range sortedKeys(input) {
		column, err := tracker.FindColumn(name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if earlier, duplicate := seen[column.ID]; duplicate {
			problems = append(problems, "\""+earlier+"\" and \""+name+"\" both refer to column \""+column.Name+"\"")
			continue
		}
		seen[column.ID] = name
		value, err := domain.NormalizeValue(column, input[name])
		if err != nil {
			problems = append(problems, "\""+column.Name+"\": "+err.Error())
			continue
		}
		if value != nil {
			values[column.StorageKey()] = value
		}
	}
	return values, problems
}

// UpdateRow changes only the given columns; a nil value clears that column.
func (s *RowService) UpdateRow(ctx context.Context, ref TrackerRef, rowID int64, changes NamedValues) (RowsResult, error) {
	if rowID <= 0 {
		return RowsResult{}, domain.InvalidInputf("row_id must be a positive number.")
	}
	if len(changes) == 0 {
		return RowsResult{}, domain.InvalidInputf("Pass at least one column value to change.")
	}
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return RowsResult{}, err
	}

	set, problems := normalizeNamedValues(tracker, changes)
	var unset []string
	for name, raw := range changes {
		column, err := tracker.FindColumn(name)
		if err != nil {
			continue // already reported by normalizeNamedValues
		}
		if _, isSet := set[column.StorageKey()]; isSet || !isEmptyInput(column, raw) {
			continue
		}
		if column.Required {
			problems = append(problems, "\""+column.Name+"\" is required and can't be cleared")
			continue
		}
		unset = append(unset, column.StorageKey())
	}
	validation := &domain.ValidationErrors{}
	for _, problem := range problems {
		validation.Addf("%s", problem)
	}
	if err := validation.AsError("The row was not changed:"); err != nil {
		return RowsResult{}, err
	}

	row, err := s.store.Rows().Patch(ctx, domain.RowPatch{TrackerID: tracker.ID, RowID: rowID, Set: set, Unset: unset})
	if errors.Is(err, repository.ErrNotFound) {
		return RowsResult{}, domain.NotFoundf("Tracker %q has no row with id %d. Use query_rows to find the right row.", tracker.Name, rowID)
	}
	if err != nil {
		return RowsResult{}, err
	}
	return RowsResult{Tracker: tracker, Rows: []domain.Row{row}}, nil
}

// isEmptyInput reports whether raw normalises to "no value" (null, "", []), i.e. a request
// to clear the column rather than an invalid value.
func isEmptyInput(column domain.Column, raw any) bool {
	value, err := domain.NormalizeValue(column, raw)
	return err == nil && value == nil
}

// DeleteRowsResult reports deleted rows (or those that would be deleted when unconfirmed)
// and ids that were not found in the tracker.
type DeleteRowsResult struct {
	Tracker    domain.Tracker
	Rows       []domain.Row
	MissingIDs []int64
	Confirmed  bool
}

// DeleteRows permanently removes rows by id. Without confirm it returns a preview only.
func (s *RowService) DeleteRows(ctx context.Context, ref TrackerRef, rowIDs []int64, confirm bool) (DeleteRowsResult, error) {
	ids, err := uniquePositiveIDs(rowIDs)
	if err != nil {
		return DeleteRowsResult{}, err
	}
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return DeleteRowsResult{}, err
	}

	result := DeleteRowsResult{Tracker: tracker}
	err = s.store.WithinTransaction(ctx, func(repos repository.Repositories) error {
		rows, err := repos.Rows().GetByIDs(ctx, tracker.ID, ids)
		if err != nil {
			return err
		}
		result.Rows, result.MissingIDs = rows, missingIDs(ids, rows)
		if !confirm || len(rows) == 0 {
			return nil
		}
		existing := make([]int64, len(rows))
		for i, row := range rows {
			existing[i] = row.ID
		}
		if _, err := repos.Rows().Delete(ctx, tracker.ID, existing); err != nil {
			return err
		}
		result.Confirmed = true
		return nil
	})
	if err != nil {
		return DeleteRowsResult{}, err
	}
	if len(result.Rows) == 0 {
		return DeleteRowsResult{}, domain.NotFoundf("None of the row ids %v exist in tracker %q.", ids, tracker.Name)
	}
	return result, nil
}

// FilterInput is one condition as a person or assistant writes it.
type FilterInput struct {
	Column   string
	Operator string
	Value    any
}

// SortInput orders results by a column.
type SortInput struct {
	Column     string
	Descending bool
}

// QueryRowsInput asks for a page of rows.
type QueryRowsInput struct {
	Filters []FilterInput
	Match   string
	Sorts   []SortInput
	Limit   int
	Offset  int
}

// QueryRowsResult is a page of matching rows.
type QueryRowsResult struct {
	Tracker       domain.Tracker
	Rows          []domain.Row
	TotalMatching int64
	Limit         int
	Offset        int
}

func (s *RowService) QueryRows(ctx context.Context, ref TrackerRef, input QueryRowsInput) (QueryRowsResult, error) {
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return QueryRowsResult{}, err
	}
	filter, err := buildRowFilter(tracker, input.Filters, input.Match)
	if err != nil {
		return QueryRowsResult{}, err
	}
	sorts, err := buildSorts(tracker, input.Sorts)
	if err != nil {
		return QueryRowsResult{}, err
	}
	if input.Offset < 0 {
		return QueryRowsResult{}, domain.InvalidInputf("offset can't be negative.")
	}
	limit := input.Limit
	switch {
	case limit <= 0:
		limit = domain.DefaultQueryLimit
	case limit > domain.MaxQueryLimit:
		limit = domain.MaxQueryLimit
	}

	page, err := s.store.Rows().Query(ctx, tracker.ID, domain.RowQuery{Filter: filter, Sorts: sorts, Limit: limit, Offset: input.Offset})
	if err != nil {
		return QueryRowsResult{}, err
	}
	return QueryRowsResult{Tracker: tracker, Rows: page.Rows, TotalMatching: page.TotalMatching, Limit: limit, Offset: input.Offset}, nil
}

// SummarizeRowsResult counts rows per value of one column.
type SummarizeRowsResult struct {
	Tracker   domain.Tracker
	GroupBy   domain.Column
	Groups    []domain.GroupCount
	Truncated bool
}

func (s *RowService) SummarizeRows(ctx context.Context, ref TrackerRef, groupBy string, filters []FilterInput, match string) (SummarizeRowsResult, error) {
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return SummarizeRowsResult{}, err
	}
	column, err := tracker.FindColumn(groupBy)
	if err != nil {
		return SummarizeRowsResult{}, err
	}
	filter, err := buildRowFilter(tracker, filters, match)
	if err != nil {
		return SummarizeRowsResult{}, err
	}
	groups, err := s.store.Rows().Summarize(ctx, tracker.ID, column, filter)
	if err != nil {
		return SummarizeRowsResult{}, err
	}
	result := SummarizeRowsResult{Tracker: tracker, GroupBy: column, Groups: groups}
	if len(groups) > domain.MaxSummaryGroups {
		result.Groups, result.Truncated = groups[:domain.MaxSummaryGroups], true
	}
	return result, nil
}

func buildRowFilter(tracker domain.Tracker, inputs []FilterInput, match string) (domain.RowFilter, error) {
	mode, err := domain.ParseMatchMode(match)
	if err != nil {
		return domain.RowFilter{}, err
	}
	if len(inputs) > domain.MaxFiltersPerQuery {
		return domain.RowFilter{}, domain.InvalidInputf("At most %d filters are allowed per query.", domain.MaxFiltersPerQuery)
	}
	filter := domain.RowFilter{Match: mode, Conditions: make([]domain.Condition, 0, len(inputs))}
	for _, input := range inputs {
		field, err := tracker.FindField(input.Column)
		if err != nil {
			return domain.RowFilter{}, err
		}
		operator, err := domain.ParseOperator(input.Operator)
		if err != nil {
			return domain.RowFilter{}, err
		}
		condition, err := domain.NewCondition(field, operator, input.Value)
		if err != nil {
			return domain.RowFilter{}, err
		}
		filter.Conditions = append(filter.Conditions, condition)
	}
	return filter, nil
}

func buildSorts(tracker domain.Tracker, inputs []SortInput) ([]domain.Sort, error) {
	if len(inputs) > domain.MaxSortsPerQuery {
		return nil, domain.InvalidInputf("At most %d sort columns are allowed.", domain.MaxSortsPerQuery)
	}
	sorts := make([]domain.Sort, 0, len(inputs))
	for _, input := range inputs {
		field, err := tracker.FindField(input.Column)
		if err != nil {
			return nil, err
		}
		sorts = append(sorts, domain.Sort{Field: field, Descending: input.Descending})
	}
	return sorts, nil
}

func uniquePositiveIDs(rowIDs []int64) ([]int64, error) {
	if len(rowIDs) == 0 {
		return nil, domain.InvalidInputf("Pass at least one row id.")
	}
	if len(rowIDs) > domain.MaxRowIDsPerCall {
		return nil, domain.InvalidInputf("At most %d row ids can be passed per call.", domain.MaxRowIDsPerCall)
	}
	seen := make(map[int64]bool, len(rowIDs))
	ids := make([]int64, 0, len(rowIDs))
	for _, id := range rowIDs {
		if id <= 0 {
			return nil, domain.InvalidInputf("Row ids must be positive numbers (got %d).", id)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func missingIDs(requested []int64, found []domain.Row) []int64 {
	present := make(map[int64]bool, len(found))
	for _, row := range found {
		present[row.ID] = true
	}
	var missing []int64
	for _, id := range requested {
		if !present[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

func sortedKeys(values NamedValues) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.ToLower(keys[i]) < strings.ToLower(keys[j]) })
	return keys
}
