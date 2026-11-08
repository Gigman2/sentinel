package seed_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Gigman2/sentinel/backend/api/internal/auth"
	"github.com/Gigman2/sentinel/backend/api/internal/seed"
	"github.com/Gigman2/sentinel/backend/api/internal/testutil"
)

func TestSeed_IdempotentAndScoped(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	cfg := seed.Config{OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test"}

	first, err := seed.Seed(ctx, pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.AdminToken, "sk_usr_") ||
		!strings.HasPrefix(first.MonitorToken, "sk_int_") ||
		!strings.HasPrefix(first.DeployToken, "sk_int_") {
		t.Fatalf("first run should generate tokens: %+v", first)
	}
	if first.Components["checkout-api"] == "" || first.Components["payment-service"] == "" {
		t.Fatalf("components missing: %v", first.Components)
	}

	second, err := seed.Seed(ctx, pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second.OrgID != first.OrgID || second.ProjectID != first.ProjectID ||
		second.MonitorIntegrationID != first.MonitorIntegrationID || second.DeployIntegrationID != first.DeployIntegrationID ||
		second.Components["payment-service"] != first.Components["payment-service"] {
		t.Fatalf("re-run changed IDs:\n%+v\n%+v", first, second)
	}
	if second.AdminToken != "" || second.MonitorToken != "" || second.DeployToken != "" {
		t.Fatalf("re-run must not return plaintext tokens: %+v", second)
	}

	for table, want := range map[string]int{
		"organizations": 1, "projects": 1, "components": 2, "integrations": 2,
		"integration_component_mappings": 3, "api_tokens": 1, "memberships": 1,
	} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s: %d rows, want %d", table, n, want)
		}
	}

	other, err := seed.Seed(ctx, pool, seed.Config{OrgSlug: "otherco", OrgName: "Other", AdminEmail: "admin@other.test"})
	if err != nil {
		t.Fatal(err)
	}
	if other.OrgID == first.OrgID || other.ProjectID == first.ProjectID {
		t.Fatal("second org shares IDs with the first")
	}
}

func TestSeed_SuppliedTokensAndReconciliation(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	cfg := seed.Config{
		OrgSlug: "uppsel", OrgName: "Uppsel", AdminEmail: "admin@uppsel.test",
		AdminToken: "sk_usr_supplied", MonitorToken: "sk_int_monitor1", DeployToken: "sk_int_deploy1",
	}
	res, err := seed.Seed(ctx, pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.AdminToken != "" || res.MonitorToken != "" {
		t.Fatal("supplied tokens must not be echoed back")
	}

	hashOf := func(integrationID string) string {
		var h string
		if err := pool.QueryRow(ctx, `SELECT credential_hash FROM integrations WHERE id = $1`, integrationID).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if hashOf(res.MonitorIntegrationID) != auth.Hash("sk_int_monitor1") {
		t.Fatal("monitor credential hash not stored")
	}

	// Drift the seeded state, then re-seed with a new monitor token.
	if _, err := pool.Exec(ctx, `UPDATE memberships SET role = 'member'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE integrations SET capability = 'change_source' WHERE id = $1`, res.MonitorIntegrationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE integration_component_mappings SET component_id = $1 WHERE external_component_id = 'payments-prod'`, res.Components["checkout-api"]); err != nil {
		t.Fatal(err)
	}

	cfg.MonitorToken = "sk_int_monitor2"
	if _, err := seed.Seed(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	if hashOf(res.MonitorIntegrationID) != auth.Hash("sk_int_monitor2") {
		t.Fatal("supplied token on re-run should replace the credential hash")
	}
	var role, capability, mapped string
	if err := pool.QueryRow(ctx, `SELECT role FROM memberships`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT capability FROM integrations WHERE id = $1`, res.MonitorIntegrationID).Scan(&capability); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT component_id::text FROM integration_component_mappings WHERE external_component_id = 'payments-prod'`).Scan(&mapped); err != nil {
		t.Fatal(err)
	}
	if role != "admin" || capability != "incident_input" || mapped != res.Components["payment-service"] {
		t.Fatalf("not reconciled: role=%s capability=%s mapped=%s", role, capability, mapped)
	}
}
