package subscription

import "testing"

const sample = `[
 {"remarks":"🇳🇱 Нидерланды 25gb/s","outbounds":[
   {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"uuid-1","flow":"xtls-rprx-vision","encryption":"none"}]}]},
    "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"www.google.com","publicKey":"PBK","shortId":"ab12","fingerprint":"chrome"}}},
   {"tag":"direct","protocol":"freedom"}]},
 {"remarks":"🇩🇪 Германия 10gb/s","outbounds":[
   {"protocol":"vless","settings":{"vnext":[{"address":"5.6.7.8","port":443,"users":[{"id":"uuid-2","encryption":"none"}]}]},
    "streamSettings":{"network":"xhttp","security":"reality","xhttpSettings":{"path":"/xh","host":"cdn.example.com","mode":"auto"},"realitySettings":{"serverName":"sni.example.com","publicKey":"PBK2","shortId":"cd34","fingerprint":"firefox"}}}]}
]`

func TestXrayJSONSubscription(t *testing.T) {
	nodes, _, err := ParseContent([]byte(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("ожидали 2 сервера, получили %d", len(nodes))
	}
	if nodes[0].Name != "🇳🇱 Нидерланды 25gb/s" || nodes[0].Address != "1.2.3.4" || nodes[0].Port != 443 {
		t.Fatalf("первая нода разобрана неверно: %+v", nodes[0])
	}
	if nodes[0].Security != "reality" || nodes[0].Transport != "raw" || !nodes[0].Supported {
		t.Fatalf("параметры первой ноды: %+v", nodes[0])
	}
	if nodes[1].Transport != "xhttp" || nodes[1].SNI != "sni.example.com" {
		t.Fatalf("вторая нода: %+v", nodes[1])
	}
}

// Обход через Яндекс CDN работает только с настройками из extra: без них
// отправка идёт POST-ом, и CDN отвечает 413.
func TestXrayJSONKeepsXHTTPExtra(t *testing.T) {
	text := `[{"remarks":"BS","outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"188.72.103.4","port":443,"users":[{"id":"11111111-2222-3333-4444-555555555555","encryption":"none"}]}]},"streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"serverName":"ioo.example"},"xhttpSettings":{"mode":"packet-up","host":"ioo.example","path":"/api/uploadFile/","noSSEHeader":true,"extra":{"uplinkHTTPMethod":"GET","uplinkDataPlacement":"header","uplinkDataKey":"X-Payload","scMaxEachPostBytes":16000}}}}]}]`
	nodes, _, err := ParseContent([]byte(text))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parse: %v %d", err, len(nodes))
	}
	_, parsed, err := ParseURI(nodes[0].RawURI)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ExtraJSON["uplinkHTTPMethod"] != "GET" || parsed.ExtraJSON["uplinkDataPlacement"] != "header" ||
		parsed.ExtraJSON["uplinkDataKey"] != "X-Payload" || parsed.ExtraJSON["noSSEHeader"] != true {
		t.Fatalf("extra lost: %#v", parsed.ExtraJSON)
	}
}
