package postgres

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
)

// sqlArgs accumulates bind parameters and hands out their placeholders. Every value and
// every JSON key goes through here; no user text is ever concatenated into SQL.
type sqlArgs struct {
	values []any
}

func (a *sqlArgs) add(value any) string {
	a.values = append(a.values, value)
	return "$" + strconv.Itoa(len(a.values))
}

// buildWhere renders the row filter for one tracker as a SQL boolean expression.
func buildWhere(trackerID int64, filter domain.RowFilter, args *sqlArgs) (string, error) {
	where := "tracker_id = " + args.add(trackerID)
	if len(filter.Conditions) == 0 {
		return where, nil
	}
	predicates := make([]string, 0, len(filter.Conditions))
	for _, condition := range filter.Conditions {
		predicate, err := buildCondition(condition, args)
		if err != nil {
			return "", err
		}
		predicates = append(predicates, "("+predicate+")")
	}
	joiner := " AND "
	if filter.Match == domain.MatchAny {
		joiner = " OR "
	}
	return where + " AND (" + strings.Join(predicates, joiner) + ")", nil
}

func buildCondition(condition domain.Condition, args *sqlArgs) (string, error) {
	if condition.Field.Column == nil {
		return buildSystemCondition(condition, args)
	}
	key := args.add(condition.Field.Column.StorageKey()) + "::text"
	jsonValue := "data->" + key
	textValue := "data->>" + key

	if condition.Operator == domain.OperatorIsEmpty {
		return jsonValue + " IS NULL", nil
	}
	if condition.Operator == domain.OperatorIsNotEmpty {
		return jsonValue + " IS NOT NULL", nil
	}

	positive, err := buildPositiveColumnCondition(condition.Field.ValueKind(), condition.Operator.Positive(),
		condition.Value, key, jsonValue, textValue, args)
	if err != nil {
		return "", err
	}
	if condition.Operator.IsNegative() {
		// Negative filters also match rows where the field is empty.
		return fmt.Sprintf("%s IS NULL OR NOT (%s)", jsonValue, positive), nil
	}
	return positive, nil
}

func buildPositiveColumnCondition(kind domain.ValueKind, operator domain.Operator, value any,
	key, jsonValue, textValue string, args *sqlArgs,
) (string, error) {
	switch kind {
	case domain.ValueKindText:
		return buildTextCondition(operator, value, textValue, args)
	case domain.ValueKindOption:
		switch operator {
		case domain.OperatorEquals:
			return fmt.Sprintf("data @> jsonb_build_object(%s, %s::text)", key, args.add(value)), nil
		case domain.OperatorIn:
			return fmt.Sprintf("%s = ANY(%s::text[])", textValue, args.add(toStrings(value))), nil
		default: // unsupported; reported below
		}
	case domain.ValueKindOptionList:
		switch operator {
		case domain.OperatorContains:
			return fmt.Sprintf("data @> jsonb_build_object(%s, jsonb_build_array(%s::text))", key, args.add(value)), nil
		case domain.OperatorIn:
			return fmt.Sprintf("EXISTS (SELECT 1 FROM jsonb_array_elements_text(%s) AS selected(option) WHERE selected.option = ANY(%s::text[]))",
				jsonValue, args.add(toStrings(value))), nil
		default: // unsupported; reported below
		}
	case domain.ValueKindNumber:
		return buildComparison(operator, "("+textValue+")::numeric", "numeric", value, args)
	case domain.ValueKindBoolean:
		if operator == domain.OperatorEquals {
			return fmt.Sprintf("data @> jsonb_build_object(%s, %s::boolean)", key, args.add(value)), nil
		}
	case domain.ValueKindTemporal:
		// Stored as fixed-format ISO strings, which sort chronologically as text.
		return buildComparison(operator, textValue, "text", value, args)
	}
	return "", fmt.Errorf("operator %q is not supported for %s values", operator, kind)
}

func buildTextCondition(operator domain.Operator, value any, textValue string, args *sqlArgs) (string, error) {
	lowered := "lower(" + textValue + ")"
	switch operator {
	case domain.OperatorEquals:
		return fmt.Sprintf("%s = lower(%s::text)", lowered, args.add(value)), nil
	case domain.OperatorContains:
		// strpos instead of LIKE, so %, _ and \ in the search text have no special meaning.
		return fmt.Sprintf("strpos(%s, lower(%s::text)) > 0", lowered, args.add(value)), nil
	case domain.OperatorStartsWith:
		return fmt.Sprintf("starts_with(%s, lower(%s::text))", lowered, args.add(value)), nil
	case domain.OperatorIn:
		values := toStrings(value)
		for i := range values {
			values[i] = strings.ToLower(values[i])
		}
		return fmt.Sprintf("%s = ANY(%s::text[])", lowered, args.add(values)), nil
	default: // unsupported; reported below
	}
	return "", fmt.Errorf("operator %q is not supported for text values", operator)
}

// buildComparison handles eq/gt/gte/lt/lte/in over an already typed SQL expression.
func buildComparison(operator domain.Operator, expression, sqlType string, value any, args *sqlArgs) (string, error) {
	comparators := map[domain.Operator]string{
		domain.OperatorEquals:         "=",
		domain.OperatorGreaterThan:    ">",
		domain.OperatorGreaterOrEqual: ">=",
		domain.OperatorLessThan:       "<",
		domain.OperatorLessOrEqual:    "<=",
	}
	if comparator, ok := comparators[operator]; ok {
		return fmt.Sprintf("%s %s %s::%s", expression, comparator, args.add(value), sqlType), nil
	}
	if operator == domain.OperatorIn {
		return fmt.Sprintf("%s = ANY(%s::%s[])", expression, args.add(typedList(value, sqlType)), sqlType), nil
	}
	return "", fmt.Errorf("operator %q is not supported here", operator)
}

func buildSystemCondition(condition domain.Condition, args *sqlArgs) (string, error) {
	column, sqlType := "id", "bigint"
	switch condition.Field.System {
	case domain.SystemFieldID:
		// Row ids are whole numbers; the number codec produced float64 values.
		condition.Value = toInt64s(condition.Value)
	case domain.SystemFieldCreatedAt:
		column, sqlType = "created_at", "timestamptz"
		condition.Value = toTime(condition.Value)
	case domain.SystemFieldUpdatedAt:
		column, sqlType = "updated_at", "timestamptz"
		condition.Value = toTime(condition.Value)
	default:
		return "", fmt.Errorf("unknown system field %q", condition.Field.System)
	}
	positive, err := buildComparison(condition.Operator.Positive(), column, sqlType, condition.Value, args)
	if err != nil {
		return "", err
	}
	if condition.Operator.IsNegative() {
		return "NOT (" + positive + ")", nil
	}
	return positive, nil
}

// buildOrderBy renders sorts, always ending with id so paging is stable.
func buildOrderBy(sorts []domain.Sort, args *sqlArgs) string {
	terms := make([]string, 0, len(sorts)+1)
	for _, sort := range sorts {
		direction := "ASC"
		if sort.Descending {
			direction = "DESC"
		}
		terms = append(terms, sortExpression(sort.Field, args)+" "+direction+" NULLS LAST")
	}
	terms = append(terms, "id ASC")
	return strings.Join(terms, ", ")
}

func sortExpression(field domain.Field, args *sqlArgs) string {
	if field.Column == nil {
		return string(field.System) // one of three fixed identifiers, never user text
	}
	textValue := "data->>" + args.add(field.Column.StorageKey()) + "::text"
	switch field.ValueKind() {
	case domain.ValueKindNumber:
		return "(" + textValue + ")::numeric"
	case domain.ValueKindBoolean:
		return "(" + textValue + ")::boolean"
	case domain.ValueKindText, domain.ValueKindOption:
		return "lower(" + textValue + ")"
	default:
		return textValue
	}
}

func toStrings(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprint(item))
	}
	return result
}

// typedList converts a []any operand into a slice pgx can encode as a Postgres array.
func typedList(value any, sqlType string) any {
	items, _ := value.([]any)
	switch sqlType {
	case "numeric":
		numbers := make([]float64, 0, len(items))
		for _, item := range items {
			if number, ok := item.(float64); ok {
				numbers = append(numbers, number)
			}
		}
		return numbers
	case "bigint":
		ids := make([]int64, 0, len(items))
		for _, item := range items {
			if id, ok := item.(int64); ok {
				ids = append(ids, id)
			}
		}
		return ids
	default:
		return toStrings(value)
	}
}

// toTime parses the canonical datetime string produced by the domain layer.
func toTime(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	parsed, err := time.Parse(domain.DateTimeLayout, text)
	if err != nil {
		return value
	}
	return parsed
}

func toInt64s(value any) any {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = toInt64s(item)
		}
		return result
	default:
		return value
	}
}
