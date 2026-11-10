package xray

import "testing"

func TestRoutingStrategyFor(t *testing.T) {
	base := []any{map[string]any{"type": "field", "ip": privateRanges, "outboundTag": "direct"}}
	if got := routingStrategyFor(base); got != "AsIs" {
		t.Fatalf("only private ranges: got %s, want AsIs", got)
	}
	withGeo := append(base, map[string]any{"type": "field", "ip": []string{"geoip:ru"}, "outboundTag": "direct"})
	if got := routingStrategyFor(withGeo); got != "IPIfNonMatch" {
		t.Fatalf("geoip rule: got %s, want IPIfNonMatch", got)
	}
	DomainStrategy = "IPOnDemand"
	defer func() { DomainStrategy = "" }()
	if got := routingStrategyFor(base); got != "IPOnDemand" {
		t.Fatalf("explicit: got %s", got)
	}
}
