//go:build !windows

package zapret

const isWindows = false

// Вне Windows режим не работает — он держится на драйвере WinDivert.
// Заглушки нужны, чтобы пакет собирался и тесты шли на любой системе:
// разбор стратегий и проверка доступности от платформы не зависят.

func IsAdministrator() bool { return false }
