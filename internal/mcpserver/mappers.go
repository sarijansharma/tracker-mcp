package mcpserver

import (
	"strings"
	"time"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/service"
)

const emptyGroupLabel = "(empty)"

func toTrackerRef(name string, id int64) service.TrackerRef {
	return service.TrackerRef{ID: id, Name: name}
}

func toColumnSpec(dto ColumnDefinitionDTO) domain.ColumnSpec {
	return domain.ColumnSpec{
		Name: dto.Name, Type: dto.Type, Description: dto.Description,
		Required: dto.Required, Options: dto.Options, DefaultValue: dto.DefaultValue,
	}
}

func toColumnDTO(column domain.Column) ColumnDTO {
	operators := column.Type.SupportedOperators()
	names := make([]string, len(operators))
	for i, op := range operators {
		names[i] = string(op)
	}
	return ColumnDTO{
		Name: column.Name, Type: string(column.Type), Description: column.Description, Required: column.Required,
		Options: column.Options, DefaultValue: column.DefaultValue, FilterOperators: names,
	}
}

func toTrackerDTO(details service.TrackerDetails) TrackerDTO {
	tracker := details.Tracker
	columns := make([]ColumnDTO, len(tracker.Columns))
	for i, column := range tracker.Columns {
		columns[i] = toColumnDTO(column)
	}
	return TrackerDTO{
		ID: tracker.ID, Name: tracker.Name, Description: tracker.Description, CreatedBy: tracker.CreatedBy,
		CreatedAt: formatTime(tracker.CreatedAt), UpdatedAt: formatTime(tracker.UpdatedAt),
		Columns: columns, RowCount: details.RowCount,
	}
}

func toTrackerSummaryDTO(summary domain.TrackerSummary) TrackerSummaryDTO {
	dto := TrackerSummaryDTO{
		ID: summary.ID, Name: summary.Name, Description: summary.Description, ColumnCount: summary.ColumnCount,
		RowCount: summary.RowCount, CreatedBy: summary.CreatedBy, CreatedAt: formatTime(summary.CreatedAt),
	}
	if summary.DeletedAt != nil {
		dto.DeletedAt = formatTime(*summary.DeletedAt)
	}
	return dto
}

func toTrackerRefDTO(tracker domain.Tracker) TrackerRefDTO {
	return TrackerRefDTO{ID: tracker.ID, Name: tracker.Name}
}

// toRowDTOs converts stored rows to column-name keyed values. Keys of columns that no
// longer exist (e.g. removed concurrently) are skipped.
func toRowDTOs(tracker domain.Tracker, rows []domain.Row) []RowDTO {
	dtos := make([]RowDTO, len(rows))
	for i, row := range rows {
		values := make(map[string]any, len(row.Values))
		for key, value := range row.Values {
			if column, ok := tracker.FindColumnByKey(key); ok {
				values[column.Name] = value
			}
		}
		dtos[i] = RowDTO{
			ID: row.ID, Values: values, CreatedBy: row.CreatedBy,
			CreatedAt: formatTime(row.CreatedAt), UpdatedAt: formatTime(row.UpdatedAt),
		}
	}
	return dtos
}

func toFilterInputs(dtos []FilterDTO) []service.FilterInput {
	inputs := make([]service.FilterInput, len(dtos))
	for i, dto := range dtos {
		inputs[i] = service.FilterInput{Column: dto.Column, Operator: dto.Operator, Value: dto.Value}
	}
	return inputs
}

func toSortInputs(dtos []SortDTO) ([]service.SortInput, error) {
	inputs := make([]service.SortInput, len(dtos))
	for i, dto := range dtos {
		switch strings.ToLower(strings.TrimSpace(dto.Direction)) {
		case "", "asc", "ascending", "oldest", "lowest":
		case "desc", "descending", "newest", "highest":
			inputs[i].Descending = true
		default:
			return nil, domain.InvalidInputf("Sort direction must be asc or desc, got %q.", dto.Direction)
		}
		inputs[i].Column = dto.Column
	}
	return inputs, nil
}

func toNamedValues(values map[string]any) service.NamedValues {
	return service.NamedValues(values)
}

func toGroupCountDTOs(groups []domain.GroupCount) ([]GroupCountDTO, int64) {
	dtos := make([]GroupCountDTO, len(groups))
	var total int64
	for i, group := range groups {
		label := emptyGroupLabel
		if group.Value != nil {
			label = *group.Value
		}
		dtos[i] = GroupCountDTO{Value: label, Count: group.Count}
		total += group.Count
	}
	return dtos, total
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
