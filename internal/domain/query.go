package domain

import (
	"fmt"
	"strings"
)

// Operator is a comparison used in row filters.
type Operator string

const (
	OperatorEquals         Operator = "eq"
	OperatorNotEquals      Operator = "neq"
	OperatorGreaterThan    Operator = "gt"
	OperatorGreaterOrEqual Operator = "gte"
	OperatorLessThan       Operator = "lt"
	OperatorLessOrEqual    Operator = "lte"
	OperatorContains       Operator = "contains"
	OperatorNotContains    Operator = "not_contains"
	OperatorStartsWith     Operator = "starts_with"
	OperatorIn             Operator = "in"
	OperatorNotIn          Operator = "not_in"
	OperatorIsEmpty        Operator = "is_empty"
	OperatorIsNotEmpty     Operator = "is_not_empty"
)

var operatorAliases = map[string]Operator{
	"=": OperatorEquals, "==": OperatorEquals, "equals": OperatorEquals, "is": OperatorEquals,
	"!=": OperatorNotEquals, "<>": OperatorNotEquals, "ne": OperatorNotEquals, "not_equals": OperatorNotEquals, "is_not": OperatorNotEquals,
	">": OperatorGreaterThan, ">=": OperatorGreaterOrEqual, "<": OperatorLessThan, "<=": OperatorLessOrEqual,
	"after": OperatorGreaterThan, "before": OperatorLessThan,
	"like": OperatorContains, "includes": OperatorContains, "has": OperatorContains,
	"not_like": OperatorNotContains, "excludes": OperatorNotContains,
	"one_of": OperatorIn, "any_of": OperatorIn, "none_of": OperatorNotIn,
	"empty": OperatorIsEmpty, "is_null": OperatorIsEmpty, "not_empty": OperatorIsNotEmpty, "is_not_null": OperatorIsNotEmpty,
}

var allOperators = []Operator{
	OperatorEquals, OperatorNotEquals, OperatorGreaterThan, OperatorGreaterOrEqual, OperatorLessThan,
	OperatorLessOrEqual, OperatorContains, OperatorNotContains, OperatorStartsWith, OperatorIn,
	OperatorNotIn, OperatorIsEmpty, OperatorIsNotEmpty,
}

// ParseOperator accepts canonical operator names and common symbols/aliases.
func ParseOperator(raw string) (Operator, error) {
	key := strings.ToLower(strings.ReplaceAll(CleanName(raw), " ", "_"))
	for _, op := range allOperators {
		if string(op) == key {
			return op, nil
		}
	}
	if op, ok := operatorAliases[key]; ok {
		return op, nil
	}
	return "", InvalidInputf("Unknown filter operator %q. Supported: %s.", raw, joinOperators(allOperators))
}

// IsNegative reports whether the operator excludes matches. Negative operators also match
// rows where the field is empty ("status is not Rejected" includes rows with no status yet).
func (o Operator) IsNegative() bool {
	return o == OperatorNotEquals || o == OperatorNotContains || o == OperatorNotIn
}

// Positive returns the operator a negative operator negates (neq -> eq).
func (o Operator) Positive() Operator {
	switch o {
	case OperatorNotEquals:
		return OperatorEquals
	case OperatorNotContains:
		return OperatorContains
	case OperatorNotIn:
		return OperatorIn
	default:
		return o
	}
}

// TakesList reports whether the operand is a list of values.
func (o Operator) TakesList() bool { return o == OperatorIn || o == OperatorNotIn }

// TakesNoValue reports whether the operator ignores the operand.
func (o Operator) TakesNoValue() bool { return o == OperatorIsEmpty || o == OperatorIsNotEmpty }

func joinOperators(operators []Operator) string {
	names := make([]string, len(operators))
	for i, op := range operators {
		names[i] = string(op)
	}
	return strings.Join(names, ", ")
}

// SystemField is a built-in field every row has.
type SystemField string

const (
	SystemFieldID        SystemField = "id"
	SystemFieldCreatedAt SystemField = "created_at"
	SystemFieldUpdatedAt SystemField = "updated_at"
)

// Field is what a filter or sort targets: a user column or a built-in row field.
type Field struct {
	Column *Column
	System SystemField
}

// Name is the field's display name.
func (f Field) Name() string {
	if f.Column != nil {
		return f.Column.Name
	}
	return string(f.System)
}

// ValueKind is how the field is compared.
func (f Field) ValueKind() ValueKind {
	if f.Column != nil {
		return f.Column.Type.ValueKind()
	}
	if f.System == SystemFieldID {
		return ValueKindNumber
	}
	return ValueKindTemporal
}

// SupportedOperators lists the operators valid for the field.
func (f Field) SupportedOperators() []Operator {
	if f.Column != nil {
		return f.Column.Type.SupportedOperators()
	}
	if f.System == SystemFieldID {
		return []Operator{OperatorEquals, OperatorNotEquals, OperatorGreaterThan, OperatorGreaterOrEqual,
			OperatorLessThan, OperatorLessOrEqual, OperatorIn, OperatorNotIn}
	}
	return []Operator{OperatorGreaterThan, OperatorGreaterOrEqual, OperatorLessThan, OperatorLessOrEqual}
}

// Condition is one validated filter. Value is already normalised for the field
// (a slice for list operators, nil for is_empty/is_not_empty).
type Condition struct {
	Field    Field
	Operator Operator
	Value    any
}

// NewCondition validates the operator against the field and normalises the operand.
func NewCondition(field Field, operator Operator, rawValue any) (Condition, error) {
	if !containsOperator(field.SupportedOperators(), operator) {
		return Condition{}, InvalidInputf("The %q operator can't be used on %q. Allowed operators: %s.",
			operator, field.Name(), joinOperators(field.SupportedOperators()))
	}
	condition := Condition{Field: field, Operator: operator}
	if operator.TakesNoValue() {
		return condition, nil
	}
	if rawValue == nil {
		return Condition{}, InvalidInputf("The filter on %q with operator %q needs a value.", field.Name(), operator)
	}
	if !operator.TakesList() {
		value, err := normalizeOperand(field, rawValue)
		if err != nil {
			return Condition{}, InvalidInputf("Filter value for %q is invalid: %v", field.Name(), err)
		}
		condition.Value = value
		return condition, nil
	}

	items, isList := rawValue.([]any)
	if !isList {
		items = []any{rawValue}
	}
	values := make([]any, 0, len(items))
	for _, item := range items {
		value, err := normalizeOperand(field, item)
		if err != nil {
			return Condition{}, InvalidInputf("Filter value for %q is invalid: %v", field.Name(), err)
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return Condition{}, InvalidInputf("The %q filter on %q needs at least one value.", operator, field.Name())
	}
	condition.Value = values
	return condition, nil
}

// normalizeOperand converts one filter operand. Text-like fields accept any text (a partial
// email is a valid "contains" operand); option fields must name a real option.
func normalizeOperand(field Field, raw any) (any, error) {
	var value any
	var err error
	switch {
	case field.Column == nil && field.System == SystemFieldID:
		value, err = numberCodec{}.normalize(raw, Column{})
	case field.Column == nil:
		value, err = dateTimeCodec{}.normalize(raw, Column{})
	case field.ValueKind() == ValueKindText:
		value, err = textCodec{}.normalize(raw, *field.Column)
	case field.ValueKind() == ValueKindOptionList:
		value, err = singleSelectCodec{}.normalize(raw, *field.Column)
	default:
		value, err = NormalizeValue(*field.Column, raw)
	}
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("the value can't be empty (use is_empty / is_not_empty to find blank fields)")
	}
	return value, nil
}

func containsOperator(operators []Operator, target Operator) bool {
	for _, op := range operators {
		if op == target {
			return true
		}
	}
	return false
}

// MatchMode decides how multiple conditions combine.
type MatchMode string

const (
	MatchAll MatchMode = "all"
	MatchAny MatchMode = "any"
)

// ParseMatchMode defaults to MatchAll.
func ParseMatchMode(raw string) (MatchMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all", "and":
		return MatchAll, nil
	case "any", "or":
		return MatchAny, nil
	default:
		return "", InvalidInputf("match must be \"all\" or \"any\", got %q.", raw)
	}
}

// Sort orders query results by one field.
type Sort struct {
	Field      Field
	Descending bool
}

// RowFilter is a validated set of conditions.
type RowFilter struct {
	Conditions []Condition
	Match      MatchMode
}

// RowQuery is a validated page request over a tracker's rows.
type RowQuery struct {
	Filter RowFilter
	Sorts  []Sort
	Limit  int
	Offset int
}

// RowPage is one page of query results.
type RowPage struct {
	Rows          []Row
	TotalMatching int64
}

// GroupCount is one bucket of a summary: how many rows have Value in the grouped column.
// Value is nil for rows where the column is empty.
type GroupCount struct {
	Value *string
	Count int64
}
