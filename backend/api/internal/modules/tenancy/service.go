// Package tenancy manages projects and the components inside them. Every
// operation is scoped to the caller's organization.
package tenancy

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
	"github.com/Gigman2/sentinel/backend/api/internal/platform/httpx"
)

// ErrNotFound means the project does not exist or belongs to another organization.
var ErrNotFound = errors.New("project not found")

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	// EnvironmentPattern is the allowed shape of an environment name.
	EnvironmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

// Project is a collection of components within an organization.
type Project struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Component is an operated service or dependency in one environment.
type Component struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Environment string    `json:"environment"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Description *string   `json:"description"`
	Owner       *string   `json:"owner"`
	CreatedAt   time.Time `json:"created_at"`
}

// ComponentInput is the body of a component create request.
type ComponentInput struct {
	Slug        string  `json:"slug"`
	DisplayName string  `json:"display_name"`
	Environment string  `json:"environment"`
	Description *string `json:"description"`
	Owner       *string `json:"owner"`
}

// ProjectInOrg returns nil if projectID is a project in orgID, ErrNotFound
// if not (including a malformed ID), or a database error.
func ProjectInOrg(ctx context.Context, q database.DBTX, orgID, projectID string) error {
	if !httpx.IsUUID(projectID) {
		return ErrNotFound
	}
	ok, err := projectExistsInOrg(ctx, q, orgID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func badRequest(code, message string) *httpx.Error {
	return &httpx.Error{Status: http.StatusBadRequest, Code: code, Message: message}
}

// Service manages projects and components.
type Service struct {
	DB database.DBTX
}

// CreateProject creates a project in orgID.
func (s Service) CreateProject(ctx context.Context, orgID, slug, name string) (Project, error) {
	if !slugPattern.MatchString(slug) {
		return Project{}, badRequest("invalid_slug", "slug must be 2–63 characters of lowercase letters, digits and hyphens, starting with a letter or digit")
	}
	if strings.TrimSpace(name) == "" {
		return Project{}, badRequest("missing_field", "missing required field name")
	}
	p, err := insertProject(ctx, s.DB, orgID, slug, name)
	if _, dup := database.UniqueViolation(err); dup {
		return Project{}, &httpx.Error{Status: http.StatusConflict, Code: "slug_taken", Message: "a project with slug " + slug + " already exists"}
	}
	return p, err
}

// ListProjects lists orgID's projects.
func (s Service) ListProjects(ctx context.Context, orgID string) ([]Project, error) {
	return listProjects(ctx, s.DB, orgID)
}

// CreateComponent creates a component in a project of orgID.
func (s Service) CreateComponent(ctx context.Context, orgID, projectID string, in ComponentInput) (Component, error) {
	if err := ProjectInOrg(ctx, s.DB, orgID, projectID); err != nil {
		return Component{}, err
	}
	switch {
	case !slugPattern.MatchString(in.Slug):
		return Component{}, badRequest("invalid_slug", "slug must be 2–63 characters of lowercase letters, digits and hyphens, starting with a letter or digit")
	case strings.TrimSpace(in.DisplayName) == "":
		return Component{}, badRequest("missing_field", "missing required field display_name")
	case in.Environment == "":
		return Component{}, badRequest("missing_field", "missing required field environment")
	case !EnvironmentPattern.MatchString(in.Environment):
		return Component{}, badRequest("invalid_field", "field environment must be 1–32 characters of lowercase letters, digits, '-' and '_'")
	}
	c, err := insertComponent(ctx, s.DB, orgID, projectID, in)
	if _, dup := database.UniqueViolation(err); dup {
		return Component{}, &httpx.Error{Status: http.StatusConflict, Code: "component_exists",
			Message: "component " + in.Slug + " already exists in environment " + in.Environment}
	}
	return c, err
}

// ListComponents lists the components of a project in orgID.
func (s Service) ListComponents(ctx context.Context, orgID, projectID string) ([]Component, error) {
	if err := ProjectInOrg(ctx, s.DB, orgID, projectID); err != nil {
		return nil, err
	}
	return listComponents(ctx, s.DB, projectID)
}
