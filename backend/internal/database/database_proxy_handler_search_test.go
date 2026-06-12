package database

import (
	"strings"
	"testing"

	"magpie/internal/api/dto"
)

func TestProxyMatchesSearch(t *testing.T) {
	proxy := dto.ProxyInfo{
		IP:             "198.51.100.42",
		Port:           3128,
		EstimatedType:  "HTTP",
		Country:        "Germany",
		AnonymityLevel: "High",
		ResponseTime:   450,
		Alive:          true,
		Reputation: &dto.ProxyReputationSummary{
			Overall: &dto.ProxyReputation{
				Kind:  "overall",
				Score: 87.2,
				Label: "Good",
			},
			Protocols: map[string]dto.ProxyReputation{
				"http": {
					Kind:  "http",
					Score: 75.5,
					Label: "Neutral",
				},
			},
		},
	}

	testCases := map[string]bool{
		"198.51.100":                                       true,
		"198.51.100.42":                                    true,
		"198.51.100.42:3128":                               true,
		"198.51.100.42 3128":                               true,
		"http://198.51.100.42:3128":                        true,
		"https://198.51.100.42:3128":                       true,
		"user:pass@198.51.100.42:3128":                     true,
		"http://user:pass@198.51.100.42:3128":              true,
		"http://user:pass@198.51.100.42:3128/path?foo=bar": true,
		"198.51.100.42/extra":                              true,
		"3128":                                             true,
		"http":                                             true,
		"germany":                                          true,
		"high":                                             true,
		"450":                                              true,
		"alive":                                            true,
		"good":                                             true,
		"87":                                               true,
		"neutral":                                          true,
		"75.5":                                             true,
		"notfound":                                         false,
		"bad":                                              false,
	}

	for term, expected := range testCases {
		result := proxyMatchesSearch(proxy, term)
		if result != expected {
			t.Errorf("proxyMatchesSearch(%q) = %v, want %v", term, result, expected)
		}
	}
}

func TestBuildProxySearchPredicate_AvoidsCastHeavyPredicates(t *testing.T) {
	sql, args := buildProxySearchPredicate("alive")
	if sql == "" || len(args) == 0 {
		t.Fatal("expected predicate for alive search")
	}
	if strings.Contains(sql, "CAST(") || strings.Contains(sql, "ILIKE") {
		t.Fatalf("expected no cast-heavy ilike predicates, got %q", sql)
	}
	if !strings.Contains(sql, "ufi.alive") {
		t.Fatalf("expected status predicate in %q", sql)
	}
}

func TestBuildProxySearchPredicate_NumericIncludesTypedMatches(t *testing.T) {
	sql, args := buildProxySearchPredicate("3128")
	if sql == "" {
		t.Fatal("expected predicate for numeric search")
	}
	if !strings.Contains(sql, "ufi.port = ?") {
		t.Fatalf("expected typed port match in %q", sql)
	}
	if !strings.Contains(sql, "ufi.response_time") {
		t.Fatalf("expected typed response time match in %q", sql)
	}

	var hasPortArg bool
	for _, arg := range args {
		if value, ok := arg.(uint16); ok && value == 3128 {
			hasPortArg = true
			break
		}
	}
	if !hasPortArg {
		t.Fatalf("expected uint16 numeric argument in %#v", args)
	}
}
