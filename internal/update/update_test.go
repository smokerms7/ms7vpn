package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
		{"ms7.vs1.10", "ms7.vs1.9", true}, // десятая новее девятой, а не наоборот
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

// Кнопка «Code» на GitHub даёт адрес с «.git» на конце. Раньше он превращался
// в запрос к репозиторию «ms7vpn.git», которого не существует, и проверка
// обновлений отвечала «сервер обновлений ответил 404».
func TestGitHubURLWithGitSuffix(t *testing.T) {
	cases := map[string]string{
		"https://github.com/smokerms7/ms7vpn.git":  "https://api.github.com/repos/smokerms7/ms7vpn/releases/latest",
		"https://github.com/smokerms7/ms7vpn.git/": "https://api.github.com/repos/smokerms7/ms7vpn/releases/latest",
	}
	for input, want := range cases {
		got, isGitHub := githubReleasesURL(input)
		if !isGitHub || got != want {
			t.Errorf("githubReleasesURL(%q) = %q (github=%v), ожидалось %q", input, got, isGitHub, want)
		}
	}
	// Один «.git» вместо имени репозитория адресом не является.
	if _, isGitHub := githubReleasesURL("https://github.com/smokerms7/.git"); isGitHub {
		t.Error("пустое имя репозитория не должно считаться адресом GitHub")
	}
}

// Схема номеров менять нельзя: цифра 7 из «ms7» участвует в сравнении.
func TestNewerKeepsProjectScheme(t *testing.T) {
	if !Newer("ms7.vs2.0", "ms7.vs1.2") {
		t.Error("ms7.vs2.0 должна быть новее ms7.vs1.2")
	}
	if Newer("ms7.vs1.2", "ms7.vs2.0") {
		t.Error("сравнение не должно работать в обратную сторону")
	}
	// Тег без приставки «ms7» ломает сравнение — тест фиксирует это как
	// известное поведение, чтобы такой тег не выпустили по ошибке.
	if Newer("v2.0", "ms7.vs1.2") {
		t.Error("тег v2.0 даёт [2,0] против [7,1,2] и новее не считается — " +
			"выпускать такой тег нельзя")
	}
}

func TestParseGitHubReleaseFindsPayloadAndSums(t *testing.T) {
	body := []byte(`{"tag_name":"ms7.vs2.0","assets":[
		{"name":"MS7VPN-Setup.exe","browser_download_url":"https://example.com/setup.exe"},
		{"name":"MS7VPN-Setup.exe.sha256","browser_download_url":"https://example.com/setup.exe.sha256"},
		{"name":"MS7VPN-Setup-Full.exe","browser_download_url":"https://example.com/full.exe"},
		{"name":"MS7VPN-payload.zip","browser_download_url":"https://example.com/payload.zip"},
		{"name":"MS7VPN-payload.zip.sha256","browser_download_url":"https://example.com/payload.zip.sha256"}]}`)
	manifest, err := parseGitHubRelease(body)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.URL != "https://example.com/setup.exe" {
		t.Errorf("установщик: получили %q", manifest.URL)
	}
	if manifest.SHA256URL != "https://example.com/setup.exe.sha256" {
		t.Errorf("сумма установщика: получили %q", manifest.SHA256URL)
	}
	if manifest.PayloadURL != "https://example.com/payload.zip" {
		t.Errorf("payload: получили %q", manifest.PayloadURL)
	}
	if manifest.PayloadSHA256URL != "https://example.com/payload.zip.sha256" {
		t.Errorf("сумма payload: получили %q", manifest.PayloadSHA256URL)
	}
}

// Полная офлайн-сборка не должна попадать в обновление: она весит в десять
// раз больше и ставит ровно то же самое.
func TestParseGitHubReleaseSkipsFullInstaller(t *testing.T) {
	body := []byte(`{"tag_name":"ms7.vs2.0","assets":[
		{"name":"MS7VPN-Setup-Full.exe","browser_download_url":"https://example.com/full.exe"},
		{"name":"MS7VPN-Setup.exe","browser_download_url":"https://example.com/setup.exe"}]}`)
	manifest, err := parseGitHubRelease(body)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.URL != "https://example.com/setup.exe" {
		t.Errorf("должен выбираться онлайн-установщик, получили %q", manifest.URL)
	}
}

func TestParseSHA256File(t *testing.T) {
	const sum = "752cd2c0f66a7d6148be908e297931e10846aa59fe8888b6b3ed179c5452bdcf"
	cases := []string{
		sum,
		sum + "  MS7VPN-Setup.exe\n",
		sum + " *MS7VPN-Setup.exe\n",
		"SHA256 hash of MS7VPN-Setup.exe:\n" + sum + "\n",
	}
	for _, input := range cases {
		if got := ParseSHA256File(input); got != sum {
			t.Errorf("ParseSHA256File(%q) = %q", input, got)
		}
	}
	if got := ParseSHA256File("нет тут суммы"); got != "" {
		t.Errorf("ожидалась пустая строка, получили %q", got)
	}
}

func TestDownloadChecksSum(t *testing.T) {
	payload := []byte("содержимое установщика")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	correct := sha256.Sum256(payload)
	path, err := Download(context.Background(), server.URL, hex.EncodeToString(correct[:]), "", nil)
	if err != nil {
		t.Fatalf("правильная сумма должна проходить: %v", err)
	}
	defer os.Remove(path)
	if data, _ := os.ReadFile(path); string(data) != string(payload) {
		t.Error("скачан не тот файл")
	}

	bad, err := Download(context.Background(), server.URL,
		"0000000000000000000000000000000000000000000000000000000000000000", "", nil)
	if err == nil {
		os.Remove(bad)
		t.Fatal("неверная сумма должна давать ошибку")
	}
	if bad != "" {
		if _, statErr := os.Stat(bad); statErr == nil {
			os.Remove(bad)
			t.Error("файл с неверной суммой не должен оставаться на диске")
		}
	}
}
