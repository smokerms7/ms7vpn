package splitroute

import (
	"reflect"
	"testing"
)

func TestCleanDomains(t *testing.T) {
	got := CleanDomains([]string{
		"https://www.Kino.ru/films?x=1",
		"kino.ru",
		"*.example.com",
		"site.com:8443",
		"президент.рф",
		"192.168.1.1",
		"localhost",
		"not a domain",
		"",
		"sub.domain.co.uk.",
	}, 100)
	want := []string{"kino.ru", "example.com", "site.com", "xn--d1abbgf6aiiy.xn--p1ai", "sub.domain.co.uk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CleanDomains = %#v, want %#v", got, want)
	}
	if n := len(CleanDomains([]string{"a.ru", "b.ru", "c.ru"}, 2)); n != 2 {
		t.Fatalf("limit not applied: %d", n)
	}
}

func TestCleanApps(t *testing.T) {
	got := CleanApps([]string{`C:\Program Files\Telegram Desktop\Telegram.exe`, "chrome", "CHROME.EXE", `"steam.exe"`, "  ", "bad|name"}, 100)
	want := []string{"telegram.exe", "chrome.exe", "steam.exe"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CleanApps = %#v, want %#v", got, want)
	}
}

func TestCleanRulesKinds(t *testing.T) {
	got := CleanRules([]string{"GeoSite:Netflix", "geoip:ru", "keyword:torrent", "10.1.2.3", "192.168.0.0/16", "2001:db8::1", "https://Kino.ru/x", "geosite:bad value", "# comment"}, 100)
	want := []string{"geosite:netflix", "geoip:ru", "keyword:torrent", "10.1.2.3/32", "192.168.0.0/16", "2001:db8::1/128", "kino.ru"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CleanRules = %#v, want %#v", got, want)
	}
	d, k, c, s, i := Split(got)
	if len(d) != 1 || len(k) != 1 || len(c) != 3 || len(s) != 1 || len(i) != 1 {
		t.Fatalf("split: %v %v %v %v %v", d, k, c, s, i)
	}
}
