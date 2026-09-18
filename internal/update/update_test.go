package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerHandlesProjectVersionSchemes(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"ms7.vs1.2", "ms7.vs1.1", true},
		{"ms7.vs1.1", "ms7.vs1.2", false},
		{"ms7.vs1.2", "ms7.vs1.2", false},
		{"ms7.vs2.0", "ms7.vs1.9", true},
		{"ms7.vs1.10", "ms7.vs1.9", true},   // десятая новее девятой, а не наоборот
		{"1.1.4", "1.1.3", true},
		{"1.2.0", "1.1.9", true},
		{"ms7.vs1.3", "0.4.0-alpha.1", true}, // переход со старой схемы номеров
	}
	for _, tc := range cases {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, ожидалось %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCheckReportsUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"version":"ms7.vs1.5","url":"https://example.com/setup.exe","notes":"Быстрее"}`)
	}))
	defer server.Close()

	result, err := Check(context.Background(), server.URL, "ms7.vs1.2")
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasUpdate || result.Latest != "ms7.vs1.5" || result.DownloadURL == "" {
		t.Fatalf("%+v", result)
	}
}

func TestCheckReportsUpToDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"version":"ms7.vs1.2"}`)
	}))
	defer server.Close()

	result, err := Check(context.Background(), server.URL, "ms7.vs1.2")
	if err != nil {
		t.Fatal(err)
	}
	if result.HasUpdate {
		t.Fatalf("обновление не должно предлагаться: %+v", result)
	}
}

func TestCheckFailsClearly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := Check(context.Background(), server.URL, "ms7.vs1.2"); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if _, err := Check(context.Background(), "", "ms7.vs1.2"); err == nil {
		t.Fatal("пустой адрес должен давать ошибку")
	}
}

func TestGitHubReleasesURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/arbi/ms7vpn":          "https://api.github.com/repos/arbi/ms7vpn/releases/latest",
		"https://github.com/arbi/ms7vpn/":         "https://api.github.com/repos/arbi/ms7vpn/releases/latest",
		"https://github.com/arbi/ms7vpn/releases": "https://api.github.com/repos/arbi/ms7vpn/releases/latest",
	}
	for input, want := range cases {
		got, isGitHub := githubReleasesURL(input)
		if !isGitHub || got != want {
			t.Errorf("githubReleasesURL(%q) = %q (github=%v), ожидалось %q", input, got, isGitHub, want)
		}
	}
	if _, isGitHub := githubReleasesURL("https://ms7pc.shop/app/latest.json"); isGitHub {
		t.Error("обычный адрес не должен считаться GitHub")
	}
}

func TestParseGitHubReleasePicksSetup(t *testing.T) {
	body := []byte(`{"tag_name":"ms7.vs1.5","body":"Заметки","assets":[
		{"name":"MS7VPN.exe","browser_download_url":"https://example.com/app.exe"},
		{"name":"MS7VPN-Setup.exe","browser_download_url":"https://example.com/setup.exe"}]}`)
	manifest, err := parseGitHubRelease(body)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "ms7.vs1.5" {
		t.Fatalf("версия: %q", manifest.Version)
	}
	if manifest.URL != "https://example.com/setup.exe" {
		t.Fatalf("должен выбираться установщик, получено %q", manifest.URL)
	}
	if manifest.Notes != "Заметки" {
		t.Fatalf("заметки: %q", manifest.Notes)
	}
}

func TestParseGitHubReleaseFallsBackToAnyExe(t *testing.T) {
	body := []byte(`{"tag_name":"v2.0","assets":[
		{"name":"MS7VPN.exe","browser_download_url":"https://example.com/app.exe"}]}`)
	manifest, err := parseGitHubRelease(body)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.URL != "https://example.com/app.exe" {
		t.Fatalf("запасной выбор не сработал: %q", manifest.URL)
	}
}
