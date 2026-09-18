// Package update проверяет, есть ли свежая версия MS7VPN.
//
// Приложение спрашивает у сервера небольшой файл с описанием последней
// сборки и сравнивает её номер с собственным. Ничего не скачивается и не
// устанавливается само: решение всегда за человеком.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Manifest — файл, который лежит на сервере обновлений.
//
//	{
//	  "version": "ms7.vs1.3",
//	  "url": "https://example.com/MS7VPN-Setup.exe",
//	  "sha256": "…",
//	  "notes": "Что нового"
//	}
type Manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes"`
}

// Result — что показать человеку.
type Result struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	HasUpdate   bool   `json:"hasUpdate"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	Notes       string `json:"notes,omitempty"`
	CheckedAt   int64  `json:"checkedAt"`
}

// Check запрашивает сведения о последней сборке и сравнивает версии.
//
// Поддерживаются два источника:
//   - GitHub: ссылка вида https://github.com/владелец/репозиторий — берётся
//     последний выпуск, файл установщика из его вложений;
//   - свой сервер: ссылка на JSON-файл вида Manifest.
func Check(ctx context.Context, source, currentVersion string) (Result, error) {
	result := Result{Current: currentVersion, CheckedAt: time.Now().Unix()}
	source = strings.TrimSpace(source)
	if source == "" {
		return result, fmt.Errorf("адрес проверки обновлений не задан")
	}
	if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
		return result, fmt.Errorf("адрес проверки обновлений должен начинаться с https://")
	}

	requestURL, isGitHub := githubReleasesURL(source)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MS7VPN/"+currentVersion+" update-check")
	request.Header.Set("Cache-Control", "no-cache")
	if isGitHub {
		// GitHub требует явного указания версии своего API.
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return result, fmt.Errorf("сервер обновлений недоступен: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("сервер обновлений ответил %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return result, err
	}

	var manifest Manifest
	if isGitHub {
		manifest, err = parseGitHubRelease(body)
		if err != nil {
			return result, err
		}
	} else if json.Unmarshal(body, &manifest) != nil {
		return result, fmt.Errorf("не удалось разобрать ответ сервера обновлений")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return result, fmt.Errorf("в ответе сервера нет номера версии")
	}

	result.Latest = strings.TrimSpace(manifest.Version)
	result.DownloadURL = strings.TrimSpace(manifest.URL)
	result.Notes = strings.TrimSpace(manifest.Notes)
	result.HasUpdate = Newer(result.Latest, currentVersion)
	return result, nil
}

var numbersRE = regexp.MustCompile(`\d+`)

// Newer сообщает, новее ли версия a, чем b.
//
// Схемы номеров у проекта разные: «ms7.vs1.2», «1.1.4», «0.4.0-alpha.1».
// Сравниваем по числам в порядке появления — этого достаточно для любой
// из них и не ломается при смене оформления номера.
func Newer(a, b string) bool {
	left, right := versionNumbers(a), versionNumbers(b)
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	for i := 0; i < length; i++ {
		var x, y int
		if i < len(left) {
			x = left[i]
		}
		if i < len(right) {
			y = right[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func versionNumbers(value string) []int {
	parts := numbersRE.FindAllString(value, -1)
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		numbers = append(numbers, number)
	}
	return numbers
}


// githubReleasesURL превращает ссылку на репозиторий в адрес его последнего
// выпуска. Для остальных адресов возвращает их без изменений.
func githubReleasesURL(source string) (string, bool) {
	if strings.HasPrefix(source, "https://api.github.com/repos/") {
		return source, true
	}
	rest, ok := strings.CutPrefix(source, "https://github.com/")
	if !ok {
		return source, false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return source, false
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", parts[0], parts[1]), true
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// parseGitHubRelease достаёт из выпуска номер версии и ссылку на установщик.
func parseGitHubRelease(body []byte) (Manifest, error) {
	var release githubRelease
	if json.Unmarshal(body, &release) != nil {
		return Manifest{}, fmt.Errorf("не удалось разобрать ответ GitHub")
	}
	version := strings.TrimSpace(release.TagName)
	if version == "" {
		version = strings.TrimSpace(release.Name)
	}
	if version == "" {
		return Manifest{}, fmt.Errorf("в последнем выпуске GitHub нет номера версии")
	}

	manifest := Manifest{Version: version, Notes: strings.TrimSpace(release.Body)}
	// Берём установщик; если его нет, годится любой exe из вложений.
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.HasSuffix(name, ".exe") && strings.Contains(name, "setup") {
			manifest.URL = asset.BrowserDownloadURL
			break
		}
	}
	if manifest.URL == "" {
		for _, asset := range release.Assets {
			if strings.HasSuffix(strings.ToLower(asset.Name), ".exe") {
				manifest.URL = asset.BrowserDownloadURL
				break
			}
		}
	}
	return manifest, nil
}
