package recipes

import (
	"testing"

	"dashgen/internal/profiles"
)

// TestEveryRecipeSectionIsReachable asserts that every recipe registered in
// each profile's registry declares a Section() value that appears in
// profiles.Sections for that profile.
//
// This is a regression guard for issues like #2, where k8s_scheduler_latency
// and k8s_coredns were introduced with Section()="latency" — a section that
// does not exist in the k8s profile's canonical section list
// {overview, pods, workloads, resources}. The synth loop iterates profile
// sections in canonical order and silently skips any recipe whose Section()
// doesn't match, causing it to ship as dead code.
func TestEveryRecipeSectionIsReachable(t *testing.T) {
	cases := []struct {
		profile profiles.Profile
		reg     *Registry
	}{
		{profiles.ProfileService, NewServiceRegistry()},
		{profiles.ProfileInfra, NewInfraRegistry()},
		{profiles.ProfileK8s, NewK8sRegistry()},
	}
	for _, tc := range cases {
		valid := map[string]bool{}
		for _, s := range profiles.Sections(tc.profile) {
			valid[s] = true
		}
		for _, r := range tc.reg.All() {
			if !valid[r.Section()] {
				t.Errorf("recipe %s in %v profile declares Section()=%q which is not in profiles.Sections=%v",
					r.Name(), tc.profile, r.Section(), profiles.Sections(tc.profile))
			}
		}
	}
}
