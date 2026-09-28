package mcpserver

// Data transfer objects: the exact JSON shapes MCP clients send and receive. Field tags
// double as the tool input/output JSON schema, so descriptions are written for the AI
// assistant that fills them in. Fields without `omitempty` are required.

// ---- shared ----

// ColumnDefinitionDTO defines a column when creating a tracker or adding a column.
type ColumnDefinitionDTO struct {
	Name         string   `json:"name" jsonschema:"Column name as people will say it, e.g. Candidate Name"`
	Type         string   `json:"type" jsonschema:"One of: text, number, boolean, date, datetime, single_select, multi_select, email, url"`
	Description  string   `json:"description,omitempty" jsonschema:"Optional explanation of what goes in this column"`
	Required     bool     `json:"required,omitempty" jsonschema:"If true, every row must have a value (default false)"`
	Options      []string `json:"options,omitempty" jsonschema:"Allowed values; required for single_select and multi_select, not allowed otherwise"`
	DefaultValue any      `json:"default_value,omitempty" jsonschema:"Value used when a new row leaves this column out; also fills existing rows when the column is added"`
}

// ColumnDTO describes an existing column.
type ColumnDTO struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Description     string   `json:"description,omitempty"`
	Required        bool     `json:"required"`
	Options         []string `json:"options,omitempty"`
	DefaultValue    any      `json:"default_value,omitempty"`
	FilterOperators []string `json:"filter_operators" jsonschema:"Operators query_rows accepts for this column"`
}

// TrackerDTO is a tracker with its columns.
type TrackerDTO struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	CreatedBy   string      `json:"created_by,omitempty"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
	Columns     []ColumnDTO `json:"columns"`
	RowCount    int64       `json:"row_count"`
}

// TrackerSummaryDTO is a tracker in a listing.
type TrackerSummaryDTO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ColumnCount int64  `json:"column_count"`
	RowCount    int64  `json:"row_count"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	DeletedAt   string `json:"deleted_at,omitempty"`
}

// TrackerRefDTO names the tracker a result belongs to.
type TrackerRefDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// RowDTO is one tracker entry, with values keyed by column name.
type RowDTO struct {
	ID        int64          `json:"id"`
	Values    map[string]any `json:"values"`
	CreatedBy string         `json:"created_by,omitempty"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

// FilterDTO is one query condition.
type FilterDTO struct {
	Column   string `json:"column" jsonschema:"Column name, or a built-in field: id, created_at, updated_at"`
	Operator string `json:"operator" jsonschema:"eq, neq, gt, gte, lt, lte, contains, not_contains, starts_with, in, not_in, is_empty, is_not_empty. Text comparisons ignore case. neq/not_in/not_contains also match empty values."`
	Value    any    `json:"value,omitempty" jsonschema:"Value to compare with; a list for in/not_in; omit for is_empty/is_not_empty. Dates as YYYY-MM-DD."`
}

// SortDTO orders query results.
type SortDTO struct {
	Column    string `json:"column" jsonschema:"Column name, or id, created_at, updated_at"`
	Direction string `json:"direction,omitempty" jsonschema:"asc (default) or desc; empty values always sort last"`
}

// ---- tracker tools ----

type CreateTrackerRequest struct {
	Name        string                `json:"name" jsonschema:"Unique tracker name, e.g. Interview Tracker 2026 (uniqueness ignores case)"`
	Description string                `json:"description,omitempty" jsonschema:"What the tracker is for"`
	Columns     []ColumnDefinitionDTO `json:"columns" jsonschema:"At least one column"`
}

type TrackerResponse struct {
	Message string     `json:"message"`
	Tracker TrackerDTO `json:"tracker"`
}

type ListTrackersRequest struct {
	Search         string `json:"search,omitempty" jsonschema:"Optional part of a name to look for; tolerant of typos"`
	IncludeDeleted bool   `json:"include_deleted,omitempty" jsonschema:"Also list deleted trackers (they can be restored)"`
}

type ListTrackersResponse struct {
	Trackers []TrackerSummaryDTO `json:"trackers"`
	Count    int                 `json:"count"`
}

type TrackerLookupRequest struct {
	Tracker   string `json:"tracker,omitempty" jsonschema:"Tracker name (case-insensitive, must match fully). Either tracker or tracker_id is required."`
	TrackerID int64  `json:"tracker_id,omitempty" jsonschema:"Tracker id; preferred when several trackers have similar names"`
}

type RenameTrackerRequest struct {
	Tracker   string `json:"tracker,omitempty" jsonschema:"Current tracker name. Either tracker or tracker_id is required."`
	TrackerID int64  `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	NewName   string `json:"new_name" jsonschema:"The new unique name"`
}

type DeleteTrackerRequest struct {
	Tracker   string `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64  `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"false (default) only previews; set true only after the user explicitly agreed"`
}

type DeleteTrackerResponse struct {
	Message   string            `json:"message"`
	Tracker   TrackerSummaryDTO `json:"tracker"`
	Confirmed bool              `json:"confirmed"`
}

type RestoreTrackerRequest struct {
	Tracker   string `json:"tracker,omitempty" jsonschema:"Name of the deleted tracker. Either tracker or tracker_id is required."`
	TrackerID int64  `json:"tracker_id,omitempty" jsonschema:"Id of the deleted tracker; needed when several deleted trackers share a name"`
	NewName   string `json:"new_name,omitempty" jsonschema:"Restore under this name, e.g. when the old name is now used by another tracker"`
}

// ---- column tools ----

type AddColumnRequest struct {
	Tracker   string              `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64               `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Column    ColumnDefinitionDTO `json:"column"`
}

type AddColumnResponse struct {
	Message            string    `json:"message"`
	Column             ColumnDTO `json:"column"`
	ExistingRowsFilled int64     `json:"existing_rows_filled" jsonschema:"Existing rows that received the default value"`
}

type UpdateColumnRequest struct {
	Tracker            string   `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID          int64    `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Column             string   `json:"column" jsonschema:"Name of the column to change"`
	Description        *string  `json:"description,omitempty" jsonschema:"New description"`
	Required           *bool    `json:"required,omitempty" jsonschema:"Make the column required or optional. Making it required fills empty rows with the default value, or fails if there is none."`
	AddOptions         []string `json:"add_options,omitempty" jsonschema:"Options to add to a select column"`
	RemoveOptions      []string `json:"remove_options,omitempty" jsonschema:"Options to remove; refused while rows still use them"`
	DefaultValue       any      `json:"default_value,omitempty" jsonschema:"New default value for rows that leave this column out"`
	RemoveDefaultValue bool     `json:"remove_default_value,omitempty" jsonschema:"Remove the default value"`
}

type UpdateColumnResponse struct {
	Message            string    `json:"message"`
	Column             ColumnDTO `json:"column"`
	ExistingRowsFilled int64     `json:"existing_rows_filled"`
}

type RenameColumnRequest struct {
	Tracker   string `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64  `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Column    string `json:"column" jsonschema:"Current column name"`
	NewName   string `json:"new_name" jsonschema:"New column name"`
}

type ColumnResponse struct {
	Message string    `json:"message"`
	Column  ColumnDTO `json:"column"`
}

type RemoveColumnRequest struct {
	Tracker   string `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64  `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Column    string `json:"column" jsonschema:"Name of the column to remove"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"false (default) only previews; set true only after the user explicitly agreed. Values in this column are permanently lost."`
}

type RemoveColumnResponse struct {
	Message          string   `json:"message"`
	Column           string   `json:"column"`
	RowsWithValues   int64    `json:"rows_with_values" jsonschema:"Rows that had (or have) a value in this column"`
	Confirmed        bool     `json:"confirmed"`
	RemainingColumns []string `json:"remaining_columns"`
}

// ---- row tools ----

type AddRowsRequest struct {
	Tracker   string           `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64            `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Rows      []map[string]any `json:"rows" jsonschema:"One object per row mapping column name to value. All rows are added, or none if any is invalid."`
}

type RowsResponse struct {
	Message string        `json:"message"`
	Tracker TrackerRefDTO `json:"tracker"`
	Rows    []RowDTO      `json:"rows"`
}

type UpdateRowRequest struct {
	Tracker   string         `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64          `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	RowID     int64          `json:"row_id" jsonschema:"Id of the row to change (from query_rows)"`
	Values    map[string]any `json:"values" jsonschema:"Column name to new value. Only these columns change; null clears a value."`
}

type DeleteRowsRequest struct {
	Tracker   string  `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64   `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	RowIDs    []int64 `json:"row_ids" jsonschema:"Ids of the rows to delete (from query_rows)"`
	Confirm   bool    `json:"confirm,omitempty" jsonschema:"false (default) only previews; set true only after the user explicitly agreed. Deleted rows can't be recovered."`
}

type DeleteRowsResponse struct {
	Message    string        `json:"message"`
	Tracker    TrackerRefDTO `json:"tracker"`
	Rows       []RowDTO      `json:"rows" jsonschema:"Rows deleted, or that would be deleted when not confirmed"`
	MissingIDs []int64       `json:"missing_ids,omitempty" jsonschema:"Requested ids that don't exist in this tracker"`
	Confirmed  bool          `json:"confirmed"`
}

type QueryRowsRequest struct {
	Tracker   string      `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64       `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	Filters   []FilterDTO `json:"filters,omitempty" jsonschema:"Conditions rows must meet; omit to get all rows"`
	Match     string      `json:"match,omitempty" jsonschema:"all (default): every filter must match; any: at least one"`
	SortBy    []SortDTO   `json:"sort_by,omitempty" jsonschema:"Sort order; default is oldest first"`
	Limit     int         `json:"limit,omitempty" jsonschema:"Rows per page, default 50, max 500"`
	Offset    int         `json:"offset,omitempty" jsonschema:"Rows to skip, for paging"`
}

type QueryRowsResponse struct {
	Tracker       TrackerRefDTO `json:"tracker"`
	Columns       []string      `json:"columns"`
	Rows          []RowDTO      `json:"rows"`
	TotalMatching int64         `json:"total_matching"`
	Returned      int           `json:"returned"`
	Offset        int           `json:"offset"`
	HasMore       bool          `json:"has_more"`
	NextOffset    int           `json:"next_offset,omitempty"`
}

type SummarizeRowsRequest struct {
	Tracker   string      `json:"tracker,omitempty" jsonschema:"Tracker name. Either tracker or tracker_id is required."`
	TrackerID int64       `json:"tracker_id,omitempty" jsonschema:"Tracker id"`
	GroupBy   string      `json:"group_by" jsonschema:"Column to count rows by, e.g. Status"`
	Filters   []FilterDTO `json:"filters,omitempty" jsonschema:"Only count rows matching these filters"`
	Match     string      `json:"match,omitempty" jsonschema:"all (default) or any"`
}

type GroupCountDTO struct {
	Value string `json:"value" jsonschema:"The column value, or (empty) for rows without one"`
	Count int64  `json:"count"`
}

type SummarizeRowsResponse struct {
	Tracker   TrackerRefDTO   `json:"tracker"`
	GroupBy   string          `json:"group_by"`
	Groups    []GroupCountDTO `json:"groups"`
	TotalRows int64           `json:"total_rows" jsonschema:"Sum of counts (multi_select rows count once per option)"`
	Truncated bool            `json:"truncated,omitempty"`
}
