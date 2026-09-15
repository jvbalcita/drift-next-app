package transportconnect

import (
	"context"
	"strconv"
	"strings"
	"time"

	driftv1 "drift.local/drift-next/gen/go/drift/v1"
	"drift.local/drift-next/internal/organizations"
	store "drift.local/drift-next/internal/store/sqlite"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	productActor    = "operator"
)

func parsePage(page *driftv1.PageRequest) (offset, limit int, err error) {
	limit = defaultPageSize
	if page != nil {
		if page.GetPageSize() > 0 {
			limit = int(page.GetPageSize())
		}
		if token := strings.TrimSpace(page.GetPageToken()); token != "" {
			parsed, parseErr := strconv.Atoi(token)
			if parseErr != nil || parsed < 0 {
				return 0, 0, invalidArgument("page token must be a decimal offset")
			}
			offset = parsed
		}
	}
	if limit > maxPageSize {
		return 0, 0, invalidArgument("page size exceeds the maximum of 200")
	}
	return offset, limit, nil
}

func applyPage[T any](items []T, offset, limit int) (page []T, next string) {
	if offset >= len(items) {
		return []T{}, ""
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page = items[offset:end]
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return page, next
}

func pageResponse(next string) *driftv1.PageResponse {
	if next == "" {
		return &driftv1.PageResponse{}
	}
	return &driftv1.PageResponse{NextPageToken: next}
}

func lookupWorkspace(ctx context.Context, db *store.DB, ref *driftv1.WorkspaceRef) (organizations.WorkspaceID, error) {
	if err := validateWorkspace(ref); err != nil {
		return "", err
	}
	id := organizations.WorkspaceID(ref.GetWorkspaceId())
	if _, err := store.NewWorkspaceRepository(db).Get(ctx, id); err != nil {
		return "", MapError(err)
	}
	return id, nil
}

func lookupResourceWorkspace(ctx context.Context, db *store.DB, ref *driftv1.ResourceRef) (organizations.WorkspaceID, string, error) {
	if ref == nil {
		return "", "", invalidArgument("resource is required")
	}
	workspace, err := lookupWorkspace(ctx, db, ref.GetWorkspace())
	if err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(ref.GetResourceId())
	if id == "" {
		return "", "", invalidArgument("resource ID is required")
	}
	return workspace, id, nil
}

func actorFromContext(requestContext *driftv1.RequestContext) (string, error) {
	if err := validateRequestID(requestContext); err != nil {
		return "", err
	}
	return requestContext.GetRequestId(), nil
}

func requireActor(requestContext *driftv1.RequestContext) (actorType, actorID string, err error) {
	actorID, err = actorFromContext(requestContext)
	if err != nil {
		return "", "", err
	}
	return productActor, actorID, nil
}

func workspaceRef(id organizations.WorkspaceID) *driftv1.WorkspaceRef {
	return &driftv1.WorkspaceRef{WorkspaceId: string(id)}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatTime(*value)
}

func newID(db *store.DB) (string, error) {
	if db == nil || db.IDs() == nil {
		return "", invalidArgument("identifier generator is required")
	}
	id, err := db.IDs().NewID()
	if err != nil {
		return "", MapError(err)
	}
	return id, nil
}
