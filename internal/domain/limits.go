package domain

// Limits that keep a single tool call bounded and predictable.
const (
	MaxTrackerNameLength        = 100
	MaxTrackerDescriptionLength = 1000
	MaxColumnNameLength         = 64
	MaxColumnDescriptionLength  = 500
	MaxColumnsPerTracker        = 100
	MaxOptionsPerColumn         = 200
	MaxOptionLength             = 100
	MaxTextValueLength          = 10000
	MaxRowsPerInsert            = 200
	MaxRowIDsPerCall            = 500
	MaxFiltersPerQuery          = 20
	MaxSortsPerQuery            = 5
	DefaultQueryLimit           = 50
	MaxQueryLimit               = 500
	MaxSummaryGroups            = 100
	MaxSimilarSuggestions       = 5
)
