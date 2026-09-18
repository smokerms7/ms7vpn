package xray

import (
	"strings"
	"testing"
)

func TestNoHardcodedAPIPortInConfig(t *testing.T) {
	for _, mode := range []string{"tun", "system-proxy"} {
		raw, err := BuildConfig(mustNode(t), ConfigOptions{Mode: mode, HTTPPort: 10809, SOCKSPort: 10808})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "10085") {
			t.Fatalf("%s: в конфиге остался зашитый порт 10085", mode)
		}
	}
}
