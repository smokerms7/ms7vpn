package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ms7vpn/internal/model"
)

func TestMoveSubscription(t *testing.T) {
	a, err := newApp(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	a.state.Subscriptions = []model.Subscription{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	move := func(id string, delta string) string {
		r := httptest.NewRequest(http.MethodPost, "/api/subscriptions/move", strings.NewReader(`{"id":"`+id+`","delta":`+delta+`}`))
		w := httptest.NewRecorder()
		a.handleMoveSubscription(w, r)
		order := ""
		for _, s := range a.state.Subscriptions {
			order += s.ID
		}
		return order
	}
	if got := move("c", "-1"); got != "acb" {
		t.Fatalf("got %s", got)
	}
	if got := move("a", "-1"); got != "acb" {
		t.Fatalf("top cannot move up: %s", got)
	}
	if got := move("a", "1"); got != "cab" {
		t.Fatalf("got %s", got)
	}
	if !transientTunStart(errString("Cannot create a file when that file already exists.")) {
		t.Fatal("collision must be transient")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
