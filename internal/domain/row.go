package domain

import "time"

// RowValues maps column storage keys to normalised values. Empty values are absent.
type RowValues map[string]any

// Row is one entry in a tracker.
type Row struct {
	ID        int64
	TrackerID int64
	Values    RowValues
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RowPatch changes some fields of a row: Set overwrites values, Unset clears them.
// Fields not mentioned are left untouched, so concurrent edits to different fields don't
// overwrite each other.
type RowPatch struct {
	TrackerID int64
	RowID     int64
	Set       RowValues
	Unset     []string
}
