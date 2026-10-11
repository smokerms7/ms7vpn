package zapret

import "strings"

// shortErr убирает из ошибки технические хвосты, чтобы её можно было
// показать человеку в окне.
func shortErr(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "context canceled"):
		return "проверка прервана"
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline exceeded"):
		return "нет ответа"
	case strings.Contains(text, "reset by peer"), strings.Contains(text, "forcibly closed"),
		strings.Contains(text, "connection was aborted"):
		return "соединение сброшено"
	case strings.Contains(text, "no such host"):
		return "домен не разрешается"
	case strings.Contains(text, "refused"):
		return "соединение отклонено"
	case strings.Contains(text, "заморожено"):
		// Своё объяснение из fetchEnough — оно и так написано словами.
		if index := strings.LastIndex(text, ": "); index > 0 {
			return text[index+2:]
		}
	}
	if len(text) > 90 {
		text = text[:90] + "…"
	}
	return text
}
