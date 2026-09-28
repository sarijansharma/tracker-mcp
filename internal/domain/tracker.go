package domain

import (
	"fmt"
	"strings"
	"time"
)

// Tracker is a user-defined table: a name plus an ordered set of columns.
type Tracker struct {
	ID          int64
	Name        string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
	Columns     []Column
}

// IsDeleted reports whether the tracker has been (soft) deleted.
func (t Tracker) IsDeleted() bool { return t.DeletedAt != nil }

// ColumnNames returns column names in display order.
func (t Tracker) ColumnNames() []string {
	names := make([]string, len(t.Columns))
	for i, column := range t.Columns {
		names[i] = column.Name
	}
	return names
}

// FindColumn looks a column up by name (case and whitespace insensitive). The error lists
// what is available and suggests close spellings.
func (t Tracker) FindColumn(name string) (Column, error) {
	for _, column := range t.Columns {
		if SameName(column.Name, name) {
			return column, nil
		}
	}
	return Column{}, t.unknownColumnError(name)
}

// FindColumnByKey looks a column up by its storage key.
func (t Tracker) FindColumnByKey(key string) (Column, bool) {
	for _, column := range t.Columns {
		if column.StorageKey() == key {
			return column, true
		}
	}
	return Column{}, false
}

// FindField resolves a filter/sort target: a built-in field (id, created_at, updated_at)
// or a user column.
func (t Tracker) FindField(name string) (Field, error) {
	switch SystemField(strings.ToLower(strings.ReplaceAll(CleanName(name), " ", "_"))) {
	case SystemFieldID:
		return Field{System: SystemFieldID}, nil
	case SystemFieldCreatedAt:
		return Field{System: SystemFieldCreatedAt}, nil
	case SystemFieldUpdatedAt:
		return Field{System: SystemFieldUpdatedAt}, nil
	}
	column, err := t.FindColumn(name)
	if err != nil {
		return Field{}, err
	}
	return Field{Column: &column}, nil
}

func (t Tracker) unknownColumnError(name string) *Error {
	available := "it has no columns yet"
	if len(t.Columns) > 0 {
		available = "available columns: " + strings.Join(QuoteAll(t.ColumnNames()), ", ")
	}
	return NotFoundf("Tracker %q has no column named %q (%s).", t.Name, CleanName(name), available).
		WithSuggestions(QuoteAll(SuggestSimilar(name, t.ColumnNames(), 2))...)
}

// TrackerSummary is the lightweight view used in listings and "did you mean" hints.
type TrackerSummary struct {
	ID          int64
	Name        string
	Description string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
	ColumnCount int64
	RowCount    int64
}

// Hint renders the summary so a person can tell similarly named trackers apart.
func (s TrackerSummary) Hint() string {
	hint := fmt.Sprintf("%q (id %d, created %s, %d rows", s.Name, s.ID, s.CreatedAt.Format(DateLayout), s.RowCount)
	if s.DeletedAt != nil {
		hint += ", deleted " + s.DeletedAt.Format(DateLayout)
	}
	return hint + ")"
}

// TrackerListFilter narrows list_trackers.
type TrackerListFilter struct {
	Search         string
	IncludeDeleted bool
}
