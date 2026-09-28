package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/service"
)

func (h *handlers) registerTrackerTools(server *mcp.Server) {
	addTool(server, h.errors, &mcp.Tool{
		Name: "create_tracker",
		Description: "Create a new tracker (a table) with its columns. Example: an 'Interview Tracker 2026' with columns " +
			"Candidate (text, required), Role (text), Interview Date (date), Status (single_select: Scheduled, Selected, Rejected). " +
			"Names must be unique ignoring case; if one exists you'll get its details instead.",
		Annotations: additive("Create tracker"),
	}, h.createTracker)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "list_trackers",
		Description: "List trackers with their column and row counts. Use search to find a tracker by part of its name (typos tolerated).",
		Annotations: readOnly("List trackers"),
	}, h.listTrackers)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "describe_tracker",
		Description: "Show a tracker's columns (types, options, required, defaults, allowed filter operators) and row count. Call this before adding or querying rows.",
		Annotations: readOnly("Describe tracker"),
	}, h.describeTracker)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "rename_tracker",
		Description: "Rename a tracker. The new name must not be used by another active tracker.",
		Annotations: modifying("Rename tracker", true),
	}, h.renameTracker)

	addTool(server, h.errors, &mcp.Tool{
		Name: "delete_tracker",
		Description: "Delete a tracker. Without confirm=true this only previews what would be deleted. " +
			"Deleted trackers are hidden but can be brought back with restore_tracker.",
		Annotations: destructive("Delete tracker"),
	}, h.deleteTracker)

	addTool(server, h.errors, &mcp.Tool{
		Name:        "restore_tracker",
		Description: "Bring back a deleted tracker with all its rows. If its name is now taken, pass new_name.",
		Annotations: modifying("Restore tracker", false),
	}, h.restoreTracker)
}

func (h *handlers) createTracker(ctx context.Context, request CreateTrackerRequest) (TrackerResponse, error) {
	specs := make([]domain.ColumnSpec, len(request.Columns))
	for i, column := range request.Columns {
		specs[i] = toColumnSpec(column)
	}
	details, err := h.trackers.Create(ctx, service.CreateTrackerInput{Name: request.Name, Description: request.Description, Columns: specs})
	if err != nil {
		return TrackerResponse{}, err
	}
	return TrackerResponse{
		Message: fmt.Sprintf("Created tracker %q (id %d) with %d columns.", details.Tracker.Name, details.Tracker.ID, len(details.Tracker.Columns)),
		Tracker: toTrackerDTO(details),
	}, nil
}

func (h *handlers) listTrackers(ctx context.Context, request ListTrackersRequest) (ListTrackersResponse, error) {
	summaries, err := h.trackers.List(ctx, domain.TrackerListFilter{Search: request.Search, IncludeDeleted: request.IncludeDeleted})
	if err != nil {
		return ListTrackersResponse{}, err
	}
	dtos := make([]TrackerSummaryDTO, len(summaries))
	for i, summary := range summaries {
		dtos[i] = toTrackerSummaryDTO(summary)
	}
	return ListTrackersResponse{Trackers: dtos, Count: len(dtos)}, nil
}

func (h *handlers) describeTracker(ctx context.Context, request TrackerLookupRequest) (TrackerResponse, error) {
	details, err := h.trackers.Describe(ctx, toTrackerRef(request.Tracker, request.TrackerID))
	if err != nil {
		return TrackerResponse{}, err
	}
	return TrackerResponse{
		Message: fmt.Sprintf("Tracker %q has %d columns and %d rows.", details.Tracker.Name, len(details.Tracker.Columns), details.RowCount),
		Tracker: toTrackerDTO(details),
	}, nil
}

func (h *handlers) renameTracker(ctx context.Context, request RenameTrackerRequest) (TrackerResponse, error) {
	details, err := h.trackers.Rename(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.NewName)
	if err != nil {
		return TrackerResponse{}, err
	}
	return TrackerResponse{Message: fmt.Sprintf("Tracker renamed to %q.", details.Tracker.Name), Tracker: toTrackerDTO(details)}, nil
}

func (h *handlers) deleteTracker(ctx context.Context, request DeleteTrackerRequest) (DeleteTrackerResponse, error) {
	result, err := h.trackers.Delete(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.Confirm)
	if err != nil {
		return DeleteTrackerResponse{}, err
	}
	message := fmt.Sprintf("Preview only, nothing was deleted: tracker %q (id %d) with %d rows would be deleted. "+
		"Confirm with the user, then call again with confirm=true.", result.Tracker.Name, result.Tracker.ID, result.Tracker.RowCount)
	if result.Confirmed {
		message = fmt.Sprintf("Deleted tracker %q (id %d). It can be brought back with restore_tracker.", result.Tracker.Name, result.Tracker.ID)
	}
	return DeleteTrackerResponse{Message: message, Tracker: toTrackerSummaryDTO(result.Tracker), Confirmed: result.Confirmed}, nil
}

func (h *handlers) restoreTracker(ctx context.Context, request RestoreTrackerRequest) (TrackerResponse, error) {
	details, err := h.trackers.Restore(ctx, toTrackerRef(request.Tracker, request.TrackerID), request.NewName)
	if err != nil {
		return TrackerResponse{}, err
	}
	return TrackerResponse{
		Message: fmt.Sprintf("Restored tracker %q (id %d) with %d rows.", details.Tracker.Name, details.Tracker.ID, details.RowCount),
		Tracker: toTrackerDTO(details),
	}, nil
}
