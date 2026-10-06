package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Здесь не проверка «функция возвращает ошибку», а попытка провести атаку
// целиком: злоумышленник держит сервер обновлений и пытается добиться, чтобы
// программа скачала и запустила его файл.

// Атака 1: сервер отдаёт манифест без контрольной суммы.
//
// Так дыра и работала: сумма не обязательна -> сверка пропускается ->
// RunInstaller запускает что прислали.
func TestАтакаВыпускБезСуммы(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"version":"ms7.vs9.9","url":"https://evil.tld/troyan.exe"}`)
	}))
	defer evil.Close()

	result, err := Check(context.Background(), evil.URL, "ms7.vs2.2")
	if err == nil {
		t.Fatalf("АТАКА ПРОШЛА: обновление принято без суммы, ссылка %q", result.DownloadURL)
	}
	t.Logf("отбито: %v", err)
}

// Атака 2: сумма есть, но файл подменён на другой.
func TestАтакаПодменаФайлаПриВернойСумме(t *testing.T) {
	честный := []byte("настоящий установщик")
	сумма := sha256.Sum256(честный)

	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Отдаём не то, на что указывает сумма.
		fmt.Fprint(w, "ЗЛОВРЕДНЫЙ установщик")
	}))
	defer evil.Close()

	path, err := Download(context.Background(), evil.URL, hex.EncodeToString(сумма[:]), "ms7vpn-test-*", nil)
	if err == nil {
		os.Remove(path)
		t.Fatal("АТАКА ПРОШЛА: подменённый файл принят")
	}
	if _, statErr := os.Stat(path); path != "" && statErr == nil {
		t.Fatal("подменённый файл остался на диске")
	}
	t.Logf("отбито: %v", err)
}

// Атака 3: обновление по открытому каналу, где файл подменяется на лету.
//
// Проверяем не сам факт ошибки, а её причину: несуществующий узел даёт ошибку
// и без всякой защиты, сетевую. Нужно, чтобы адрес отвергался по схеме, то
// есть ещё до обращения к сети.
func TestАтакаОткрытыйКанал(t *testing.T) {
	отвергнутПоСхеме := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "https://")
	}

	for _, адрес := range []string{
		"http://evil.tld/latest.json",
		"http://github.com/smokerms7/ms7vpn",
	} {
		_, err := Check(context.Background(), адрес, "ms7.vs2.2")
		if !отвергнутПоСхеме(err) {
			t.Fatalf("АТАКА ПРОШЛА: открытый канал %q не отвергнут по схеме (ошибка: %v)", адрес, err)
		}
	}

	_, err := Download(context.Background(), "http://evil.tld/setup.exe",
		"d004c39288ce9ada487c6f398c7c545f7d749e44bdfdd59dbc9f865afba4e1ad", "x-*", nil)
	if !отвергнутПоСхеме(err) {
		t.Fatalf("АТАКА ПРОШЛА: скачивание по открытому каналу не отвергнуто (ошибка: %v)", err)
	}

	// И сама сумма по открытому каналу бессмысленна.
	_, err = FetchSHA256(context.Background(), "http://evil.tld/setup.exe.sha256")
	if !отвергнутПоСхеме(err) {
		t.Fatalf("АТАКА ПРОШЛА: сумма по открытому каналу не отвергнута (ошибка: %v)", err)
	}
}

// Атака 4: сумма подсунута мусором, чтобы сверка не состоялась.
func TestАтакаМусорВместоСуммы(t *testing.T) {
	for _, сумма := range []string{"", "   ", "нет", "0x0", "ZZZZ", "d004c392"} {
		evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"version":"ms7.vs9.9","url":"https://evil.tld/x.exe","sha256":%q}`, сумма)
		}))
		if _, err := Check(context.Background(), evil.URL, "ms7.vs2.2"); err == nil {
			evil.Close()
			t.Fatalf("АТАКА ПРОШЛА: принята сумма %q", сумма)
		}
		evil.Close()
	}
}

// Честный выпуск при этом обязан работать — иначе защита превратится в поломку.
func TestЧестныйВыпускРаботает(t *testing.T) {
	файл := []byte("настоящий установщик")
	сумма := sha256.Sum256(файл)
	текст := hex.EncodeToString(сумма[:])

	var адрес string
	сервер := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file" {
			w.Write(файл)
			return
		}
		fmt.Fprintf(w, `{"version":"ms7.vs9.9","url":%q,"sha256":%q}`, адрес+"/file", текст)
	}))
	defer сервер.Close()
	адрес = сервер.URL

	result, err := Check(context.Background(), сервер.URL, "ms7.vs2.2")
	if err != nil {
		t.Fatalf("честный выпуск отвергнут: %v", err)
	}
	if !result.HasUpdate || result.SHA256 != текст {
		t.Fatalf("не увидел обновление: %+v", result)
	}
	path, err := Download(context.Background(), result.DownloadURL, result.SHA256, "ms7vpn-test-*", nil)
	if err != nil {
		t.Fatalf("честный файл не скачался: %v", err)
	}
	defer os.Remove(path)
	t.Logf("честный выпуск принят, файл проверен по сумме")
}
