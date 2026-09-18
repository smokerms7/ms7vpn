package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ms7vpn/internal/model"
)

// Панели Remnawave отдают подробности подписки отдельным адресом «<ссылка>/info»
// в формате JSON. В самом ответе со списком серверов есть только заголовок
// subscription-userinfo, и там обычно лишь израсходованный трафик.
//
// Запрос необязательный: если панель его не поддерживает или не ответила,
// используется то, что пришло в заголовке, и ничего не додумывается.

type infoResponse struct {
	IsFound bool `json:"isFound"`
	User    struct {
		Username             string `json:"username"`
		UserStatus           string `json:"userStatus"`
		Status               string `json:"status"`
		IsActive             *bool  `json:"isActive"`
		ExpiresAt            string `json:"expiresAt"`
		ExpireAt             string `json:"expireAt"`
		DaysLeft             *int   `json:"daysLeft"`
		TrafficUsedBytes     any    `json:"trafficUsedBytes"`
		TrafficLimitBytes    any    `json:"trafficLimitBytes"`
		UsedTrafficBytes     any    `json:"usedTrafficBytes"`
		TrafficLimit         any    `json:"trafficLimit"`
		LifetimeUsedTraffic  any    `json:"lifetimeUsedTrafficBytes"`
	} `json:"user"`
}

// FetchInfo дополняет сведения о подписке данными панели.
//
// Панель выбирает ответ по User-Agent: браузеру она отдаёт страницу с полной
// информацией о подписке, а клиенту вроде Happ — только список серверов и
// заголовок с израсходованным трафиком. Поэтому за сроком, лимитом и статусом
// обращаемся так же, как это делает её собственная страница.
func (f *Fetcher) FetchInfo(ctx context.Context, subscriptionURL string, base model.UserInfo) model.UserInfo {
	endpoint := strings.TrimRight(strings.TrimSpace(subscriptionURL), "/")
	if endpoint == "" || !strings.HasPrefix(endpoint, "http") {
		return base
	}
	for _, candidate := range []string{endpoint + "/info", endpoint} {
		if parsed, ok := f.requestInfo(ctx, candidate); ok {
			return mergeInfo(base, parsed)
		}
	}
	return base
}

func (f *Fetcher) requestInfo(ctx context.Context, endpoint string) (infoResponse, bool) {
	var parsed infoResponse
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return parsed, false
	}
	request.Header.Set("Accept", "application/json")
	// Представляемся браузером: именно так панель отдаёт полные данные.
	request.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	request.Header.Set("X-Hwid", f.DeviceID)
	request.Header.Set("X-MS7-Info", "1")

	response, err := f.Client.Do(request)
	if err != nil {
		return parsed, false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return parsed, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return parsed, false
	}
	if json.Unmarshal(body, &parsed) != nil {
		return parsed, false
	}
	// Пустой разбор означает, что пришло не то: HTML-страница или список ссылок.
	if parsed.User.Username == "" && parsed.User.UserStatus == "" && parsed.User.Status == "" &&
		parsed.User.ExpiresAt == "" && parsed.User.ExpireAt == "" &&
		parsed.User.TrafficUsedBytes == nil && parsed.User.TrafficLimitBytes == nil &&
		parsed.User.UsedTrafficBytes == nil && parsed.User.TrafficLimit == nil {
		return parsed, false
	}
	return parsed, true
}

func mergeInfo(base model.UserInfo, parsed infoResponse) model.UserInfo {
	result := base
	if name := strings.TrimSpace(parsed.User.Username); name != "" {
		result.Username = name
	}
	if status := firstNonEmpty(parsed.User.UserStatus, parsed.User.Status); status != "" {
		result.Status = strings.ToUpper(status)
	} else if parsed.User.IsActive != nil {
		if *parsed.User.IsActive {
			result.Status = "ACTIVE"
		} else {
			result.Status = "DISABLED"
		}
	}
	if expire := parseTimestamp(firstNonEmpty(parsed.User.ExpiresAt, parsed.User.ExpireAt)); expire > 0 {
		result.ExpireUnix = expire
	}
	if used := asBytes(firstNonNil(parsed.User.TrafficUsedBytes, parsed.User.UsedTrafficBytes, parsed.User.LifetimeUsedTraffic)); used > 0 {
		// Панель отдаёт суммарный расход; раскладывать его на приём и отдачу
		// она не умеет, поэтому кладём в «загрузку».
		result.DownloadBytes = used
		result.UploadBytes = 0
	}
	if limit := asBytes(firstNonNil(parsed.User.TrafficLimitBytes, parsed.User.TrafficLimit)); limit > 0 {
		result.TotalBytes = limit
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// asBytes принимает и число, и строку: панели отдают лимит по-разному.
func asBytes(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || strings.EqualFold(text, "unlimited") || text == "∞" {
			return 0
		}
		var number int64
		if _, err := fmt.Sscanf(text, "%d", &number); err == nil {
			return number
		}
	}
	return 0
}

func parseTimestamp(value string) int64 {
	if value == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}
