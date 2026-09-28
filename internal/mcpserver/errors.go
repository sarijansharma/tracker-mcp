package mcpserver

import (
	"context"
	"errors"
	"log/slog"

	"github.com/sarijansharma/tracker-mcp/internal/domain"
)

// errorPresenter turns failures into messages that are safe and useful to show. Domain
// errors are passed through (they are written for people); anything else is logged and
// replaced with a generic message so database internals never leak to clients.
type errorPresenter struct {
	logger *slog.Logger
}

func (p errorPresenter) present(ctx context.Context, tool string, err error) error {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return domainErr
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errors.New("the request took too long or was cancelled; please try again, perhaps with narrower filters")
	}
	p.logger.ErrorContext(ctx, "tool failed", slog.String("tool", tool), slog.Any("error", err))
	return errors.New("something went wrong on the tracker server; please try again, and contact the administrator if it keeps happening")
}
