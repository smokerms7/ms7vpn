package app

import (
	"fmt"
	"net"
	"testing"
)

func TestDeadLoopbackProxy(t *testing.T) {
	alive, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer alive.Close()
	alivePort := alive.Addr().(*net.TCPAddr).Port

	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadPort := dead.Addr().(*net.TCPAddr).Port
	dead.Close()

	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{fmt.Sprintf("127.0.0.1:%d", deadPort), true},
		{fmt.Sprintf("http://127.0.0.1:%d", deadPort), true},
		{fmt.Sprintf("http=127.0.0.1:%d;https=127.0.0.1:%d;socks=127.0.0.1:%d", deadPort, deadPort, deadPort), true},
		{fmt.Sprintf("localhost:%d", deadPort), true},
		{fmt.Sprintf("127.0.0.1:%d", alivePort), false},
		{fmt.Sprintf("http=127.0.0.1:%d;https=127.0.0.1:%d", deadPort, alivePort), false},
		{"proxy.corp.local:3128", false},
		{fmt.Sprintf("http=127.0.0.1:%d;https=10.0.0.1:3128", deadPort), false},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := deadLoopbackProxy(c.value); got != c.want {
			t.Errorf("deadLoopbackProxy(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}
