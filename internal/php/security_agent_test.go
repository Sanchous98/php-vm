package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecurityOpenBasedir — точечная проверка enforcement open_basedir.
// Прогоняет .phpt из tests/security, если задан PHPT_DIR (или путь из
// окружения), иначе проверяет базовую арифметику разрешённых путей.
func TestSecurityOpenBasedir(t *testing.T) {
	// 1. Проверка разбора/сравнения путей без запуска VM.
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	// open_basedir=".", cwd=sub: "../x" вне разрешённого, "y" — внутри.
	SetIni(map[string]string{"open_basedir": "."})
	os.Chdir(sub)
	if openBasedirAllows("../x") {
		t.Errorf("../x должен быть запрещён")
	}
	if !openBasedirAllows("./y") {
		t.Errorf("./y должен быть разрешён")
	}
	// Нелокальные обёртки не проверяются.
	if !openBasedirAllows("php://memory") {
		t.Errorf("php://memory не должен проверяться")
	}
	// Начальная настройка сбрасывается.
	SetIni(nil)
	if openBasedirRaw() != "" {
		t.Fatalf("open_basedir должен сброситься, получено %q", openBasedirRaw())
	}
}

// TestSecurityOpenBasedirTree прогоняет каталог tests/security, если доступен.
func TestSecurityOpenBasedirTree(t *testing.T) {
	root := os.Getenv("PHPT_SECURITY_DIR")
	if root == "" {
		root = "/home/dev/phpsrc/tests/security"
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("нет каталога %s", root)
	}
	defer SetIni(nil)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skip(err)
	}
	pass, total := 0, 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".phpt") {
			continue
		}
		p := parsePhpt(readFileString(filepath.Join(root, e.Name())))
		if p.HasSkipIf || p.HasExpectRe || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			continue
		}
		if hasUnsupportedExt(p.Extensions) {
			continue
		}
		total++
		got, err := runPhptAt(p, filepath.Join(root, e.Name()))
		if err != nil {
			t.Logf("FAIL %s: run: %v", e.Name(), err)
			continue
		}
		g := stripStartup(phpTrim(strings.ReplaceAll(got, "\r\n", "\n")))
		ok := false
		if p.HasExpect {
			ok = g == stripStartup(phpTrim(p.Expect))
		}
		if !ok && p.HasExpectf {
			ok = expectfMatch(stripStartup(phpTrim(p.Expectf)), g)
		}
		if ok {
			pass++
		} else {
			t.Logf("FAIL %s", e.Name())
		}
	}
	t.Logf("security: %d/%d", pass, total)
	if total > 0 && pass != total {
		t.Errorf("security: %d/%d", pass, total)
	}
}

func readFileString(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}
