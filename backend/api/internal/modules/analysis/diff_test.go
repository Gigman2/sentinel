package analysis

import (
	"slices"
	"testing"
)

func TestDiffRulesCountsDuplicates(t *testing.T) {
	obs := func(id string) RuleRef { return RuleRef{Kind: "observation", RuleID: id} }
	// Two deployments made deployment_in_window fire twice; one remains.
	prev := []RuleRef{obs("trigger_alert"), obs("deployment_in_window"), obs("deployment_in_window")}
	next := []RuleRef{obs("trigger_alert"), obs("deployment_in_window"), {Kind: "hypothesis", RuleID: "deployment_in_window"}}
	c := diffRules(prev, next)
	if !slices.Equal(c.Added, []RuleRef{{Kind: "hypothesis", RuleID: "deployment_in_window"}}) {
		t.Fatalf("added %+v", c.Added)
	}
	if !slices.Equal(c.Removed, []RuleRef{obs("deployment_in_window")}) {
		t.Fatalf("removed %+v", c.Removed)
	}

	same := diffRules(prev, prev)
	if same.Added == nil || same.Removed == nil || len(same.Added)+len(same.Removed) != 0 {
		t.Fatalf("identical revisions: %+v", same)
	}
}
