package http

import "testing"

func TestParseExportOptionsIncludesEgressProfiles(t *testing.T) {
	t.Parallel()
	opts := parseExportOptions("http_rules,l4_rules,egress_profiles")

	if !opts.EgressProfiles {
		t.Fatalf("EgressProfiles = false, want true")
	}
}
