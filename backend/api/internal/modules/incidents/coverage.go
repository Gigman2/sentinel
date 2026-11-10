package incidents

import (
	"context"
	"fmt"

	"github.com/Gigman2/sentinel/backend/api/internal/platform/database"
)

// Evidence source statuses.
const (
	SourceRetrieved      = "retrieved"
	SourceNoMatchingData = "no_matching_data"
	SourceNotConnected   = "not_connected"
)

// EvidenceSource says whether one source contributed evidence to an
// incident. Integration fields are null for the fixed Logs and Metrics
// entries, which Phase 1 cannot connect.
type EvidenceSource struct {
	IntegrationID *string `json:"integration_id"`
	Name          string  `json:"name"`
	Capability    *string `json:"capability"`
	EventCount    int     `json:"event_count"`
	Status        string  `json:"status"`
}

// evidenceSources lists every integration in the incident's project with the
// number of its events linked to the incident, then Logs and Metrics as not
// connected.
func evidenceSources(ctx context.Context, q database.DBTX, projectID, incidentID string) ([]EvidenceSource, error) {
	rows, err := integrationEventCounts(ctx, q, projectID, incidentID)
	if err != nil {
		return nil, fmt.Errorf("read evidence sources: %w", err)
	}
	out := make([]EvidenceSource, 0, len(rows)+2)
	for _, r := range rows {
		status := SourceNoMatchingData
		if r.EventCount > 0 {
			status = SourceRetrieved
		}
		id, capability := r.ID, r.Capability
		out = append(out, EvidenceSource{IntegrationID: &id, Name: r.Name, Capability: &capability, EventCount: r.EventCount, Status: status})
	}
	return append(out,
		EvidenceSource{Name: "Logs", Status: SourceNotConnected},
		EvidenceSource{Name: "Metrics", Status: SourceNotConnected},
	), nil
}
