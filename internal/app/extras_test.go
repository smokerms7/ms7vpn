package app

import "testing"

func TestSubscriptionFromDeepLink(t *testing.T) {
	cases := map[string]string{
		"ms7vpn://add?url=https%3A%2F%2Fsub.example.com%2Fabc": "https://sub.example.com/abc",
		"ms7vpn://import/https://sub.example.com/abc":          "https://sub.example.com/abc",
		"MS7VPN://https://sub.example.com/x?y=1":               "https://sub.example.com/x?y=1",
	}
	for in, want := range cases {
		got, ok := SubscriptionFromDeepLink(in)
		if !ok || got != want {
			t.Fatalf("%s -> %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"ms7vpn://add?url=file:///c:/x", "https://sub.example.com", "ms7vpn://add?url=javascript:alert(1)", "ms7vpn://"} {
		if _, ok := SubscriptionFromDeepLink(bad); ok {
			t.Fatalf("accepted %s", bad)
		}
	}
}
