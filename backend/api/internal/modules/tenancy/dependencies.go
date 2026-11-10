package tenancy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// ComponentRef names a component in a dependency.
type ComponentRef struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
}

// Dependency is a registered "From calls To" edge.
type Dependency struct {
	From        ComponentRef `json:"from"`
	To          ComponentRef `json:"to"`
	Environment string       `json:"environment"`
	CreatedAt   time.Time    `json:"created_at"`
}

// Downstream is a component reached from an incident's component: Depth 1
// is called directly; Depth 2 is called by ViaComponentID.
type Downstream struct {
	Component      ComponentRef `json:"component"`
	Depth          int          `json:"depth"`
	ViaComponentID *string      `json:"via_component_id"`
}

// ErrDependencyNotFound means no such edge is registered in the project.
var ErrDependencyNotFound = errors.New("dependency not found")

func invalidDependency(message string) *httpx.Error {
	return badRequest("invalid_dependency", message)
}

// ListDependencies lists a project's registered dependencies.
func (s Service) ListDependencies(ctx context.Context, orgID, projectID string) ([]Dependency, error) {
	if err := ProjectInOrg(ctx, s.DB, orgID, projectID); err != nil {
		return nil, err
	}
	return listDependencies(ctx, s.DB, projectID)
}

// AddDependency registers "from calls to". Both must be components of the
// project in the same environment, and different.
func (s Service) AddDependency(ctx context.Context, orgID, projectID, from, to string) (Dependency, error) {
	if err := ProjectInOrg(ctx, s.DB, orgID, projectID); err != nil {
		return Dependency{}, err
	}
	if !httpx.IsUUID(from) || !httpx.IsUUID(to) {
		return Dependency{}, invalidDependency("from and to must be component IDs")
	}
	if from == to {
		return Dependency{}, invalidDependency("a component cannot depend on itself")
	}
	fromEnv, err := componentScope(ctx, s.DB, projectID, from)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dependency{}, invalidDependency("from is not a component of this project")
	} else if err != nil {
		return Dependency{}, err
	}
	toEnv, err := componentScope(ctx, s.DB, projectID, to)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dependency{}, invalidDependency("to is not a component of this project")
	} else if err != nil {
		return Dependency{}, err
	}
	if fromEnv != toEnv {
		return Dependency{}, invalidDependency("both components must be in the same environment")
	}
	if _, err := insertDependency(ctx, s.DB, projectID, fromEnv, from, to); err != nil {
		if _, dup := database.UniqueViolation(err); dup {
			return Dependency{}, &httpx.Error{Status: http.StatusConflict, Code: "duplicate", Message: "this dependency is already registered"}
		}
		return Dependency{}, err
	}
	all, err := listDependencies(ctx, s.DB, projectID)
	if err != nil {
		return Dependency{}, err
	}
	for _, d := range all {
		if d.From.ID == from && d.To.ID == to {
			return d, nil
		}
	}
	return Dependency{}, ErrDependencyNotFound // deleted concurrently
}

// RemoveDependency unregisters "from calls to".
func (s Service) RemoveDependency(ctx context.Context, orgID, projectID, from, to string) error {
	if err := ProjectInOrg(ctx, s.DB, orgID, projectID); err != nil {
		return err
	}
	if !httpx.IsUUID(from) || !httpx.IsUUID(to) {
		return ErrDependencyNotFound
	}
	ok, err := deleteDependency(ctx, s.DB, projectID, from, to)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDependencyNotFound
	}
	return nil
}

// DownstreamOf returns what componentID calls, two hops at most. It does no
// organisation check: callers pass a component they have already scoped.
func DownstreamOf(ctx context.Context, q database.DBTX, componentID string) ([]Downstream, error) {
	return downstream(ctx, q, componentID)
}
