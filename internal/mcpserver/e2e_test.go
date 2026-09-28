package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sarijansharma/tracker-mcp/internal/mcpserver"
	"github.com/sarijansharma/tracker-mcp/internal/repository/postgres"
	"github.com/sarijansharma/tracker-mcp/internal/service"
)

// These tests drive the real MCP server over an in-memory transport against a real
// Postgres. Set TEST_DATABASE_URL to a disposable database; its public schema is reset.
func newTestClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping end-to-end tests")
	}
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, databaseURL, postgres.PoolOptions{MaxConnections: 5})
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("resetting schema: %v", err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	store := postgres.NewStore(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := mcpserver.NewServer(mcpserver.Dependencies{
		Trackers: service.NewTrackerService(store, "hr@example.com"),
		Columns:  service.NewColumnService(store),
		Rows:     service.NewRowService(store, "hr@example.com"),
		Logger:   logger,
	}, "test")

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type toolResult struct {
	isError bool
	text    string
	data    map[string]any
}

func call(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) toolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", tool, err)
	}
	out := toolResult{isError: result.IsError}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			out.text += text.Text
		}
	}
	if result.StructuredContent != nil {
		raw, _ := json.Marshal(result.StructuredContent)
		_ = json.Unmarshal(raw, &out.data)
	}
	return out
}

func mustSucceed(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) toolResult {
	t.Helper()
	result := call(t, session, tool, args)
	if result.isError {
		t.Fatalf("%s failed: %s", tool, result.text)
	}
	return result
}

func mustFail(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any, wantInMessage ...string) toolResult {
	t.Helper()
	result := call(t, session, tool, args)
	if !result.isError {
		t.Fatalf("%s: expected an error, got %s", tool, result.text)
	}
	for _, want := range wantInMessage {
		if !strings.Contains(result.text, want) {
			t.Fatalf("%s: error %q should mention %q", tool, result.text, want)
		}
	}
	return result
}

func createInterviewTracker(t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	mustSucceed(t, session, "create_tracker", map[string]any{
		"name":        "Interview Tracker",
		"description": "Candidates for open roles",
		"columns": []map[string]any{
			{"name": "Candidate", "type": "text", "required": true},
			{"name": "Email", "type": "email"},
			{"name": "Interview Date", "type": "date"},
			{"name": "Status", "type": "dropdown", "options": []string{"Scheduled", "Selected", "Rejected"}, "default_value": "Scheduled"},
			{"name": "Skills", "type": "tags", "options": []string{"Go", "SQL", "React"}},
			{"name": "Experience", "type": "number"},
		},
	})
	mustSucceed(t, session, "add_rows", map[string]any{
		"tracker": "interview tracker",
		"rows": []map[string]any{
			{"Candidate": "Asha Rao", "Email": "Asha@Example.com", "Interview Date": "2026-10-01", "Status": "selected", "Skills": []string{"Go", "SQL"}, "Experience": "5"},
			{"Candidate": "Ravi Kumar", "Interview Date": "2026-10-03", "Status": "Rejected", "Skills": "react", "Experience": 2},
			{"Candidate": "Meera Iyer", "Interview Date": "5 Oct 2026", "Experience": 8},
		},
	})
}

func TestToolsAreRegistered(t *testing.T) {
	session := newTestClient(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 15 {
		t.Fatalf("expected 15 tools, got %d", len(tools.Tools))
	}
}

func TestTrackerNameEdgeCases(t *testing.T) {
	session := newTestClient(t)
	createInterviewTracker(t, session)

	t.Run("duplicate name ignoring case and spacing is refused and points at the existing tracker", func(t *testing.T) {
		mustFail(t, session, "create_tracker", map[string]any{
			"name": "  interview   TRACKER ", "columns": []map[string]any{{"name": "A", "type": "text"}},
		}, "already uses this name", "Interview Tracker", "id 1")
	})

	t.Run("misspelled name is never guessed but suggested", func(t *testing.T) {
		mustFail(t, session, "describe_tracker", map[string]any{"tracker": "Intervew Tracker"},
			`No tracker is named "Intervew Tracker"`, "Did you mean", `"Interview Tracker" (id 1`)
	})

	t.Run("partial name lists candidates when several trackers look alike", func(t *testing.T) {
		mustSucceed(t, session, "create_tracker", map[string]any{
			"name": "Interview Tracker 2025", "columns": []map[string]any{{"name": "Candidate", "type": "text"}},
		})
		result := mustFail(t, session, "describe_tracker", map[string]any{"tracker": "interview"}, "Did you mean")
		if !strings.Contains(result.text, "Interview Tracker 2025") || !strings.Contains(result.text, `"Interview Tracker" (id 1`) {
			t.Fatalf("both trackers should be suggested: %s", result.text)
		}
	})

	t.Run("id and name that disagree are rejected", func(t *testing.T) {
		mustFail(t, session, "describe_tracker", map[string]any{"tracker": "Interview Tracker 2025", "tracker_id": 1}, "also given")
	})

	t.Run("delete previews first, then soft deletes; name becomes reusable", func(t *testing.T) {
		preview := mustSucceed(t, session, "delete_tracker", map[string]any{"tracker": "Interview Tracker 2025"})
		if preview.data["confirmed"] != false || !strings.Contains(preview.text, "Preview only") {
			t.Fatalf("expected a preview: %s", preview.text)
		}
		mustSucceed(t, session, "describe_tracker", map[string]any{"tracker": "Interview Tracker 2025"})
		deleted := mustSucceed(t, session, "delete_tracker", map[string]any{"tracker": "Interview Tracker 2025", "confirm": true})
		deletedID := deleted.data["tracker"].(map[string]any)["id"]
		mustFail(t, session, "describe_tracker", map[string]any{"tracker": "Interview Tracker 2025"}, "deleted tracker with that name", "restore_tracker")

		// Same name, new tracker, different creation time.
		mustSucceed(t, session, "create_tracker", map[string]any{
			"name": "Interview Tracker 2025", "columns": []map[string]any{{"name": "Candidate", "type": "text"}},
		})
		mustFail(t, session, "restore_tracker", map[string]any{"tracker_id": deletedID}, "already named", "new_name")
		restored := mustSucceed(t, session, "restore_tracker", map[string]any{"tracker_id": deletedID, "new_name": "Interview Tracker 2025 (old)"})
		if restored.data["tracker"].(map[string]any)["name"] != "Interview Tracker 2025 (old)" {
			t.Fatalf("unexpected restore result: %s", restored.text)
		}
	})

	t.Run("several deleted trackers with the same name need an id to restore", func(t *testing.T) {
		for range 2 {
			mustSucceed(t, session, "create_tracker", map[string]any{"name": "Sprint Tasks", "columns": []map[string]any{{"name": "Task", "type": "text"}}})
			mustSucceed(t, session, "delete_tracker", map[string]any{"tracker": "Sprint Tasks", "confirm": true})
		}
		mustFail(t, session, "restore_tracker", map[string]any{"tracker": "sprint tasks"}, "2 deleted trackers", "tracker_id")
	})
}

func TestRowEdgeCases(t *testing.T) {
	session := newTestClient(t)
	createInterviewTracker(t, session)

	t.Run("values are normalised and defaults applied", func(t *testing.T) {
		result := mustSucceed(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "sort_by": []map[string]any{{"column": "id"}}})
		rows := result.data["rows"].([]any)
		asha := rows[0].(map[string]any)["values"].(map[string]any)
		meera := rows[2].(map[string]any)["values"].(map[string]any)
		if asha["Email"] != "asha@example.com" || asha["Status"] != "Selected" || asha["Experience"] != 5.0 {
			t.Fatalf("unexpected normalisation: %v", asha)
		}
		if meera["Status"] != "Scheduled" || meera["Interview Date"] != "2026-10-05" {
			t.Fatalf("default or date parsing not applied: %v", meera)
		}
	})

	t.Run("bulk add is all-or-nothing and reports every problem", func(t *testing.T) {
		result := mustFail(t, session, "add_rows", map[string]any{
			"tracker": "Interview Tracker",
			"rows": []map[string]any{
				{"Candidate": "Valid Person"},
				{"Candidate": "X", "Stauts": "Selected"},
				{"Email": "not-an-email"},
				{"Candidate": "Y", "Status": "Selcted", "Interview Date": "03/04/2026"},
			},
		}, "Nothing was added", "row 2", `"Status"`, "row 3", "required", "not a valid email", "row 4", `Did you mean "Selected"`, "unambiguously")
		_ = result
		count := mustSucceed(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker"})
		if count.data["total_matching"] != 3.0 {
			t.Fatalf("no rows should have been added, total=%v", count.data["total_matching"])
		}
	})

	t.Run("update is partial; null clears; required can't be cleared", func(t *testing.T) {
		mustSucceed(t, session, "update_row", map[string]any{"tracker": "Interview Tracker", "row_id": 1, "values": map[string]any{"Experience": nil, "Skills": []string{"React"}}})
		row := mustSucceed(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "filters": []map[string]any{{"column": "id", "operator": "eq", "value": 1}}})
		values := row.data["rows"].([]any)[0].(map[string]any)["values"].(map[string]any)
		if _, has := values["Experience"]; has || values["Candidate"] != "Asha Rao" {
			t.Fatalf("unexpected row after update: %v", values)
		}
		mustFail(t, session, "update_row", map[string]any{"tracker": "Interview Tracker", "row_id": 1, "values": map[string]any{"Candidate": ""}}, "required")
		mustFail(t, session, "update_row", map[string]any{"tracker": "Interview Tracker", "row_id": 999, "values": map[string]any{"Candidate": "Z"}}, "no row with id 999")
	})

	t.Run("filters", func(t *testing.T) {
		query := func(filters []map[string]any, match string) float64 {
			args := map[string]any{"tracker": "Interview Tracker", "filters": filters}
			if match != "" {
				args["match"] = match
			}
			return mustSucceed(t, session, "query_rows", args).data["total_matching"].(float64)
		}
		cases := []struct {
			name    string
			filters []map[string]any
			match   string
			want    float64
		}{
			{"select eq ignores case", []map[string]any{{"column": "status", "operator": "=", "value": "selected"}}, "", 1},
			{"neq includes rows without a value", []map[string]any{{"column": "Email", "operator": "neq", "value": "asha@example.com"}}, "", 2},
			{"date range", []map[string]any{{"column": "Interview Date", "operator": "gte", "value": "2026-10-02"}}, "", 2},
			{"text contains", []map[string]any{{"column": "Candidate", "operator": "contains", "value": "KUMAR"}}, "", 1},
			{"multi select contains", []map[string]any{{"column": "Skills", "operator": "contains", "value": "react"}}, "", 2},
			{"number gt", []map[string]any{{"column": "Experience", "operator": ">", "value": 3}}, "", 1},
			{"in list", []map[string]any{{"column": "Status", "operator": "in", "value": []string{"Selected", "Rejected"}}}, "", 2},
			{"is_empty", []map[string]any{{"column": "Email", "operator": "is_empty"}}, "", 2},
			{"match any", []map[string]any{{"column": "Status", "operator": "eq", "value": "Rejected"}, {"column": "Experience", "operator": "gte", "value": 8}}, "any", 2},
			{"wildcards are literal", []map[string]any{{"column": "Candidate", "operator": "contains", "value": "%"}}, "", 0},
		}
		for _, tc := range cases {
			if got := query(tc.filters, tc.match); got != tc.want {
				t.Errorf("%s: got %v rows, want %v", tc.name, got, tc.want)
			}
		}
		mustFail(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "filters": []map[string]any{{"column": "Staus", "operator": "eq", "value": "x"}}}, `"Status"`)
		mustFail(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "filters": []map[string]any{{"column": "Status", "operator": "gt", "value": "Selected"}}}, "can't be used")
		mustFail(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "filters": []map[string]any{{"column": "Status", "operator": "eq", "value": "Hired"}}}, "not an allowed option")
	})

	t.Run("sorting puts empty values last and paging reports has_more", func(t *testing.T) {
		result := mustSucceed(t, session, "query_rows", map[string]any{
			"tracker": "Interview Tracker", "sort_by": []map[string]any{{"column": "Experience", "direction": "desc"}}, "limit": 2,
		})
		rows := result.data["rows"].([]any)
		first := rows[0].(map[string]any)["values"].(map[string]any)
		if first["Candidate"] != "Meera Iyer" || result.data["has_more"] != true || result.data["next_offset"] != 2.0 {
			t.Fatalf("unexpected page: %s", result.text)
		}
	})

	t.Run("summarize counts per option including empty", func(t *testing.T) {
		result := mustSucceed(t, session, "summarize_rows", map[string]any{"tracker": "Interview Tracker", "group_by": "Skills"})
		groups := map[string]float64{}
		for _, g := range result.data["groups"].([]any) {
			group := g.(map[string]any)
			groups[group["value"].(string)] = group["count"].(float64)
		}
		if groups["React"] != 2 || groups["(empty)"] != 1 {
			t.Fatalf("unexpected groups: %v", groups)
		}
	})

	t.Run("delete rows previews, reports missing ids, then deletes", func(t *testing.T) {
		preview := mustSucceed(t, session, "delete_rows", map[string]any{"tracker": "Interview Tracker", "row_ids": []int{2, 77}})
		if preview.data["confirmed"] != false || len(preview.data["rows"].([]any)) != 1 || !strings.Contains(preview.text, "77") {
			t.Fatalf("unexpected preview: %s", preview.text)
		}
		mustSucceed(t, session, "delete_rows", map[string]any{"tracker": "Interview Tracker", "row_ids": []int{2}, "confirm": true})
		mustFail(t, session, "delete_rows", map[string]any{"tracker": "Interview Tracker", "row_ids": []int{2}, "confirm": true}, "None of the row ids")
	})
}

func TestColumnEdgeCases(t *testing.T) {
	session := newTestClient(t)
	createInterviewTracker(t, session)

	t.Run("required column on non-empty tracker needs a default, which backfills", func(t *testing.T) {
		mustFail(t, session, "add_column", map[string]any{"tracker": "Interview Tracker", "column": map[string]any{"name": "Round", "type": "number", "required": true}}, "default_value")
		result := mustSucceed(t, session, "add_column", map[string]any{"tracker": "Interview Tracker", "column": map[string]any{"name": "Round", "type": "number", "required": true, "default_value": 1}})
		if result.data["existing_rows_filled"] != 3.0 {
			t.Fatalf("expected 3 rows backfilled: %s", result.text)
		}
	})

	t.Run("duplicate and reserved column names are refused", func(t *testing.T) {
		mustFail(t, session, "add_column", map[string]any{"tracker": "Interview Tracker", "column": map[string]any{"name": "status", "type": "text"}}, "already has a column")
		mustFail(t, session, "add_column", map[string]any{"tracker": "Interview Tracker", "column": map[string]any{"name": "created_at", "type": "date"}}, "built-in")
	})

	t.Run("rename keeps values", func(t *testing.T) {
		mustSucceed(t, session, "rename_column", map[string]any{"tracker": "Interview Tracker", "column": "Status", "new_name": "Stage"})
		result := mustSucceed(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "filters": []map[string]any{{"column": "Stage", "operator": "eq", "value": "Selected"}}})
		if result.data["total_matching"] != 1.0 {
			t.Fatalf("values should survive rename: %s", result.text)
		}
	})

	t.Run("options in use can't be removed", func(t *testing.T) {
		mustFail(t, session, "update_column", map[string]any{"tracker": "Interview Tracker", "column": "Stage", "remove_options": []string{"Rejected"}}, "used by 1 rows")
		mustSucceed(t, session, "update_column", map[string]any{"tracker": "Interview Tracker", "column": "Stage", "add_options": []string{"On Hold"}})
		mustSucceed(t, session, "update_column", map[string]any{"tracker": "Interview Tracker", "column": "Stage", "default_value": "on hold"})
		mustFail(t, session, "update_column", map[string]any{"tracker": "Interview Tracker", "column": "Stage", "remove_options": []string{"On Hold"}}, "default value")
	})

	t.Run("making a column required without a default is refused while rows are empty", func(t *testing.T) {
		mustFail(t, session, "update_column", map[string]any{"tracker": "Interview Tracker", "column": "Email", "required": true}, "2 rows have no value")
	})

	t.Run("remove column previews, then erases values", func(t *testing.T) {
		preview := mustSucceed(t, session, "remove_column", map[string]any{"tracker": "Interview Tracker", "column": "Skills"})
		if preview.data["confirmed"] != false || preview.data["rows_with_values"] != 2.0 {
			t.Fatalf("unexpected preview: %s", preview.text)
		}
		mustSucceed(t, session, "remove_column", map[string]any{"tracker": "Interview Tracker", "column": "Skills", "confirm": true})
		mustFail(t, session, "query_rows", map[string]any{"tracker": "Interview Tracker", "filters": []map[string]any{{"column": "Skills", "operator": "is_empty"}}}, "no column named")
	})
}
