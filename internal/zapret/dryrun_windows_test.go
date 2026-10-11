//go:build windows

package zapret

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Все стратегии бандла должны проходить проверку самого winws.
//
// Это единственная проверка, которая ловит ошибку в стратегии целиком: наш
// разбор видит только знакомые ключи, а winws — ещё и значения, и
// существование файлов, на которые стратегия ссылается. Без неё опечатка в
// пути к поддельному пакету выяснилась бы у человека при включении.
//
// Драйвер при --dry-run не поднимается и права администратора не нужны,
// поэтому тест идёт в обычной сборке.
func TestBundleStrategiesPassWinwsDryRun(t *testing.T) {
	bundle := bundlePath(t)
	if _, err := os.Stat(filepath.Join(bundle, ExeName)); err != nil {
		t.Skip("бандла нет рядом с исходниками — проверять нечего")
	}
	list, err := LoadStrategies(filepath.Join(bundle, "strategies.json"))
	if err != nil {
		t.Fatalf("стратегии не прочитаны: %v", err)
	}

	dir := t.TempDir()
	m := NewManager(dir)
	m.binDir = bundle
	autoPath := filepath.Join(dir, "autohostlist.txt")
	if err := os.WriteFile(autoPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, strategy := range list {
		t.Run(strategy.ID, func(t *testing.T) {
			args := Expand(strategy.Args, m.fakeDir(), m.listsDir(), autoPath)
			err := m.dryRun(context.Background(), args)
			if err == nil {
				return
			}
			// Вторая попытка нужна из-за самих тестов: пакеты go test идут
			// параллельно, а тесты internal/app создают приложение, которое
			// при запуске чистит остатки режима — `taskkill /IM
			// shade-winws.exe`. Под эту зачистку попадает и наша проверка,
			// запущенная в ту же секунду. В программе так не бывает: вторая
			// копия не запускается вообще.
			if retry := m.dryRun(context.Background(), args); retry != nil {
				t.Errorf("winws не принял стратегию: %v", retry)
			}
		})
	}
}
