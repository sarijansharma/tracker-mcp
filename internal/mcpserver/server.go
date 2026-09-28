// Package mcpserver exposes the tracker services as MCP tools. It only translates between
// MCP DTOs and service calls; all business rules live in the service layer.
package mcpserver

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/service"
)

// TrackerManager is the tracker-level behaviour the tools need.
type TrackerManager interface {
	Create(ctx context.Context, input service.CreateTrackerInput) (service.TrackerDetails, error)
	List(ctx context.Context, filter domain.TrackerListFilter) ([]domain.TrackerSummary, error)
	Describe(ctx context.Context, ref service.TrackerRef) (service.TrackerDetails, error)
	Rename(ctx context.Context, ref service.TrackerRef, newName string) (service.TrackerDetails, error)
	Delete(ctx context.Context, ref service.TrackerRef, confirm bool) (service.DeleteTrackerResult, error)
	Restore(ctx context.Context, ref service.TrackerRef, newName string) (service.TrackerDetails, error)
}

// ColumnManager is the column-level behaviour the tools need.
type ColumnManager interface {
	Add(ctx context.Context, ref service.TrackerRef, spec domain.ColumnSpec) (service.AddColumnResult, error)
	Update(ctx context.Context, ref service.TrackerRef, column string, changes service.ColumnChanges) (service.UpdateColumnResult, error)
	Rename(ctx context.Context, ref service.TrackerRef, column, newName string) (domain.Column, error)
	Remove(ctx context.Context, ref service.TrackerRef, column string, confirm bool) (service.RemoveColumnResult, error)
}

// RowManager is the row-level behaviour the tools need.
type RowManager interface {
	AddRows(ctx context.Context, ref service.TrackerRef, rows []service.NamedValues) (service.RowsResult, error)
	UpdateRow(ctx context.Context, ref service.TrackerRef, rowID int64, changes service.NamedValues) (service.RowsResult, error)
	DeleteRows(ctx context.Context, ref service.TrackerRef, rowIDs []int64, confirm bool) (service.DeleteRowsResult, error)
	QueryRows(ctx context.Context, ref service.TrackerRef, input service.QueryRowsInput) (service.QueryRowsResult, error)
	SummarizeRows(ctx context.Context, ref service.TrackerRef, groupBy string, filters []service.FilterInput, match string) (service.SummarizeRowsResult, error)
}

// Dependencies are the services the MCP server delegates to.
type Dependencies struct {
	Trackers TrackerManager
	Columns  ColumnManager
	Rows     RowManager
	Logger   *slog.Logger
}

const serverInstructions = `Tracker MCP lets people keep simple tables ("trackers") without knowing databases,
e.g. an interview tracker for HR or a per-person task tracker for a team.

Guidelines:
- Start with list_trackers or describe_tracker to learn exact tracker and column names before adding or querying rows.
- If a tool says a tracker or column was not found and offers suggestions, ask the user which one they meant; never assume.
- Several trackers can have similar names; when in doubt, use tracker_id.
- delete_tracker, remove_column and delete_rows first return a preview. Show it to the user, and call again with confirm=true only after they agree.
- Dates are YYYY-MM-DD. Datetimes without a timezone are treated as UTC.
- Row ids come from query_rows; use them for update_row and delete_rows.`

// NewServer builds an MCP server with every tracker tool registered.
func NewServer(deps Dependencies, version string) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "tracker-mcp", Title: "Tracker", Version: version},
		&mcp.ServerOptions{Instructions: serverInstructions, Logger: deps.Logger},
	)
	h := &handlers{
		trackers: deps.Trackers,
		columns:  deps.Columns,
		rows:     deps.Rows,
		errors:   errorPresenter{logger: deps.Logger},
	}
	h.registerTrackerTools(server)
	h.registerColumnTools(server)
	h.registerRowTools(server)
	return server
}

type handlers struct {
	trackers TrackerManager
	columns  ColumnManager
	rows     RowManager
	errors   errorPresenter
}

// addTool registers a typed handler and routes its errors through the error presenter.
func addTool[In, Out any](server *mcp.Server, presenter errorPresenter, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		output, err := handle(ctx, input)
		if err != nil {
			var zero Out
			return nil, zero, presenter.present(ctx, tool.Name, err)
		}
		return nil, output, nil
	})
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPtr(false)}
}

func additive(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
}

func modifying(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: boolPtr(false), IdempotentHint: idempotent, OpenWorldHint: boolPtr(false)}
}

func destructive(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)}
}

func boolPtr(value bool) *bool { return &value }
