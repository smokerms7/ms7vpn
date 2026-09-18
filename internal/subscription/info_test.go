package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"ms7vpn/internal/model"
)

// Панель отдаёт полные сведения о подписке по адресу «<ссылка>/info».
func TestFetchInfoFillsRealValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub/key/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"isFound":true,"user":{"username":"arbi","userStatus":"ACTIVE",
			"expiresAt":"2026-09-20T12:00:00.000Z","trafficUsedBytes":13736739648,"trafficLimitBytes":107374182400}}`)
	}))
	defer server.Close()

	fetcher := NewFetcher(t.TempDir())
	got := fetcher.FetchInfo(context.Background(), server.URL+"/sub/key", model.UserInfo{DownloadBytes: 1})
	if got.Status != "ACTIVE" || got.Username != "arbi" {
		t.Fatalf("статус/имя: %+v", got)
	}
	if got.TotalBytes != 107374182400 || got.DownloadBytes != 13736739648 {
		t.Fatalf("трафик: %+v", got)
	}
	if got.ExpireUnix == 0 {
		t.Fatal("срок действия не разобран")
	}
}

// Если панель такого адреса не знает, данные из заголовка остаются нетронутыми
// и ничего не выдумывается.
func TestFetchInfoKeepsHeaderDataWhenUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	base := model.UserInfo{DownloadBytes: 13736739648}
	got := NewFetcher(t.TempDir()).FetchInfo(context.Background(), server.URL+"/sub/key", base)
	if got != base {
		t.Fatalf("данные заголовка изменены: %+v", got)
	}
}

// Если адреса «/info» нет, панель обычно отдаёт те же данные по самой ссылке
// при запросе от браузера.
func TestFetchInfoFallsBackToSubscriptionURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sub/key/info" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"user":{"userStatus":"LIMITED","trafficUsedBytes":"5368709120"}}`)
	}))
	defer server.Close()

	got := NewFetcher(t.TempDir()).FetchInfo(context.Background(), server.URL+"/sub/key", model.UserInfo{})
	if got.Status != "LIMITED" || got.DownloadBytes != 5368709120 {
		t.Fatalf("данные по запасному пути не получены: %+v", got)
	}
}
