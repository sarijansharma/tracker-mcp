// Package service holds the business rules of the tracker product. It depends only on the
// domain model and the repository contracts, never on a concrete database or transport.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
	"github.com/sarijansharma/tracker-mcp/internal/repository"
)

// TrackerRef identifies a tracker by id, by name, or both (they must then agree).
type TrackerRef struct {
	ID   int64
	Name string
}

func (r TrackerRef) String() string {
	if r.ID > 0 {
		return fmt.Sprintf("id %d", r.ID)
	}
	return fmt.Sprintf("%q", domain.CleanName(r.Name))
}

func (r TrackerRef) validate() error {
	if r.ID < 0 {
		return domain.InvalidInputf("tracker_id must be a positive number.")
	}
	if r.ID == 0 && domain.CleanName(r.Name) == "" {
		return domain.InvalidInputf("Tell me which tracker to use: pass its name (tracker) or its id (tracker_id).")
	}
	return nil
}

// resolveActiveTracker finds a live tracker. It never guesses: a misspelled or partial
// name produces a not-found error listing the closest matches, so the person can confirm.
func resolveActiveTracker(ctx context.Context, repos repository.Repositories, ref TrackerRef) (domain.Tracker, error) {
	if err := ref.validate(); err != nil {
		return domain.Tracker{}, err
	}
	if ref.ID > 0 {
		return resolveActiveByID(ctx, repos, ref)
	}
	return resolveActiveByName(ctx, repos, domain.CleanName(ref.Name))
}

func resolveActiveByID(ctx context.Context, repos repository.Repositories, ref TrackerRef) (domain.Tracker, error) {
	tracker, err := repos.Trackers().Get(ctx, ref.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.Tracker{}, domain.NotFoundf("There is no tracker with id %d. Use list_trackers to see available trackers.", ref.ID)
	}
	if err != nil {
		return domain.Tracker{}, err
	}
	if tracker.IsDeleted() {
		return domain.Tracker{}, domain.NotFoundf("Tracker %q (id %d) was deleted on %s. Use restore_tracker to bring it back.",
			tracker.Name, tracker.ID, tracker.DeletedAt.Format(domain.DateLayout))
	}
	if name := domain.CleanName(ref.Name); name != "" && !domain.SameName(name, tracker.Name) {
		return domain.Tracker{}, domain.InvalidInputf("tracker_id %d is the tracker %q, but the name %q was also given. Pass only one, or make them match.",
			tracker.ID, tracker.Name, name)
	}
	return tracker, nil
}

func resolveActiveByName(ctx context.Context, repos repository.Repositories, name string) (domain.Tracker, error) {
	tracker, err := repos.Trackers().FindActiveByName(ctx, name)
	if err == nil {
		return tracker, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return domain.Tracker{}, err
	}

	message := fmt.Sprintf("No tracker is named %q.", name)
	deleted, err := repos.Trackers().FindDeletedByName(ctx, name)
	if err != nil {
		return domain.Tracker{}, err
	}
	if len(deleted) > 0 {
		message += fmt.Sprintf(" A deleted tracker with that name exists: %s. Use restore_tracker to bring it back.", hints(deleted))
	}
	similar, err := repos.Trackers().SearchSimilar(ctx, name, domain.MaxSimilarSuggestions)
	if err != nil {
		return domain.Tracker{}, err
	}
	notFound := domain.NotFoundf("%s", message)
	if len(similar) == 0 && len(deleted) == 0 {
		notFound.Message += " Use list_trackers to see available trackers."
	}
	for _, summary := range similar {
		notFound.Suggestions = append(notFound.Suggestions, summary.Hint())
	}
	return domain.Tracker{}, notFound
}

// resolveDeletedTracker finds a deleted tracker to restore. Several deleted trackers can
// share a name (created and deleted at different times); then the caller must pick an id.
func resolveDeletedTracker(ctx context.Context, repos repository.Repositories, ref TrackerRef) (domain.Tracker, error) {
	if err := ref.validate(); err != nil {
		return domain.Tracker{}, err
	}
	if ref.ID > 0 {
		tracker, err := repos.Trackers().Get(ctx, ref.ID)
		if errors.Is(err, repository.ErrNotFound) {
			return domain.Tracker{}, domain.NotFoundf("There is no tracker with id %d.", ref.ID)
		}
		if err != nil {
			return domain.Tracker{}, err
		}
		if !tracker.IsDeleted() {
			return domain.Tracker{}, domain.Conflictf("Tracker %q (id %d) is not deleted, so there is nothing to restore.", tracker.Name, tracker.ID)
		}
		return tracker, nil
	}

	name := domain.CleanName(ref.Name)
	deleted, err := repos.Trackers().FindDeletedByName(ctx, name)
	if err != nil {
		return domain.Tracker{}, err
	}
	switch len(deleted) {
	case 0:
		if _, activeErr := repos.Trackers().FindActiveByName(ctx, name); activeErr == nil {
			return domain.Tracker{}, domain.Conflictf("Tracker %q is not deleted, so there is nothing to restore.", name)
		}
		return domain.Tracker{}, domain.NotFoundf("No deleted tracker is named %q. Use list_trackers with include_deleted=true to see deleted trackers.", name)
	case 1:
		return repos.Trackers().Get(ctx, deleted[0].ID)
	default:
		return domain.Tracker{}, domain.InvalidInputf("%d deleted trackers are named %q: %s. Pass tracker_id to choose one.",
			len(deleted), name, hints(deleted))
	}
}

func hints(summaries []domain.TrackerSummary) string {
	parts := make([]string, len(summaries))
	for i, summary := range summaries {
		parts[i] = summary.Hint()
	}
	return strings.Join(parts, "; ")
}
