package postgres

import (
	"strings"
	"testing"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
)

// User supplied names and values must only ever reach Postgres as bind parameters.
func TestBuildWhereNeverEmbedsUserText(t *testing.T) {
	hostile := `x'); DROP TABLE trackers; --`
	column := domain.Column{ID: 7, Name: hostile, Type: domain.ColumnTypeText}
	condition, err := domain.NewCondition(domain.Field{Column: &column}, domain.OperatorContains, hostile)
	if err != nil {
		t.Fatal(err)
	}
	args := &sqlArgs{}
	sql, err := buildWhere(1, domain.RowFilter{Conditions: []domain.Condition{condition}, Match: domain.MatchAll}, args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "DROP") || strings.Contains(sql, hostile) {
		t.Fatalf("user text leaked into SQL: %s", sql)
	}
	if len(args.values) != 3 || args.values[1] != "7" || args.values[2] != hostile {
		t.Fatalf("expected tracker id, storage key and value as parameters, got %#v", args.values)
	}
}

func TestNegativeOperatorsMatchEmptyValues(t *testing.T) {
	column := domain.Column{ID: 3, Name: "Status", Type: domain.ColumnTypeSingleSelect, Options: []string{"Rejected"}}
	condition, err := domain.NewCondition(domain.Field{Column: &column}, domain.OperatorNotEquals, "rejected")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := buildCondition(condition, &sqlArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sql, "data->$1::text IS NULL OR NOT") {
		t.Fatalf("neq should include empty values, got %s", sql)
	}
}
