package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ValueKind describes how values are stored in JSON and compared in queries. Several column
// types share a kind (text, email and url are all compared as text).
type ValueKind string

const (
	ValueKindText       ValueKind = "text"        // JSON string, compared case-insensitively
	ValueKindNumber     ValueKind = "number"      // JSON number
	ValueKindBoolean    ValueKind = "boolean"     // JSON boolean
	ValueKindTemporal   ValueKind = "temporal"    // ISO-8601 JSON string, sortable as text
	ValueKindOption     ValueKind = "option"      // JSON string, one of Column.Options
	ValueKindOptionList ValueKind = "option_list" // JSON array of strings from Column.Options
)

// Canonical storage layouts for temporal values. Both sort correctly as plain strings.
const (
	DateLayout     = "2006-01-02"
	DateTimeLayout = "2006-01-02T15:04:05Z"
)

// valueCodec validates and normalises values for one column type. Adding a column type
// means adding a codec here; nothing else in the domain needs to change.
type valueCodec interface {
	kind() ValueKind
	operators() []Operator
	// normalize converts user input to the canonical stored value. A nil result means
	// "empty"; empty values are never stored.
	normalize(raw any, column Column) (any, error)
}

var codecs = map[ColumnType]valueCodec{
	ColumnTypeText:         textCodec{},
	ColumnTypeEmail:        emailCodec{},
	ColumnTypeURL:          urlCodec{},
	ColumnTypeNumber:       numberCodec{},
	ColumnTypeBoolean:      booleanCodec{},
	ColumnTypeDate:         dateCodec{},
	ColumnTypeDateTime:     dateTimeCodec{},
	ColumnTypeSingleSelect: singleSelectCodec{},
	ColumnTypeMultiSelect:  multiSelectCodec{},
}

// NormalizeValue validates raw input for the column and returns the value to store
// (nil when the input is empty).
func NormalizeValue(column Column, raw any) (any, error) {
	codec, ok := codecs[column.Type]
	if !ok {
		return nil, fmt.Errorf("column type %q is not supported", column.Type)
	}
	if raw == nil {
		return nil, nil
	}
	return codec.normalize(raw, column)
}

// ---- text-like ----

var textOperators = []Operator{
	OperatorEquals, OperatorNotEquals, OperatorContains, OperatorNotContains, OperatorStartsWith,
	OperatorIn, OperatorNotIn, OperatorIsEmpty, OperatorIsNotEmpty,
}

type textCodec struct{}

func (textCodec) kind() ValueKind       { return ValueKindText }
func (textCodec) operators() []Operator { return textOperators }
func (textCodec) normalize(raw any, _ Column) (any, error) {
	text, err := scalarToString(raw)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(text) > MaxTextValueLength {
		return nil, fmt.Errorf("text is too long (max %d characters)", MaxTextValueLength)
	}
	return text, nil
}

type emailCodec struct{}

func (emailCodec) kind() ValueKind       { return ValueKindText }
func (emailCodec) operators() []Operator { return textOperators }
func (emailCodec) normalize(raw any, _ Column) (any, error) {
	text, err := scalarToString(raw)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	address, err := mail.ParseAddress(text)
	if err != nil || !strings.Contains(address.Address, ".") {
		return nil, fmt.Errorf("%q is not a valid email address", text)
	}
	return strings.ToLower(address.Address), nil
}

type urlCodec struct{}

func (urlCodec) kind() ValueKind       { return ValueKindText }
func (urlCodec) operators() []Operator { return textOperators }
func (urlCodec) normalize(raw any, _ Column) (any, error) {
	text, err := scalarToString(raw)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if !strings.Contains(text, "://") {
		text = "https://" + text
	}
	parsed, err := url.ParseRequestURI(text)
	if err != nil || parsed.Host == "" || !strings.Contains(parsed.Host, ".") ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%q is not a valid http(s) link", text)
	}
	return parsed.String(), nil
}

// ---- number ----

type numberCodec struct{}

func (numberCodec) kind() ValueKind { return ValueKindNumber }
func (numberCodec) operators() []Operator {
	return []Operator{
		OperatorEquals, OperatorNotEquals, OperatorGreaterThan, OperatorGreaterOrEqual, OperatorLessThan,
		OperatorLessOrEqual, OperatorIn, OperatorNotIn, OperatorIsEmpty, OperatorIsNotEmpty,
	}
}
func (numberCodec) normalize(raw any, _ Column) (any, error) {
	var number float64
	switch value := raw.(type) {
	case float64:
		number = value
	case float32:
		number = float64(value)
	case int:
		number = float64(value)
	case int64:
		number = float64(value)
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", value)
		}
		number = parsed
	case string:
		// Thousands separators are common in human input ("50,000").
		cleaned := strings.NewReplacer(",", "", "_", "", " ", "").Replace(value)
		if cleaned == "" {
			return nil, nil
		}
		parsed, err := strconv.ParseFloat(cleaned, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", value)
		}
		number = parsed
	default:
		return nil, fmt.Errorf("expected a number, got %s", describeJSONType(raw))
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, errors.New("number must be finite")
	}
	return number, nil
}

// ---- boolean ----

type booleanCodec struct{}

func (booleanCodec) kind() ValueKind { return ValueKindBoolean }
func (booleanCodec) operators() []Operator {
	return []Operator{OperatorEquals, OperatorNotEquals, OperatorIsEmpty, OperatorIsNotEmpty}
}
func (booleanCodec) normalize(raw any, _ Column) (any, error) {
	switch value := raw.(type) {
	case bool:
		return value, nil
	case float64:
		if value == 1 || value == 0 {
			return value == 1, nil
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "":
			return nil, nil
		case "true", "yes", "y", "1", "done", "checked":
			return true, nil
		case "false", "no", "n", "0", "unchecked":
			return false, nil
		}
	}
	return nil, fmt.Errorf("expected yes/no (true/false), got %v", raw)
}

// ---- date & datetime ----

var temporalOperators = []Operator{
	OperatorEquals, OperatorNotEquals, OperatorGreaterThan, OperatorGreaterOrEqual,
	OperatorLessThan, OperatorLessOrEqual, OperatorIsEmpty, OperatorIsNotEmpty,
}

// Unambiguous date formats only. "03/04/2026" is rejected because it could be
// 3 April or 4 March depending on who typed it.
var acceptedDateLayouts = []string{DateLayout, "2 Jan 2006", "02 Jan 2006", "2 January 2006", "Jan 2, 2006", "January 2, 2006"}

type dateCodec struct{}

func (dateCodec) kind() ValueKind       { return ValueKindTemporal }
func (dateCodec) operators() []Operator { return temporalOperators }
func (dateCodec) normalize(raw any, _ Column) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("expected a date like \"2026-09-28\", got %s", describeJSONType(raw))
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	for _, layout := range acceptedDateLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.Format(DateLayout), nil
		}
	}
	// A full timestamp is accepted; its calendar date (as written) is kept.
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.Format(DateLayout), nil
	}
	return nil, fmt.Errorf("%q is not a date I can read unambiguously; use YYYY-MM-DD (e.g. \"2026-09-28\")", text)
}

var acceptedDateTimeLayouts = []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"}

type dateTimeCodec struct{}

func (dateTimeCodec) kind() ValueKind       { return ValueKindTemporal }
func (dateTimeCodec) operators() []Operator { return temporalOperators }
func (dateTimeCodec) normalize(raw any, _ Column) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("expected a date-time like \"2026-09-28T14:30:00+05:30\", got %s", describeJSONType(raw))
	}
	parsed, err := ParseDateTime(text)
	if err != nil || parsed.IsZero() {
		return nil, err
	}
	return parsed.Format(DateTimeLayout), nil
}

// ParseDateTime reads a timestamp; values without a timezone are taken as UTC. A bare date
// means midnight UTC. Empty input returns the zero time and no error.
func ParseDateTime(raw string) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, nil
	}
	for _, layout := range append(acceptedDateTimeLayouts, acceptedDateLayouts...) {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC().Truncate(time.Second), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date-time I can read; use ISO format like \"2026-09-28T14:30:00+05:30\"", text)
}

// ---- select ----

type singleSelectCodec struct{}

func (singleSelectCodec) kind() ValueKind { return ValueKindOption }
func (singleSelectCodec) operators() []Operator {
	return []Operator{OperatorEquals, OperatorNotEquals, OperatorIn, OperatorNotIn, OperatorIsEmpty, OperatorIsNotEmpty}
}
func (singleSelectCodec) normalize(raw any, column Column) (any, error) {
	text, err := scalarToString(raw)
	if err != nil {
		return nil, err
	}
	if CleanName(text) == "" {
		return nil, nil
	}
	return canonicalOption(column, text)
}

type multiSelectCodec struct{}

func (multiSelectCodec) kind() ValueKind { return ValueKindOptionList }
func (multiSelectCodec) operators() []Operator {
	// contains = has this option; in = has any of these options.
	return []Operator{OperatorContains, OperatorNotContains, OperatorIn, OperatorNotIn, OperatorIsEmpty, OperatorIsNotEmpty}
}
func (multiSelectCodec) normalize(raw any, column Column) (any, error) {
	var items []any
	switch value := raw.(type) {
	case []any:
		items = value
	case []string:
		for _, s := range value {
			items = append(items, s)
		}
	default:
		// A single option given on its own is accepted as a one-item list.
		items = []any{value}
	}
	selected := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		text, err := scalarToString(item)
		if err != nil {
			return nil, err
		}
		if CleanName(text) == "" {
			continue
		}
		option, err := canonicalOption(column, text)
		if err != nil {
			return nil, err
		}
		if !seen[option] {
			seen[option] = true
			selected = append(selected, option)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return selected, nil
}

func canonicalOption(column Column, raw string) (string, error) {
	if option, ok := column.FindOption(raw); ok {
		return option, nil
	}
	message := fmt.Sprintf("%q is not an allowed option (allowed: %s)", CleanName(raw), strings.Join(column.Options, ", "))
	if suggestions := SuggestSimilar(raw, column.Options, 2); len(suggestions) > 0 {
		message += fmt.Sprintf(". Did you mean %s?", strings.Join(QuoteAll(suggestions), " or "))
	}
	return "", errors.New(message)
}

// ---- helpers ----

func scalarToString(raw any) (string, error) {
	switch value := raw.(type) {
	case string:
		return value, nil
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(value), nil
	case json.Number:
		return value.String(), nil
	default:
		return "", fmt.Errorf("expected text, got %s", describeJSONType(raw))
	}
}

func describeJSONType(raw any) string {
	switch raw.(type) {
	case nil:
		return "nothing"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	case bool:
		return "true/false"
	case float64, json.Number:
		return "a number"
	case string:
		return "text"
	default:
		return fmt.Sprintf("%T", raw)
	}
}
