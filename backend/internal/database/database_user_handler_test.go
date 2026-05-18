package database

import "testing"

func TestNormalizeDashboardCountry(t *testing.T) {
	tests := []struct {
		name    string
		country string
		want    string
	}{
		{name: "empty", country: "", want: "Unknown"},
		{name: "spaces", country: "   ", want: "Unknown"},
		{name: "n/a", country: "N/A", want: "Unknown"},
		{name: "unknown", country: "unknown", want: "Unknown"},
		{name: "unk", country: "UNK", want: "Unknown"},
		{name: "trim known country", country: "  Germany  ", want: "Germany"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDashboardCountry(tt.country); got != tt.want {
				t.Fatalf("normalizeDashboardCountry(%q) = %q, want %q", tt.country, got, tt.want)
			}
		})
	}
}
