// Package splitroute — общие правила «Без VPN» для обоих ядер.
//
// Здесь только данные и очистка ввода: какие домены считаются российскими,
// как привести строку, вставленную человеком, к домену или имени программы.
// Сами правила маршрутизации собирают xray и singbox — каждый в своём
// формате.
package splitroute

import (
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"golang.org/x/net/idna"
)

// RuZones — доменные зоны, которые целиком идут напрямую при DirectRu.
// «xn--p1ai» — это .рф, «xn--d1acj3b» — .дети, «xn--80adxhks» — .москва.
var RuZones = []string{
	"ru", "su", "xn--p1ai", "xn--d1acj3b", "xn--80adxhks", "xn--80asehdb", "xn--c1avg",
	"moscow", "tatar",
}

// RuServices — крупные российские сервисы вне зон .ru/.рф. Без них «Яндекс»,
// «ВКонтакте» и банки шли бы через VPN: у многих основные адреса на .com или
// .net. Список работает и без geosite.dat, который в обычной установке не
// ставится.
var RuServices = []string{
	// Яндекс
	"yandex.com", "yandex.net", "yastatic.net", "yandexcloud.net", "ya.cc", "yandex.by", "yandex.kz",
	"kinopoisk.ru", "turbopages.org", "yango.com", "dzen.ru", "naydex.net",
	// VK и Mail.ru
	"vk.com", "vk.me", "vkuser.net", "userapi.com", "vk-cdn.net", "vkuservideo.net", "vkvideo.ru",
	"mycdn.me", "ok.me", "odnoklassniki.ru", "mail.ru", "imgsmail.ru", "mradx.net", "vk.company",
	// Маркетплейсы и доставка
	"wildberries.ru", "wbbasket.ru", "wbstatic.net", "wb.ru", "ozon.ru", "ozone.ru", "ozonusercontent.com",
	"avito.st", "avito.ru", "megamarket.tech", "aliexpress.ru",
	// Банки и госуслуги
	"sber.ru", "sberbank.com", "sberdevices.ru", "tbank.ru", "tinkoff.ru", "tinkoff.com", "tcsbank.ru",
	"alfabank.ru", "vtb.ru", "gazprombank.ru", "raiffeisen.ru", "nspk.ru", "mironline.ru",
	"gosuslugi.ru", "nalog.gov.ru", "mos.ru", "esia.gosuslugi.ru",
	// Операторы и медиа
	"mts.ru", "megafon.ru", "beeline.ru", "tele2.ru", "t2.ru", "rutube.ru", "ivi.ru", "okko.tv",
	"wink.ru", "premier.one", "kion.ru", "start.ru", "rambler.ru", "lenta.ru", "ria.ru", "rbc.ru",
	"2gis.com", "2gis.ru", "hh.ru", "cian.ru", "drom.ru", "auto.ru", "kaspersky.com",
	"max.ru", "oneme.ru",
}

// Rule — одна запись списка «Напрямую» / «Через VPN» после разбора.
// Kind: domain (домен с поддоменами), keyword (часть имени), cidr (адрес или
// подсеть), geosite / geoip (категория из баз Xray).
type Rule struct {
	Kind  string
	Value string
}

// CleanRules принимает строки как их ввёл человек: домены и ссылки,
// IP-адреса и подсети, «keyword:торрент», «geosite:netflix», «geoip:ru».
// Возвращает нормализованные строки в том же формате — их и хранят
// настройки. Неразобранное отбрасывается.
func CleanRules(values []string, limit int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		rule, ok := ParseRule(value)
		if !ok {
			continue
		}
		text := rule.String()
		if seen[text] {
			continue
		}
		seen[text] = true
		out = append(out, text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (r Rule) String() string {
	switch r.Kind {
	case "domain", "cidr":
		return r.Value
	default:
		return r.Kind + ":" + r.Value
	}
}

// ParseRule разбирает одну запись.
func ParseRule(value string) (Rule, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return Rule{}, false
	}
	lower := strings.ToLower(raw)
	for _, prefix := range []string{"geosite:", "geoip:", "keyword:"} {
		if strings.HasPrefix(lower, prefix) {
			body := strings.TrimSpace(lower[len(prefix):])
			if body == "" || len(body) > 80 || strings.ContainsAny(body, " \t/\\\"'") {
				return Rule{}, false
			}
			return Rule{Kind: strings.TrimSuffix(prefix, ":"), Value: body}, true
		}
	}
	if strings.HasPrefix(lower, "domain:") || strings.HasPrefix(lower, "full:") {
		raw = raw[strings.Index(raw, ":")+1:]
	}
	if cidr, ok := normalizeCIDR(raw); ok {
		return Rule{Kind: "cidr", Value: cidr}, true
	}
	if domain := NormalizeDomain(raw); domain != "" {
		return Rule{Kind: "domain", Value: domain}, true
	}
	return Rule{}, false
}

func normalizeCIDR(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "/") {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return "", false
		}
		return network.String(), true
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return "", false
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.String() + "/32", true
	}
	return ip.String() + "/128", true
}

// Split раскладывает записи по видам.
func Split(values []string) (domains, keywords, cidrs, geosites, geoips []string) {
	for _, value := range values {
		rule, ok := ParseRule(value)
		if !ok {
			continue
		}
		switch rule.Kind {
		case "domain":
			domains = append(domains, rule.Value)
		case "keyword":
			keywords = append(keywords, rule.Value)
		case "cidr":
			cidrs = append(cidrs, rule.Value)
		case "geosite":
			geosites = append(geosites, rule.Value)
		case "geoip":
			geoips = append(geoips, rule.Value)
		}
	}
	return
}

// CleanDomains приводит каждую строку к домену: «https://www.Kino.ru/x» →
// «kino.ru», кириллица — в punycode, как домены видит ядро. Мусор и повторы
// выбрасываются, список ограничен limit записями.
func CleanDomains(values []string, limit int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		domain := NormalizeDomain(value)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// NormalizeDomain возвращает домен или пустую строку, если строка на домен
// не похожа.
func NormalizeDomain(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	raw = strings.TrimPrefix(raw, "*.")
	raw = strings.TrimPrefix(raw, ".")
	if raw == "" {
		return ""
	}
	host := raw
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#") {
		candidate := raw
		if !strings.Contains(candidate, "://") {
			candidate = "http://" + candidate
		}
		parsed, err := url.Parse(candidate)
		if err != nil {
			return ""
		}
		host = parsed.Hostname()
	} else if i := strings.LastIndex(raw, ":"); i > 0 && !strings.Contains(raw[:i], ":") {
		host = raw[:i]
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "www."), ".")
	if !isASCII(host) {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return ""
		}
		host = ascii
	}
	if !strings.Contains(host, ".") || len(host) > 253 {
		return ""
	}
	labels := strings.Split(host, ".")
	allDigits := true
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return ""
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return ""
			}
			if r < '0' || r > '9' {
				allDigits = false
			}
		}
	}
	if allDigits {
		// IP-адрес: доменом не считаем.
		return ""
	}
	return host
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}

// CleanApps приводит строки к именам программ: «C:\Program Files\X\Telegram.exe»
// → «telegram.exe». Без расширения дописывается .exe.
func CleanApps(values []string, limit int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		name := strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
		if name == "" {
			continue
		}
		name = filepath.Base(strings.ReplaceAll(name, "/", `\`))
		if i := strings.LastIndex(name, `\`); i >= 0 {
			name = name[i+1:]
		}
		name = strings.ToLower(name)
		if !strings.HasSuffix(name, ".exe") {
			name += ".exe"
		}
		if len(name) > 120 || strings.ContainsAny(name, `<>:"|?*`) || name == ".exe" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Limits for user lists: keep configs small and the UI responsive.
const (
	MaxDomains = 500
	MaxApps    = 100
)
