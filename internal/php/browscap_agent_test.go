package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// browscap_agent_test.go — регрессии get_browser(): разбор browscap.ini,
// сопоставление User-Agent (префикс/contains/regex, выбор специфичного),
// наследование Parent, true/false-значения, отсутствие Default-секции.
// Сверка с живым php 8.3 (get_browser встроен в standard).

const agentBrowscapINI = `; browscap sample
[GJK_Browscap_Version]
Version=1

[*]
Browser="Default Browser"
Version=0
Platform=unknown
Alpha=false
Frames=true
Tables=on
Cookies=no
AuthenticodeUpdate=0

[Opera]
Parent=*
Browser="Opera"
Frames=true

[Opera/7.1* (Windows NT 5.1; ?)*]
Parent=Opera
Version=7.1
MajorVer=7
Platform=WinXP
Win32=true

[Mozilla/5.0 (Macintosh*Mac OS X*) AppleWebKit/* Safari/*]
Browser="Safari"
Platform=MacOSX
`

func runBrowscapPHP(t *testing.T, ini, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	iniPath := filepath.Join(dir, "browscap.ini")
	if ini != "" {
		if err := os.WriteFile(iniPath, []byte(ini), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-d", "error_reporting=E_ALL", "-d", "display_errors=1", "-d", "log_errors=0", "-d", "opcache.enable=0"}
	if ini != "" {
		args = append(args, "-d", "browscap="+iniPath)
	}
	args = append(args, f)
	out, _ := exec.Command(livePHP(t), args...).CombinedOutput()
	return strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
}

func checkBrowscapDiff(t *testing.T, name, ini, src string) {
	t.Helper()
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "browscap.ini")
	SetIni(map[string]string{"browscap": iniPath})
	if ini == "" {
		SetIni(nil)
	}
	if ini != "" {
		if err := os.WriteFile(iniPath, []byte(ini), 0o644); err != nil {
			t.Fatal(err)
		}
		// Один и тот же путь с разным содержимым: кэш по mtime не нужен —
		// тесты используют уникальные TempDir, но очистим на всякий случай.
		delete(browscapCache, iniPath)
		delete(browscapFailed, iniPath)
	}
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil {
		t.Fatalf("%s: run error: %v", name, err)
	}
	want := runBrowscapPHP(t, ini, src)
	// Локация диагностик совпадает (diff.php после нормализации каталога).
	if got != want {
		t.Errorf("%s:\n--- got ---\n%q\n--- want ---\n%q", name, got, want)
	}
}

func TestBrowscapAgentVsPHP(t *testing.T) {
	livePHP(t)
	t.Run("match_and_parent", func(t *testing.T) {
		checkBrowscapDiff(t, "match_and_parent", agentBrowscapINI, `<?php
var_dump(get_browser("Opera/7.11 (Windows NT 5.1; U) [en]", true));
var_dump(get_browser("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605 Safari/605", true));
var_dump(get_browser("unknown-agent", true));
`)
	})
	t.Run("object_and_no_default", func(t *testing.T) {
		checkBrowscapDiff(t, "object", agentBrowscapINI, `<?php
$b = get_browser("Opera/7.11 (Windows NT 5.1; U) [en]");
var_dump($b->browser, $b->version, $b->win32, isset($b->platform));
`)
		checkBrowscapDiff(t, "no_default", "[Firefox]\nBrowser=\"Firefox\"\n", `<?php
var_dump(get_browser(""));
`)
	})
	t.Run("not_set_warning", func(t *testing.T) {
		checkBrowscapDiff(t, "not_set", "", `<?php
var_dump(get_browser("x"));
`)
	})
}
