package domain

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// ColumnType is the kind of data a tracker column holds.
type ColumnType string

const (
	ColumnTypeText         ColumnType = "text"
	ColumnTypeNumber       ColumnType = "number"
	ColumnTypeBoolean      ColumnType = "boolean"
	ColumnTypeDate         ColumnType = "date"
	ColumnTypeDateTime     ColumnType = "datetime"
	ColumnTypeSingleSelect ColumnType = "single_select"
	ColumnTypeMultiSelect  ColumnType = "multi_select"
	ColumnTypeEmail        ColumnType = "email"
	ColumnTypeURL          ColumnType = "url"
)

// columnTypeAliases lets people describe types in their own words ("dropdown", "checkbox").
var columnTypeAliases = map[string]ColumnType{
	"string": ColumnTypeText, "str": ColumnTypeText, "varchar": ColumnTypeText, "note": ColumnTypeText, "notes": ColumnTypeText,
	"int": ColumnTypeNumber, "integer": ColumnTypeNumber, "float": ColumnTypeNumber, "decimal": ColumnTypeNumber,
	"numeric": ColumnTypeNumber, "amount": ColumnTypeNumber, "currency": ColumnTypeNumber,
	"bool": ColumnTypeBoolean, "checkbox": ColumnTypeBoolean, "yes_no": ColumnTypeBoolean,
	"timestamp": ColumnTypeDateTime, "date_time": ColumnTypeDateTime,
	"select": ColumnTypeSingleSelect, "dropdown": ColumnTypeSingleSelect, "enum": ColumnTypeSingleSelect,
	"status": ColumnTypeSingleSelect, "choice": ColumnTypeSingleSelect,
	"multiselect": ColumnTypeMultiSelect, "tags": ColumnTypeMultiSelect, "labels": ColumnTypeMultiSelect,
	"mail": ColumnTypeEmail, "link": ColumnTypeURL, "website": ColumnTypeURL,
}

// AllColumnTypes lists the supported types in a stable, documentation friendly order.
func AllColumnTypes() []ColumnType {
	return []ColumnType{
		ColumnTypeText, ColumnTypeNumber, ColumnTypeBoolean, ColumnTypeDate, ColumnTypeDateTime,
		ColumnTypeSingleSelect, ColumnTypeMultiSelect, ColumnTypeEmail, ColumnTypeURL,
	}
}

// ParseColumnType accepts a canonical type name or a common alias.
func ParseColumnType(raw string) (ColumnType, error) {
	key := strings.ToLower(strings.ReplaceAll(CleanName(raw), " ", "_"))
	key = strings.ReplaceAll(key, "-", "_")
	candidate := ColumnType(key)
	if _, ok := codecs[candidate]; ok {
		return candidate, nil
	}
	if aliased, ok := columnTypeAliases[key]; ok {
		return aliased, nil
	}
	names := make([]string, 0, len(codecs))
	for _, t := range AllColumnTypes() {
		names = append(names, string(t))
	}
	return "", InvalidInputf("Unknown column type %q. Supported types: %s.", raw, strings.Join(names, ", ")).
		WithSuggestions(SuggestSimilar(key, names, 2)...)
}

// HasOptions reports whether the type is chosen from a fixed list of options.
func (t ColumnType) HasOptions() bool {
	return t == ColumnTypeSingleSelect || t == ColumnTypeMultiSelect
}

// ValueKind is how values of the type are stored and compared.
func (t ColumnType) ValueKind() ValueKind {
	return codecs[t].kind()
}

// SupportedOperators lists the filter operators that make sense for the type.
func (t ColumnType) SupportedOperators() []Operator {
	return codecs[t].operators()
}

// Column is one user-defined field of a tracker.
type Column struct {
	ID          int64
	TrackerID   int64
	Name        string
	Type        ColumnType
	Description string
	Required    bool
	Options     []string
	// DefaultValue is applied to new rows that omit the column, and used to backfill
	// existing rows when a column is added or made required. Nil means no default.
	DefaultValue any
	Position     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StorageKey is the immutable JSON key under which row values for this column are stored.
// Using the id (not the name) makes renames free and avoids any user text in queries.
func (c Column) StorageKey() string {
	return strconv.FormatInt(c.ID, 10)
}

// FindOption returns the canonical spelling of an option, matched case-insensitively.
func (c Column) FindOption(raw string) (string, bool) {
	cleaned := CleanName(raw)
	for _, option := range c.Options {
		if strings.EqualFold(option, cleaned) {
			return option, true
		}
	}
	return "", false
}

// ReservedFieldNames are built-in row fields; user columns can't use these names.
var ReservedFieldNames = []string{"id", "created_at", "updated_at", "created_by"}

// IsReservedFieldName reports whether name clashes with a built-in row field.
func IsReservedFieldName(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(CleanName(name), " ", "_"))
	for _, reserved := range ReservedFieldNames {
		if normalized == reserved {
			return true
		}
	}
	return false
}

// ColumnSpec is a request to define a new column, before validation.
type ColumnSpec struct {
	Name         string
	Type         string
	Description  string
	Required     bool
	Options      []string
	DefaultValue any
}

// Validate cleans the spec and turns it into a Column that is ready to be stored
// (ID, TrackerID and Position are filled in by the caller).
func (s ColumnSpec) Validate() (Column, error) {
	name, err := ValidateName("column name", s.Name, MaxColumnNameLength)
	if err != nil {
		return Column{}, err
	}
	if IsReservedFieldName(name) {
		return Column{}, InvalidInputf("%q is a built-in field name (%s); please choose another column name.",
			name, strings.Join(ReservedFieldNames, ", "))
	}
	columnType, err := ParseColumnType(s.Type)
	if err != nil {
		return Column{}, err
	}
	description := strings.TrimSpace(s.Description)
	if len(description) > MaxColumnDescriptionLength {
		return Column{}, InvalidInputf("The description of column %q is too long (max %d characters).", name, MaxColumnDescriptionLength)
	}
	column := Column{Name: name, Type: columnType, Description: description, Required: s.Required}

	if columnType.HasOptions() {
		column.Options, err = CleanOptions(name, s.Options)
		if err != nil {
			return Column{}, err
		}
		if len(column.Options) == 0 {
			return Column{}, InvalidInputf("Column %q is a %s column, so it needs at least one option (e.g. [\"Open\", \"Done\"]).", name, columnType)
		}
	} else if len(s.Options) > 0 {
		return Column{}, InvalidInputf("Column %q is a %s column; options are only allowed on single_select or multi_select columns.", name, columnType)
	}

	if s.DefaultValue != nil {
		column.DefaultValue, err = NormalizeValue(column, s.DefaultValue)
		if err != nil {
			return Column{}, InvalidInputf("Default value for column %q is invalid: %v", name, err)
		}
	}
	return column, nil
}

// CleanOptions trims options and rejects blanks and case-insensitive duplicates.
func CleanOptions(columnName string, rawOptions []string) ([]string, error) {
	if len(rawOptions) > MaxOptionsPerColumn {
		return nil, InvalidInputf("Column %q has too many options (max %d).", columnName, MaxOptionsPerColumn)
	}
	seen := make(map[string]bool, len(rawOptions))
	options := make([]string, 0, len(rawOptions))
	for _, raw := range rawOptions {
		option := CleanName(raw)
		if option == "" {
			return nil, InvalidInputf("Column %q has an empty option.", columnName)
		}
		if len(option) > MaxOptionLength {
			return nil, InvalidInputf("Option %q of column %q is too long (max %d characters).", option, columnName, MaxOptionLength)
		}
		key := strings.ToLower(option)
		if seen[key] {
			return nil, InvalidInputf("Column %q lists the option %q more than once.", columnName, option)
		}
		seen[key] = true
		options = append(options, option)
	}
	return options, nil
}

// SortColumns orders columns by position, then id, the order they are shown to users.
func SortColumns(columns []Column) {
	sort.SliceStable(columns, func(i, j int) bool {
		if columns[i].Position != columns[j].Position {
			return columns[i].Position < columns[j].Position
		}
		return columns[i].ID < columns[j].ID
	})
}
