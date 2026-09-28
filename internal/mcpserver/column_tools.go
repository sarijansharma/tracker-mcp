package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sarijansharma/tracker-mcp/internal/service"
)

func (h *handlers) registerColumnTools(server *mcp.Server) {
	addTool(server, h.errors, &mcp.Tool{
		Name: "add_column",
		Description: "Add a column to a tracker. A required column on a tracker that already has rows needs a default_value, " +
			"which is filled into the existing rows.",
		Annotations: additive("Add column"),
	}, h.addColumn)

	addTool(server, h.errors, &mcp.Tool{
		Name: "update_column",
		Description: "Change a column's description, required flag, select options or default value. The column type can't be changed; " +
			"to change it, add a new column and remove the old one.",
		Annotations: modifying("Update column", false),
	}, h.updateColumn)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "rename_column",
		Description: "Rename a column. Existing values are kept.",
		Annotations: modifying("Rename column", true),
	}, h.renameColumn)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "remove_column",
		Description: "Remove a column and permanently erase its values from all rows. Without confirm=true this only previews the impact.",
		Annotations: destructive("Remove column"),
	}, h.removeColumn)
}

func (h *handlers) addColumn(ctx context.Context, request AddColumnRequest) (AddColumnResponse, error) {
	result, err := h.columns.Add(ctx, toTrackerRef(request.Tracker, request.TrackerID), toColumnSpec(request.Column))
	if err != nil {
		return AddColumnResponse{}, err
	}
	message := fmt.Sprintf("Added column %q (%s) to tracker %q.", result.Column.Name, result.Column.Type, result.Tracker.Name)
	if result.RowsBackfill > 0 {
		message += fmt.Sprintf(" %d existing rows were filled with the default value.", result.RowsBackfill)
	}
	return AddColumnResponse{Message: message, Column: toColumnDTO(result.Column), ExistingRowsFilled: result.RowsBackfill}, nil
}

func (h *handlers) updateColumn(ctx context.Context, request UpdateColumnRequest) (UpdateColumnResponse, error) {
	changes := service.ColumnChanges{
		Description: request.Description, Required: request.Required, AddOptions: request.AddOptions,
		RemoveOptions: request.RemoveOptions, DefaultValue: request.DefaultValue, RemoveDefaultValue: request.RemoveDefaultValue,
	}
	result, err := h.columns.Update(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.Column, changes)
	if err != nil {
		return UpdateColumnResponse{}, err
	}
	message := fmt.Sprintf("Updated column %q.", result.Column.Name)
	if result.RowsBackfill > 0 {
		message += fmt.Sprintf(" %d rows without a value were filled with the default.", result.RowsBackfill)
	}
	return UpdateColumnResponse{Message: message, Column: toColumnDTO(result.Column), ExistingRowsFilled: result.RowsBackfill}, nil
}

func (h *handlers) renameColumn(ctx context.Context, request RenameColumnRequest) (ColumnResponse, error) {
	column, err := h.columns.Rename(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.Column, request.NewName)
	if err != nil {
		return ColumnResponse{}, err
	}
	return ColumnResponse{Message: fmt.Sprintf("Column renamed to %q.", column.Name), Column: toColumnDTO(column)}, nil
}

func (h *handlers) removeColumn(ctx context.Context, request RemoveColumnRequest) (RemoveColumnResponse, error) {
	result, err := h.columns.Remove(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.Column, request.Confirm)
	if err != nil {
		return RemoveColumnResponse{}, err
	}
	message := fmt.Sprintf("Preview only, nothing was removed: removing column %q would erase its values from %d rows. "+
		"Confirm with the user, then call again with confirm=true.", result.Column.Name, result.RowsWithValues)
	if result.Confirmed {
		message = fmt.Sprintf("Removed column %q and its values from %d rows.", result.Column.Name, result.RowsWithValues)
	}
	return RemoveColumnResponse{
		Message: message, Column: result.Column.Name, RowsWithValues: result.RowsWithValues,
		Confirmed: result.Confirmed, RemainingColumns: result.RemainingColumns,
	}, nil
}
