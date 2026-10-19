package cli

import "testing"

func TestTeamMeshReleaseVersions(t *testing.T) {
	for _, version := range []string{"0.6.1-overlay.4", "v0.6.1-overlay.15", "0.7.0-overlay.1"} {
		if !IsReleaseVersion(version) || !IsForkReleaseVersion(version) {
			t.Fatalf("rejected release %q", version)
		}
	}
	for _, version := range []string{"0.6.1-overlay.dev.1234567", "0.6.1-overlay.abcdef1", "0.6.1-overlay.4-dirty", "0.6.1-overlay.4+local", "0.6.1-overlay.-1", "0.6.1-overlay.01", "0.6.1-overlay.999999999999999999999999", "0.6.1-overlay."} {
		if IsReleaseVersion(version) {
			t.Fatalf("accepted development/invalid build %q", version)
		}
	}
	for _, test := range []struct {
		latest, current string
		newer           bool
	}{
		{"0.6.1-overlay.10", "0.6.1-overlay.9", true},
		{"0.6.2-overlay.1", "0.6.1-overlay.100", true},
		{"0.6.1-overlay.4", "v0.6.1-overlay.4", false},
		{"0.6.1-overlay.3", "0.6.1-overlay.4", false},
		{"0.6.1-overlay.5", "0.6.1-overlay.dev.1234567", false},
		{"0.6.2", "0.6.1-overlay.4", false},
		{"0.6.2-overlay.1", "0.6.1", false},
	} {
		if got := IsNewerVersion(test.latest, test.current); got != test.newer {
			t.Errorf("%s vs %s = %t", test.latest, test.current, got)
		}
	}
}
