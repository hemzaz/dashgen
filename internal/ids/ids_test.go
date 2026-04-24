package ids

import "testing"

func TestDashboardUIDStableAcrossReruns(t *testing.T) {
	a := DashboardUID("service", "abc123")
	b := DashboardUID("service", "abc123")
	if a != b {
		t.Fatalf("DashboardUID not stable: %q vs %q", a, b)
	}
	if len(a) != idLength {
		t.Fatalf("DashboardUID length = %d, want %d", len(a), idLength)
	}
}

func TestDashboardUIDChangesWithInputs(t *testing.T) {
	a := DashboardUID("service", "abc123")
	b := DashboardUID("service", "abc124")
	c := DashboardUID("infra", "abc123")
	if a == b {
		t.Fatalf("UID did not change when inventory hash changed")
	}
	if a == c {
		t.Fatalf("UID did not change when profile changed")
	}
}

func TestPanelUIDStableAcrossReruns(t *testing.T) {
	a := PanelUID("dash1", "traffic", "http_requests_total", "timeseries")
	b := PanelUID("dash1", "traffic", "http_requests_total", "timeseries")
	if a != b {
		t.Fatalf("PanelUID not stable: %q vs %q", a, b)
	}
	if len(a) != idLength {
		t.Fatalf("PanelUID length = %d, want %d", len(a), idLength)
	}
}

func TestPanelUIDCaseInsensitive(t *testing.T) {
	a := PanelUID("Dash1", "Traffic", "HTTP_requests_total", "TimeSeries")
	b := PanelUID("dash1", "traffic", "http_requests_total", "timeseries")
	if a != b {
		t.Fatalf("PanelUID should be case-insensitive: %q vs %q", a, b)
	}
}

func TestPanelUIDChangesWithInputs(t *testing.T) {
	base := PanelUID("dash1", "traffic", "http_requests_total", "timeseries")
	cases := []struct {
		name string
		got  string
	}{
		{"dashboard", PanelUID("dash2", "traffic", "http_requests_total", "timeseries")},
		{"section", PanelUID("dash1", "errors", "http_requests_total", "timeseries")},
		{"metric", PanelUID("dash1", "traffic", "other_metric", "timeseries")},
		{"kind", PanelUID("dash1", "traffic", "http_requests_total", "stat")},
	}
	for _, c := range cases {
		if c.got == base {
			t.Errorf("PanelUID unchanged when %s changed", c.name)
		}
	}
}
