package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeValue(t *testing.T) {
	status := Column{Name: "Status", Type: ColumnTypeSingleSelect, Options: []string{"Scheduled", "Selected", "Rejected"}}
	skills := Column{Name: "Skills", Type: ColumnTypeMultiSelect, Options: []string{"Go", "SQL", "React"}}

	tests := []struct {
		name      string
		column    Column
		raw       any
		want      any
		wantError string
	}{
		{name: "text is trimmed", column: Column{Type: ColumnTypeText}, raw: "  hello ", want: "hello"},
		{name: "blank text is empty", column: Column{Type: ColumnTypeText}, raw: "   ", want: nil},
		{name: "number from JSON", column: Column{Type: ColumnTypeNumber}, raw: 42.5, want: 42.5},
		{name: "number from text with separators", column: Column{Type: ColumnTypeNumber}, raw: "1,50,000", want: 150000.0},
		{name: "number rejects words", column: Column{Type: ColumnTypeNumber}, raw: "ten", wantError: "not a number"},
		{name: "boolean from yes", column: Column{Type: ColumnTypeBoolean}, raw: "Yes", want: true},
		{name: "boolean rejects maybe", column: Column{Type: ColumnTypeBoolean}, raw: "maybe", wantError: "yes/no"},
		{name: "iso date", column: Column{Type: ColumnTypeDate}, raw: "2026-09-28", want: "2026-09-28"},
		{name: "written date", column: Column{Type: ColumnTypeDate}, raw: "28 Sep 2026", want: "2026-09-28"},
		{name: "ambiguous slash date rejected", column: Column{Type: ColumnTypeDate}, raw: "03/04/2026", wantError: "unambiguously"},
		{name: "datetime normalised to UTC", column: Column{Type: ColumnTypeDateTime}, raw: "2026-09-28T10:00:00+05:30", want: "2026-09-28T04:30:00Z"},
		{name: "datetime without zone is UTC", column: Column{Type: ColumnTypeDateTime}, raw: "2026-09-28 10:00", want: "2026-09-28T10:00:00Z"},
		{name: "select is case-insensitive", column: status, raw: "selected", want: "Selected"},
		{name: "select suggests spelling", column: status, raw: "Selcted", wantError: `Did you mean "Selected"`},
		{name: "multi select dedupes", column: skills, raw: []any{"go", "SQL", "Go"}, want: []string{"Go", "SQL"}},
		{name: "multi select single value", column: skills, raw: "react", want: []string{"React"}},
		{name: "multi select empty list", column: skills, raw: []any{}, want: nil},
		{name: "email lowercased", column: Column{Type: ColumnTypeEmail}, raw: " Asha@Example.COM ", want: "asha@example.com"},
		{name: "email rejected", column: Column{Type: ColumnTypeEmail}, raw: "asha@", wantError: "not a valid email"},
		{name: "url gets scheme", column: Column{Type: ColumnTypeURL}, raw: "linkedin.com/in/asha", want: "https://linkedin.com/in/asha"},
		{name: "url rejects ftp", column: Column{Type: ColumnTypeURL}, raw: "ftp://files.example.com", wantError: "not a valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeValue(tt.column, tt.raw)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("want error containing %q, got value %v, error %v", tt.wantError, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseColumnTypeAcceptsAliasesAndSuggests(t *testing.T) {
	for raw, want := range map[string]ColumnType{"Dropdown": ColumnTypeSingleSelect, "checkbox": ColumnTypeBoolean, "multi select": ColumnTypeMultiSelect} {
		if got, err := ParseColumnType(raw); err != nil || got != want {
			t.Errorf("ParseColumnType(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	_, err := ParseColumnType("nubmer")
	if err == nil || !strings.Contains(err.Error(), "number") {
		t.Fatalf("want suggestion for number, got %v", err)
	}
}

func TestColumnSpecValidate(t *testing.T) {
	tests := []struct {
		name      string
		spec      ColumnSpec
		wantError string
	}{
		{name: "reserved name", spec: ColumnSpec{Name: "Created At", Type: "date"}, wantError: "built-in field"},
		{name: "select needs options", spec: ColumnSpec{Name: "Status", Type: "single_select"}, wantError: "at least one option"},
		{name: "text can't have options", spec: ColumnSpec{Name: "Notes", Type: "text", Options: []string{"a"}}, wantError: "only allowed"},
		{name: "duplicate options", spec: ColumnSpec{Name: "Status", Type: "single_select", Options: []string{"Open", "open"}}, wantError: "more than once"},
		{name: "invalid default", spec: ColumnSpec{Name: "Status", Type: "single_select", Options: []string{"Open"}, DefaultValue: "Closed"}, wantError: "Default value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.spec.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("want error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestNewConditionRejectsOperatorTypeMismatch(t *testing.T) {
	status := Column{Name: "Status", Type: ColumnTypeSingleSelect, Options: []string{"Open"}}
	_, err := NewCondition(Field{Column: &status}, OperatorGreaterThan, "Open")
	if err == nil || !strings.Contains(err.Error(), "can't be used") {
		t.Fatalf("want operator mismatch error, got %v", err)
	}
}

func TestNewConditionTextOperandIsNotValidatedAsEmail(t *testing.T) {
	email := Column{Name: "Email", Type: ColumnTypeEmail}
	condition, err := NewCondition(Field{Column: &email}, OperatorContains, "@gmail")
	if err != nil || condition.Value != "@gmail" {
		t.Fatalf("partial email should be a valid contains operand, got %v, %v", condition.Value, err)
	}
}

func TestSuggestSimilar(t *testing.T) {
	got := SuggestSimilar("Intervew Date", []string{"Interview Date", "Candidate", "Status"}, 3)
	if len(got) != 1 || got[0] != "Interview Date" {
		t.Fatalf("got %v", got)
	}
	if got := SuggestSimilar("xyz", []string{"Candidate", "Status"}, 3); len(got) != 0 {
		t.Fatalf("unrelated input should have no suggestions, got %v", got)
	}
}

func TestCleanNameAndSameName(t *testing.T) {
	if CleanName("  Interview   Tracker ") != "Interview Tracker" {
		t.Fatal("CleanName should collapse whitespace")
	}
	if !SameName("interview tracker", " Interview  TRACKER") {
		t.Fatal("SameName should ignore case and whitespace")
	}
}
