package app

import (
	"testing"

	"ms7vpn/internal/model"
)

// Адрес проверки обновлений приходит из интерфейса, то есть доступен всякому,
// кто добрался до локального токена. Чужой узел должен молча заменяться на
// свой, иначе через установку обновления запускается произвольный файл.
func TestNormalizeSettingsKeepsUpdateURLOnOurHosts(t *testing.T) {
	fallback := model.DefaultSettings().UpdateURL

	allowed := []string{
		"https://github.com/smokerms7/ms7vpn",
		"https://api.github.com/repos/smokerms7/ms7vpn/releases/latest",
		"https://ms7pc.shop/app/latest.json",
		"https://vpn.ms7pc.shop/app/latest.json",
	}
	for _, value := range allowed {
		settings := model.DefaultSettings()
		settings.UpdateURL = value
		if got := normalizeSettings(settings).UpdateURL; got != value {
			t.Errorf("свой адрес %q заменён на %q", value, got)
		}
	}

	rejected := []string{
		"http://github.com/smokerms7/ms7vpn",     // открытый канал
		"https://github.com.evil.tld/x",          // похожее имя, чужой узел
		"https://evil.tld/latest.json",           // чужой узел
		"https://127.0.0.1:8080/latest.json",     // свой же компьютер, но не наш узел
		"ftp://github.com/x",                     // не та схема
		"  ",                                     // пусто
		"https://notgithub.com/smokerms7/ms7vpn", // имя лишь содержит наше
	}
	for _, value := range rejected {
		settings := model.DefaultSettings()
		settings.UpdateURL = value
		if got := normalizeSettings(settings).UpdateURL; got != fallback {
			t.Errorf("чужой адрес %q остался как %q", value, got)
		}
	}
}
