// Package update проверяет, есть ли свежая версия MS7VPN, и умеет её скачать.
//
// Приложение спрашивает у сервера обновлений описание последней сборки и
// сравнивает её номер с собственным. Скачивание начинается только после того,
// как человек нажал кнопку: само по себе ничего не качается.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PayloadAssetName — вложение выпуска, которое качает онлайн-установщик.
const PayloadAssetName = "MS7VPN-payload.zip"

// Manifest — файл, который лежит на сервере обновлений.
//
//	{
//	  "version": "ms7.vs2.0",
//	  "url": "https://example.com/MS7VPN-Setup.exe",
//	  "sha256": "…",
//	  "payloadUrl": "https://example.com/MS7VPN-payload.zip",
//	  "payloadSha256": "…",
//	  "notes": "Что нового"
//	}
type Manifest struct {
	Version       string `json:"version"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	PayloadURL    string `json:"payloadUrl"`
	PayloadSHA256 string `json:"payloadSha256"`
	Notes         string `json:"notes"`

	// У GitHub контрольные суммы лежат отдельными вложениями, а не внутри
	// ответа. Тогда заполняются эти поля, а сама сумма подтягивается
	// вторым запросом — только когда она действительно понадобится.
	SHA256URL        string `json:"sha256Url"`
	PayloadSHA256URL string `json:"payloadSha256Url"`
}

// resolveSum возвращает готовую сумму: либо ту, что уже есть, либо скачанную
// по ссылке. Отсутствие суммы не ошибка — проверка тогда просто не делается.
func resolveSum(ctx context.Context, sum, sumURL string) string {
	if sum = strings.TrimSpace(sum); sum != "" {
		return sum
	}
	if sumURL = strings.TrimSpace(sumURL); sumURL == "" {
		return ""
	}
	fetched, err := FetchSHA256(ctx, sumURL)
	if err != nil {
		return ""
	}
	return fetched
}

// Result — что показать человеку.
type Result struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	HasUpdate   bool   `json:"hasUpdate"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Notes       string `json:"notes,omitempty"`
	CheckedAt   int64  `json:"checkedAt"`
}

// Payload — архив с файлами программы для онлайн-установщика.
type Payload struct {
	Version string
	URL     string
	SHA256  string
}

// Check запрашивает сведения о последней сборке и сравнивает версии.
//
// Поддерживаются два источника:
//   - GitHub: ссылка вида https://github.com/владелец/репозиторий — берётся
//     последний выпуск, файл установщика из его вложений;
//   - свой сервер: ссылка на JSON-файл вида Manifest.
func Check(ctx context.Context, source, currentVersion string) (Result, error) {
	result := Result{Current: currentVersion, CheckedAt: time.Now().Unix()}
	manifest, err := fetchManifest(ctx, source, currentVersion)
	if err != nil {
		return result, err
	}

	result.Latest = strings.TrimSpace(manifest.Version)
	result.DownloadURL = strings.TrimSpace(manifest.URL)
	result.Notes = strings.TrimSpace(manifest.Notes)
	result.HasUpdate = Newer(result.Latest, currentVersion)
	// За суммой ходим только когда обновление есть: иначе каждая проверка
	// делала бы лишний запрос ради файла, который никому не нужен.
	if result.HasUpdate {
		result.SHA256 = resolveSum(ctx, manifest.SHA256, manifest.SHA256URL)
	}
	return result, nil
}

// LatestPayload находит архив с файлами программы для онлайн-установщика.
func LatestPayload(ctx context.Context, source, currentVersion string) (Payload, error) {
	manifest, err := fetchManifest(ctx, source, currentVersion)
	if err != nil {
		return Payload{}, err
	}
	url := strings.TrimSpace(manifest.PayloadURL)
	if url == "" {
		return Payload{}, fmt.Errorf("в выпуске %s нет вложения %s",
			strings.TrimSpace(manifest.Version), PayloadAssetName)
	}
	return Payload{
		Version: strings.TrimSpace(manifest.Version),
		URL:     url,
		SHA256:  resolveSum(ctx, manifest.PayloadSHA256, manifest.PayloadSHA256URL),
	}, nil
}

func fetchManifest(ctx context.Context, source, currentVersion string) (Manifest, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return Manifest{}, fmt.Errorf("адрес проверки обновлений не задан")
	}
	if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
		return Manifest{}, fmt.Errorf("адрес проверки обновлений должен начинаться с https://")
	}

	requestURL, isGitHub := githubReleasesURL(source)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return Manifest{}, err
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
		return Manifest{}, fmt.Errorf("сервер обновлений недоступен: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("сервер обновлений ответил %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest
	if isGitHub {
		manifest, err = parseGitHubRelease(body)
		if err != nil {
			return Manifest{}, err
		}
	} else if json.Unmarshal(body, &manifest) != nil {
		return Manifest{}, fmt.Errorf("не удалось разобрать ответ сервера обновлений")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return Manifest{}, fmt.Errorf("в ответе сервера нет номера версии")
	}
	return manifest, nil
}

// Progress вызывается по мере скачивания. total равен нулю, если сервер не
// сообщил размер файла.
type Progress func(done, total int64)

// maxDownloadBytes ограничивает размер скачиваемого файла. Установщик весит
// десятки мегабайт; сотня с запасом закрывает любые будущие сборки и при этом
// не даёт подсунуть гигабайтный файл.
const maxDownloadBytes = 256 << 20

// Download скачивает файл во временную папку и сверяет контрольную сумму.
//
// Возвращает путь к скачанному файлу. При несовпадении суммы файл удаляется:
// запускать не проверенный установщик нельзя.
func Download(ctx context.Context, url, expectedSHA256, namePattern string, progress Progress) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("адрес файла не задан")
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", fmt.Errorf("адрес файла должен начинаться с https://")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "MS7VPN downloader")
	request.Header.Set("Accept", "application/octet-stream")

	// Таймаут на всё скачивание не ставим: на медленном канале двадцать
	// мегабайт легко идут дольше любого разумного срока. Отмену даёт ctx.
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("не удалось скачать: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("сервер ответил %s", response.Status)
	}

	if namePattern == "" {
		namePattern = "ms7vpn-download-*"
	}
	file, err := os.CreateTemp("", namePattern)
	if err != nil {
		return "", err
	}
	path := file.Name()

	var digest hash.Hash
	var writer io.Writer = file
	if expectedSHA256 != "" {
		digest = sha256.New()
		writer = io.MultiWriter(file, digest)
	}

	written, copyErr := copyWithProgress(ctx, writer,
		io.LimitReader(response.Body, maxDownloadBytes+1), response.ContentLength, progress)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("не удалось скачать: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if written > maxDownloadBytes {
		_ = os.Remove(path)
		return "", fmt.Errorf("файл слишком большой")
	}
	if size := response.ContentLength; size > 0 && written != size {
		_ = os.Remove(path)
		return "", fmt.Errorf("файл скачан не полностью: %d из %d байт", written, size)
	}
	if digest != nil {
		actual := hex.EncodeToString(digest.Sum(nil))
		if !strings.EqualFold(actual, expectedSHA256) {
			_ = os.Remove(path)
			return "", fmt.Errorf("контрольная сумма не совпала — файл повреждён или подменён")
		}
	}
	return path, nil
}

func copyWithProgress(ctx context.Context, destination io.Writer, source io.Reader, total int64, progress Progress) (int64, error) {
	buffer := make([]byte, 256<<10)
	var written int64
	var lastReport time.Time
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			if _, writeErr := destination.Write(buffer[:n]); writeErr != nil {
				return written, writeErr
			}
			written += int64(n)
			// Сообщаем не чаще пяти раз в секунду: иначе прогресс заливает
			// интерфейс сообщениями и тормозит саму закачку.
			if progress != nil && time.Since(lastReport) > 200*time.Millisecond {
				progress(written, total)
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return written, readErr
		}
	}
	if progress != nil {
		progress(written, total)
	}
	return written, nil
}

// ParseSHA256File достаёт сумму из файла вида «хеш *имя» или просто «хеш».
// Такие файлы делают и sha256sum, и certutil, и наш скрипт сборки.
func ParseSHA256File(content string) string {
	for _, line := range strings.Split(content, "\n") {
		for _, field := range strings.Fields(line) {
			candidate := strings.TrimPrefix(field, "*")
			if len(candidate) != 64 {
				continue
			}
			if _, err := hex.DecodeString(candidate); err == nil {
				return strings.ToLower(candidate)
			}
		}
	}
	return ""
}

// FetchSHA256 скачивает небольшой файл с контрольной суммой.
func FetchSHA256(ctx context.Context, url string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(url), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "MS7VPN downloader")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("сервер ответил %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if err != nil {
		return "", err
	}
	sum := ParseSHA256File(string(body))
	if sum == "" {
		return "", fmt.Errorf("в файле нет контрольной суммы")
	}
	return sum, nil
}

// FileSHA256 считает сумму уже лежащего на диске файла.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

var numbersRE = regexp.MustCompile(`\d+`)

// Newer сообщает, новее ли версия a, чем b.
//
// Схемы номеров у проекта разные: «ms7.vs1.2», «1.1.4», «0.4.0-alpha.1».
// Сравниваем по числам в порядке появления — этого достаточно для любой
// из них и не ломается при смене оформления номера.
//
// Отсюда важное следствие для тегов выпусков: цифра 7 в «ms7» тоже идёт в
// сравнение. Тег вида «v2.0» даст [2,0] против [7,1,2] у «ms7.vs1.2», и
// обновление не найдётся. Схему номеров менять нельзя.
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
	if len(parts) < 2 || parts[0] == "" {
		return source, false
	}
	// Кнопка «Code» на GitHub даёт адрес с «.git» на конце. Без этого
	// отсечения запрос уходил к репозиторию «ms7vpn.git», которого нет,
	// и проверка обновлений отвечала «сервер обновлений ответил 404».
	repository := strings.TrimSuffix(parts[1], ".git")
	if repository == "" {
		return source, false
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", parts[0], repository), true
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

// parseGitHubRelease достаёт из выпуска номер версии и ссылки на файлы.
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

	sums := map[string]string{}
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		switch {
		case name == strings.ToLower(PayloadAssetName):
			manifest.PayloadURL = asset.BrowserDownloadURL
		case strings.HasSuffix(name, ".sha256"):
			sums[strings.TrimSuffix(name, ".sha256")] = asset.BrowserDownloadURL
		}
	}

	// Установщик: сначала полноценный «setup», иначе любой exe.
	// Полная офлайн-сборка (…-Full.exe) в обновлении не нужна: она весит
	// в десять раз больше и ставит ровно то же самое.
	installerName := ""
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if !strings.HasSuffix(name, ".exe") || strings.Contains(name, "full") {
			continue
		}
		if strings.Contains(name, "setup") {
			manifest.URL = asset.BrowserDownloadURL
			installerName = name
			break
		}
	}
	if manifest.URL == "" {
		for _, asset := range release.Assets {
			name := strings.ToLower(asset.Name)
			if strings.HasSuffix(name, ".exe") {
				manifest.URL = asset.BrowserDownloadURL
				installerName = name
				break
			}
		}
	}

	// Контрольные суммы лежат отдельными вложениями — сохраняем ссылки,
	// сами суммы скачиваются позже и только при необходимости.
	if installerName != "" {
		manifest.SHA256URL = sums[installerName]
	}
	manifest.PayloadSHA256URL = sums[strings.ToLower(PayloadAssetName)]
	return manifest, nil
}

// TempName подбирает имя для скачанного файла, сохраняя расширение.
func TempName(url string) string {
	base := filepath.Base(url)
	extension := filepath.Ext(base)
	if extension == "" {
		extension = ".tmp"
	}
	return "ms7vpn-*" + extension
}
