package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sarijansharma/tracker-mcp/internal/service"
)

func (h *handlers) registerRowTools(server *mcp.Server) {
	addTool(server, h.errors, &mcp.Tool{
		Name: "add_rows",
		Description: "Add one or more rows (max 200 per call). Each row maps column names to values, e.g. " +
			`{"Candidate": "Asha Rao", "Interview Date": "2026-10-02", "Status": "Scheduled"}. ` +
			"Values are checked against the column types; if any row is invalid nothing is added and every problem is listed.",
		Annotations: additive("Add rows"),
	}, h.addRows)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "update_row",
		Description: "Change some values of one row. Only the columns you pass change; pass null to clear a value.",
		Annotations: modifying("Update row", true),
	}, h.updateRow)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "delete_rows",
		Description: "Permanently delete rows by id. Without confirm=true this only previews the rows that would be deleted.",
		Annotations: destructive("Delete rows"),
	}, h.deleteRows)

	addTool(server, h.errors, &mcp.Tool{
		Name: "query_rows",
		Description: "Find rows with optional filters, sorting and paging. Example filters: " +
			`[{"column": "Status", "operator": "eq", "value": "Selected"}, {"column": "Interview Date", "operator": "gte", "value": "2026-10-01"}]. ` +
			"Returns total_matching and has_more; use offset to page.",
		Annotations: readOnly("Query rows"),
	}, h.queryRows)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "summarize_rows",
		Description: "Count rows per value of a column (e.g. candidates per Status), optionally only rows matching filters.",
		Annotations: readOnly("Summarize rows"),
	}, h.summarizeRows)
}

func (h *handlers) addRows(ctx context.Context, request AddRowsRequest) (RowsResponse, error) {
	rows := make([]service.NamedValues, len(request.Rows))
	for i, row := range request.Rows {
		rows[i] = toNamedValues(row)
	}
	result, err := h.rows.AddRows(ctx, toTrackerRef(request.Tracker, request.TrackerID), rows)
	if err != nil {
		return RowsResponse{}, err
	}
	return RowsResponse{
		Message: fmt.Sprintf("Added %d rows to tracker %q.", len(result.Rows), result.Tracker.Name),
		Tracker: toTrackerRefDTO(result.Tracker),
		Rows:    toRowDTOs(result.Tracker, result.Rows),
	}, nil
}

func (h *handlers) updateRow(ctx context.Context, request UpdateRowRequest) (RowsResponse, error) {
	result, err := h.rows.UpdateRow(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.RowID, toNamedValues(request.Values))
	if err != nil {
		return RowsResponse{}, err
	}
	return RowsResponse{
		Message: fmt.Sprintf("Updated row %d in tracker %q.", request.RowID, result.Tracker.Name),
		Tracker: toTrackerRefDTO(result.Tracker),
		Rows:    toRowDTOs(result.Tracker, result.Rows),
	}, nil
}

func (h *handlers) deleteRows(ctx context.Context, request DeleteRowsRequest) (DeleteRowsResponse, error) {
	result, err := h.rows.DeleteRows(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.RowIDs, request.Confirm)
	if err != nil {
		return DeleteRowsResponse{}, err
	}
	message := fmt.Sprintf("Preview only, nothing was deleted: %d rows would be permanently deleted. "+
		"Confirm with the user, then call again with confirm=true.", len(result.Rows))
	if result.Confirmed {
		message = fmt.Sprintf("Permanently deleted %d rows from tracker %q.", len(result.Rows), result.Tracker.Name)
	}
	if len(result.MissingIDs) > 0 {
		message += fmt.Sprintf(" These ids were not found in this tracker: %v.", result.MissingIDs)
	}
	return DeleteRowsResponse{
		Message: message, Tracker: toTrackerRefDTO(result.Tracker), Rows: toRowDTOs(result.Tracker, result.Rows),
		MissingIDs: result.MissingIDs, Confirmed: result.Confirmed,
	}, nil
}

func (h *handlers) queryRows(ctx context.Context, request QueryRowsRequest) (QueryRowsResponse, error) {
	sorts, err := toSortInputs(request.SortBy)
	if err != nil {
		return QueryRowsResponse{}, err
	}
	result, err := h.rows.QueryRows(ctx, toTrackerRef(request.Tracker, request.TrackerID), service.QueryRowsInput{
		Filters: toFilterInputs(request.Filters), Match: request.Match, Sorts: sorts, Limit: request.Limit, Offset: request.Offset,
	})
	if err != nil {
		return QueryRowsResponse{}, err
	}
	response := QueryRowsResponse{
		Tracker:       toTrackerRefDTO(result.Tracker),
		Columns:       result.Tracker.ColumnNames(),
		Rows:          toRowDTOs(result.Tracker, result.Rows),
		TotalMatching: result.TotalMatching,
		Returned:      len(result.Rows),
		Offset:        result.Offset,
	}
	if next := result.Offset + len(result.Rows); int64(next) < result.TotalMatching {
		response.HasMore, response.NextOffset = true, next
	}
	return response, nil
}

func (h *handlers) summarizeRows(ctx context.Context, request SummarizeRowsRequest) (SummarizeRowsResponse, error) {
	result, err := h.rows.SummarizeRows(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.GroupBy,
		toFilterInputs(request.Filters), request.Match)
	if err != nil {
		return SummarizeRowsResponse{}, err
	}
	groups, total := toGroupCountDTOs(result.Groups)
	return SummarizeRowsResponse{
		Tracker: toTrackerRefDTO(result.Tracker), GroupBy: result.GroupBy.Name, Groups: groups,
		TotalRows: total, Truncated: result.Truncated,
	}, nil
}
