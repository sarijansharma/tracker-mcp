package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

// Actor names whoever performs an operation. v1 has no authentication, so it comes from
// configuration; this is the seam where an authenticated identity would plug in.
type Actor string

// TrackerService manages trackers as a whole: create, find, rename, delete, restore.
type TrackerService struct {
	store repository.Store
	actor Actor
}

func NewTrackerService(store repository.Store, actor Actor) *TrackerService {
	return &TrackerService{store: store, actor: actor}
}

// CreateTrackerInput describes a new tracker and its initial columns.
type CreateTrackerInput struct {
	Name        string
	Description string
	Columns     []domain.ColumnSpec
}

// TrackerDetails is a tracker with its columns and how many rows it holds.
type TrackerDetails struct {
	Tracker  domain.Tracker
	RowCount int64
}

func (s *TrackerService) Create(ctx context.Context, input CreateTrackerInput) (TrackerDetails, error) {
	name, err := domain.ValidateName("tracker name", input.Name, domain.MaxTrackerNameLength)
	if err != nil {
		return TrackerDetails{}, err
	}
	description, err := validateDescription(input.Description)
	if err != nil {
		return TrackerDetails{}, err
	}
	columns, err := validateInitialColumns(input.Columns)
	if err != nil {
		return TrackerDetails{}, err
	}

	var created domain.Tracker
	err = s.store.WithinTransaction(ctx, func(repos repository.Repositories) error {
		tracker, err := repos.Trackers().Create(ctx, repository.NewTracker{Name: name, Description: description, CreatedBy: string(s.actor)})
		if err != nil {
			return err
		}
		for position, column := range columns {
			column.TrackerID, column.Position = tracker.ID, position
			stored, err := repos.Columns().Create(ctx, column)
			if err != nil {
				return err
			}
			tracker.Columns = append(tracker.Columns, stored)
		}
		created = tracker
		return nil
	})
	if errors.Is(err, repository.ErrDuplicateName) {
		return TrackerDetails{}, s.duplicateTrackerNameError(ctx, name)
	}
	if err != nil {
		return TrackerDetails{}, err
	}
	return TrackerDetails{Tracker: created}, nil
}

// duplicateTrackerNameError explains a name clash and points at the existing tracker.
func (s *TrackerService) duplicateTrackerNameError(ctx context.Context, name string) error {
	message := fmt.Sprintf("A tracker named %q already exists", name)
	if existing, err := s.store.Trackers().FindActiveByName(ctx, name); err == nil {
		if summary, err := s.store.Trackers().Summary(ctx, existing.ID); err == nil {
			message = "A tracker already uses this name: " + summary.Hint()
		}
	}
	return domain.AlreadyExistsf("%s. Tracker names must be unique (ignoring case). Add to the existing tracker, "+
		"or pick a distinguishing name such as %q or %q.", message, name+" 2026", name+" - Team A")
}

func validateInitialColumns(specs []domain.ColumnSpec) ([]domain.Column, error) {
	if len(specs) == 0 {
		return nil, domain.InvalidInputf("A tracker needs at least one column, e.g. {\"name\": \"Title\", \"type\": \"text\"}.")
	}
	if len(specs) > domain.MaxColumnsPerTracker {
		return nil, domain.InvalidInputf("A tracker can have at most %d columns.", domain.MaxColumnsPerTracker)
	}
	problems := &domain.ValidationErrors{}
	columns := make([]domain.Column, 0, len(specs))
	seen := make(map[string]bool, len(specs))
	for i, spec := range specs {
		column, err := spec.Validate()
		if err != nil {
			problems.Addf("column %d: %v", i+1, err)
			continue
		}
		key := strings.ToLower(column.Name)
		if seen[key] {
			problems.Addf("column %d: the name %q is used more than once", i+1, column.Name)
			continue
		}
		seen[key] = true
		columns = append(columns, column)
	}
	if err := problems.AsError("Some columns are not valid:"); err != nil {
		return nil, err
	}
	return columns, nil
}

func validateDescription(raw string) (string, error) {
	description := strings.TrimSpace(raw)
	if len(description) > domain.MaxTrackerDescriptionLength {
		return "", domain.InvalidInputf("The description is too long (max %d characters).", domain.MaxTrackerDescriptionLength)
	}
	return description, nil
}

func (s *TrackerService) List(ctx context.Context, filter domain.TrackerListFilter) ([]domain.TrackerSummary, error) {
	filter.Search = domain.CleanName(filter.Search)
	return s.store.Trackers().List(ctx, filter)
}

func (s *TrackerService) Describe(ctx context.Context, ref TrackerRef) (TrackerDetails, error) {
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return TrackerDetails{}, err
	}
	rowCount, err := s.store.Rows().CountRows(ctx, tracker.ID)
	if err != nil {
		return TrackerDetails{}, err
	}
	return TrackerDetails{Tracker: tracker, RowCount: rowCount}, nil
}

func (s *TrackerService) Rename(ctx context.Context, ref TrackerRef, newName string) (TrackerDetails, error) {
	name, err := domain.ValidateName("new tracker name", newName, domain.MaxTrackerNameLength)
	if err != nil {
		return TrackerDetails{}, err
	}
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return TrackerDetails{}, err
	}
	if tracker.Name == name {
		return TrackerDetails{}, domain.InvalidInputf("Tracker %q already has that name.", name)
	}
	err = s.store.Trackers().Rename(ctx, tracker.ID, name)
	if errors.Is(err, repository.ErrDuplicateName) {
		return TrackerDetails{}, s.duplicateTrackerNameError(ctx, name)
	}
	if err != nil {
		return TrackerDetails{}, err
	}
	return s.Describe(ctx, TrackerRef{ID: tracker.ID})
}

// DeleteTrackerResult reports what a delete did, or would do when not confirmed.
type DeleteTrackerResult struct {
	Tracker   domain.TrackerSummary
	Confirmed bool
}

// Delete soft-deletes a tracker (restorable). Without confirm it only previews the impact.
func (s *TrackerService) Delete(ctx context.Context, ref TrackerRef, confirm bool) (DeleteTrackerResult, error) {
	tracker, err := resolveActiveTracker(ctx, s.store, ref)
	if err != nil {
		return DeleteTrackerResult{}, err
	}
	summary, err := s.store.Trackers().Summary(ctx, tracker.ID)
	if err != nil {
		return DeleteTrackerResult{}, err
	}
	if !confirm {
		return DeleteTrackerResult{Tracker: summary}, nil
	}
	if err := s.store.Trackers().SoftDelete(ctx, tracker.ID); err != nil {
		return DeleteTrackerResult{}, err
	}
	summary, err = s.store.Trackers().Summary(ctx, tracker.ID)
	return DeleteTrackerResult{Tracker: summary, Confirmed: true}, err
}

// Restore brings back a deleted tracker, optionally under a new name when the old one has
// since been reused.
func (s *TrackerService) Restore(ctx context.Context, ref TrackerRef, newName string) (TrackerDetails, error) {
	tracker, err := resolveDeletedTracker(ctx, s.store, ref)
	if err != nil {
		return TrackerDetails{}, err
	}
	name := tracker.Name
	if strings.TrimSpace(newName) != "" {
		if name, err = domain.ValidateName("new tracker name", newName, domain.MaxTrackerNameLength); err != nil {
			return TrackerDetails{}, err
		}
	}
	err = s.store.Trackers().Restore(ctx, tracker.ID, name)
	if errors.Is(err, repository.ErrDuplicateName) {
		return TrackerDetails{}, domain.AlreadyExistsf("Another active tracker is already named %q. Restore this one with a new_name, e.g. %q.",
			name, fmt.Sprintf("%s (restored)", name))
	}
	if err != nil {
		return TrackerDetails{}, err
	}
	return s.Describe(ctx, TrackerRef{ID: tracker.ID})
}
