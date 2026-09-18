package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetcherRetriesHappCompatibleUserAgentOn403(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Запрос за сведениями о подписке идёт отдельно и от имени браузера —
		// проверки ниже относятся только к загрузке списка серверов.
		if r.Header.Get("X-MS7-Info") != "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		attempts++
		if r.Header.Get("X-Hwid") == "" {
			t.Errorf("missing X-Hwid")
		}
		if r.Header.Get("X-Device-Os") != "Windows" {
			t.Errorf("unexpected device os: %q", r.Header.Get("X-Device-Os"))
		}
		// Simulate a strict Remnawave response rule that accepts only the legacy Happ UA.
		if r.Header.Get("User-Agent") != "Happ/4.3.0" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("profile-title", "MS7 Test")
		_, _ = w.Write([]byte("vless://11111111-1111-1111-1111-111111111111@example.com:443?security=tls&type=raw&sni=example.com#Test"))
	}))
	defer server.Close()

	f := &Fetcher{Client: server.Client(), DeviceID: strings.Repeat("a", 24)}
	result, err := f.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d want 2", attempts)
	}
	if result.Name != "MS7 Test" || len(result.Nodes) != 1 {
		t.Fatalf("result=%+v", result)
	}
}
