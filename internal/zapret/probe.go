package zapret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Проверка «помогла ли стратегия» смотрит на прочитанное тело, а не на код
// ответа.
//
// ТСПУ часто пропускает начало соединения и замораживает его потом: сервер
// отвечает 200, приходит первый килобайт, и дальше тишина. Поэтому запрос
// считается удачным, только если тело дочитано до конца или до minProbeBytes —
// дальше читать незачем, заморозку это уже поймало.
const (
	minProbeBytes  = 24 << 10
	probeConnect   = 3 * time.Second
	probeTotal     = 5 * time.Second
	internetProbe  = "https://ya.ru/"
	internetBudget = 8 * time.Second
)

// Группы адресов. Группа считается открытой, только если открылись все её
// адреса: один домен может работать и при включённой блокировке, а три
// подряд — уже признак, что обход действительно сработал.
var (
	youtubeURLs = []string{
		"https://www.youtube.com/",
		"https://i.ytimg.com/vi/jNQXAC9IVRw/hqdefault.jpg",
		"https://redirector.googlevideo.com/report_mapping",
	}
	discordURLs = []string{
		"https://discord.com/",
		"https://gateway.discord.gg/",
		"https://cdn.discordapp.com/",
	}
)

// directClient — HTTP-клиент, который ходит напрямую.
//
// Системный прокси и переменные окружения игнорируются нарочно: проверять
// обход через туннель бессмысленно, результат получился бы про туннель.
func directClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: probeConnect}).DialContext,
			TLSHandshakeTimeout: probeConnect,
			DisableKeepAlives:   true,
			ForceAttemptHTTP2:   true,
		},
		// Перенаправления разрешаем: youtube.com отвечает ими в зависимости
		// от региона, и запрет их ломал бы проверку на ровном месте.
	}
}

// fetchEnough скачивает начало ответа и сообщает, хватило ли объёма.
func fetchEnough(ctx context.Context, client *http.Client, rawURL string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTotal)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	// Без привычного заголовка часть узлов Google отвечает короткой
	// страницей, которая не дотягивает до порога и выглядела бы как блокировка.
	request.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36")
	request.Header.Set("Accept", "*/*")

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	// 5xx — это ответ нашего же соединения, но говорящий о беде на стороне
	// сервера: судить по нему о блокировке нельзя. 403 и 404 оставляем: их
	// отдают gateway.discord.gg и cdn.discordapp.com в обычной жизни, и сам
	// факт ответа означает, что рукопожатие прошло и DPI не вмешался.
	if response.StatusCode >= 500 {
		return fmt.Errorf("ответ %d", response.StatusCode)
	}

	// Читаем тело до конца или до потолка — что раньше.
	//
	// Порог нужен не сам по себе: ТСПУ часто пропускает начало соединения и
	// замораживает его потом, поэтому код ответа ничего не доказывает, а
	// доказывает прочитанное тело. Но требовать 24 КБ от каждого адреса
	// нельзя: hqdefault.jpg весит 16 КБ, а gateway.discord.gg отвечает
	// пустым 404. Короткий, но дочитанный до конца ответ — такое же
	// доказательство, как длинный: соединение не оборвали на середине.
	read, err := io.Copy(io.Discard, io.LimitReader(response.Body, minProbeBytes))
	if err != nil {
		return fmt.Errorf("оборвалось на %d КБ: %w", read>>10, err)
	}
	return nil
}

// checkGroup проверяет все адреса группы разом.
//
// Адреса идут параллельно, но первая же неудача обрывает остальные: группа
// всё равно уже не считается открытой, а подбор перебирает двадцать две
// стратегии, и лишние секунды складываются в минуты.
func checkGroup(ctx context.Context, client *http.Client, urls []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var once sync.Once
	var failure error
	var wait sync.WaitGroup
	for _, rawURL := range urls {
		wait.Add(1)
		go func(rawURL string) {
			defer wait.Done()
			if err := fetchEnough(ctx, client, rawURL); err != nil {
				once.Do(func() {
					failure = fmt.Errorf("%s: %w", rawURL, err)
					cancel()
				})
			}
		}(rawURL)
	}
	wait.Wait()
	if failure != nil {
		return failure
	}
	return ctx.Err()
}

// Probe — одна проверка обеих групп.
type Probe struct {
	YouTube bool   `json:"youtube"`
	Discord bool   `json:"discord"`
	Details string `json:"details,omitempty"`
}

// CheckBoth проверяет YouTube и Discord.
//
// Если одна группа открылась, а другая нет, проверка повторяется один раз и
// итоги объединяются по ИЛИ: случайный обрыв сети не должен похоронить
// хорошую стратегию, а две неудачи подряд на одной группе — это уже не
// случайность.
func CheckBoth(ctx context.Context) Probe {
	client := directClient()
	probe := checkOnce(ctx, client)
	if probe.YouTube == probe.Discord {
		return probe
	}
	if ctx.Err() != nil {
		return probe
	}
	again := checkOnce(ctx, client)
	return Probe{
		YouTube: probe.YouTube || again.YouTube,
		Discord: probe.Discord || again.Discord,
		Details: firstNonEmpty(again.Details, probe.Details),
	}
}

func checkOnce(ctx context.Context, client *http.Client) Probe {
	var (
		probe                  Probe
		youtubeErr, discordErr error
		wait                   sync.WaitGroup
	)
	wait.Add(2)
	go func() {
		defer wait.Done()
		youtubeErr = checkGroup(ctx, client, youtubeURLs)
	}()
	go func() {
		defer wait.Done()
		discordErr = checkGroup(ctx, client, discordURLs)
	}()
	wait.Wait()

	probe.YouTube = youtubeErr == nil
	probe.Discord = discordErr == nil
	switch {
	case youtubeErr != nil && discordErr != nil:
		probe.Details = "YouTube: " + shortErr(youtubeErr) + "; Discord: " + shortErr(discordErr)
	case youtubeErr != nil:
		probe.Details = "YouTube: " + shortErr(youtubeErr)
	case discordErr != nil:
		probe.Details = "Discord: " + shortErr(discordErr)
	}
	return probe
}

// HasInternet проверяет, что сеть есть вообще.
//
// Без этого подбор перебрал бы все двадцать две стратегии и честно сообщил,
// что ни одна не помогла, — при выдернутом кабеле.
func HasInternet(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, internetBudget)
	defer cancel()

	client := directClient()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, internetProbe, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("нет интернета: " + shortErr(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	_ = response.Body.Close()
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
