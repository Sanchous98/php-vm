package php

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func escapeNL(s string) string {
	return strings.Trim(strconv.Quote(s), `"`)
}

// TestStrProbe — проверяет варианты EXPECTF-матчинга для DIFF_FILE.
func TestStrProbe(t *testing.T) {
	f := os.Getenv("DIFF_FILE")
	if f == "" {
		t.Skip("DIFF_FILE")
	}
	data, _ := os.ReadFile(f)
	p := parsePhpt(string(data))
	got, err := runPhptAt(p, f)
	if throwableOf(err) != nil {
		err = nil
	}
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := strings.TrimSpace(p.Expectf)
	rawWant := want
	if p.HasExpect {
		want = strings.TrimSpace(p.Expect)
		rawWant = want
	}
	g := stripStartup(strings.TrimSpace(strings.ReplaceAll(got, "\r\n", "\n")))
	os.WriteFile("/home/dev/work/probe_got.txt", []byte(g), 0644)
	os.WriteFile("/home/dev/work/probe_want.txt", []byte(rawWant), 0644)
	if len(want) == 0 {
		t.Logf("no expectation")
		return
	}
	if p.HasExpect {
		t.Logf("expect-match: %v", g == want)
	} else {
		t.Logf("expectf-match: %v", expectfMatch(want, g))
	}
}

// TestStrDiff — печатает построчный дифф got vs want для DIFF_FILE.
//
//	DIFF_FILE=/path/file.phpt go test -run TestStrDiff -v ./internal/php/
func TestStrDiff(t *testing.T) {
	f := os.Getenv("DIFF_FILE")
	if f == "" {
		t.Skip("DIFF_FILE")
	}
	data, _ := os.ReadFile(f)
	p := parsePhpt(string(data))
	got, err := runPhptAt(p, f)
	if throwableOf(err) != nil {
		err = nil
	}
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	g := stripStartup(phpTrim(strings.ReplaceAll(got, "\r\n", "\n")))
	// want: если REF=1 и есть reference php — берём его вывод (точный дифф).
	want := ""
	if os.Getenv("REF") != "" {
		if out, ok := runReferencePHP(p, f); ok {
			want = stripStartup(phpTrim(strings.ReplaceAll(out, "\r\n", "\n")))
		}
	}
	if want == "" {
		if p.HasExpect {
			want = stripStartup(phpTrim(strings.ReplaceAll(p.Expect, "\r\n", "\n")))
		} else {
			want = stripStartup(phpTrim(strings.ReplaceAll(p.Expectf, "\r\n", "\n")))
		}
	}
	gl := strings.Split(g, "\n")
	wl := strings.Split(want, "\n")
	shown := 0
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var gv, wv string
		if i < len(gl) {
			gv = gl[i]
		}
		if i < len(wl) {
			wv = wl[i]
		}
		if gv == wv {
			continue
		}
		t.Logf("L%d\n  got : %s\n  want: %s", i+1, strconv.Quote(gv), strconv.Quote(wv))
		shown++
		if shown >= 60 {
			t.Logf("... (%d lines total: got=%d want=%d)", intMax(len(gl), len(wl)), len(gl), len(wl))
			break
		}
	}
	if shown == 0 && want != "" && p.HasExpectf && os.Getenv("REF") == "" {
		if !expectfMatch(want, g) {
			t.Logf("lines equal but EXPECTF-match=false (regex issue); len got=%d want=%d", len(g), len(want))
		}
	}
}

// runReferencePHP сохраняет FILE рядом (как runPhptAt) и запускает php 8.3.
func runReferencePHP(p PhpT, phptPath string) (string, bool) {
	src := p.File + "\n"
	phpFile := phptPath
	if strings.HasSuffix(phpFile, ".phpt") {
		phpFile = strings.TrimSuffix(phpFile, ".phpt") + ".php"
	} else {
		phpFile = "/home/dev/work/_ref.php"
	}
	if err := os.WriteFile(phpFile, []byte(src), 0644); err != nil {
		return "", false
	}
	defer os.Remove(phpFile)
	var ini []string
	if p.HasIni {
		for _, line := range strings.Split(p.Ini, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, ";") {
				continue
			}
			ini = append(ini, "-d"+line)
		}
	}
	args := append([]string{"-d", "display_errors=stdout", "-d", "log_errors=0", "-d", "error_reporting=-1", "-d", "html_errors=0"}, ini...)
	args = append(args, phpFile)
	cmd := exec.Command("php", args...)
	cmd.Env = append(os.Environ(), "TEST_PHP_EXECUTABLE=php")
	out, _ := cmd.CombinedOutput()
	return string(out), true
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestStrBatch — печатает REF-диффы для списка файлов из OUT_FILE
// (по одному basename в строке, формат TestStrEnumerate).
func TestStrBatch(t *testing.T) {
	list := os.Getenv("OUT_FILE")
	root := os.Getenv("PHPT_DIR")
	if list == "" || root == "" {
		t.Skip("OUT_FILE/PHPT_DIR")
	}
	data, err := os.ReadFile(list)
	if err != nil {
		t.Fatalf("read list: %v", err)
	}
	only := os.Getenv("ONLY")
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		if name == "" {
			continue
		}
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		f := filepath.Join(root, name)
		src, err := os.ReadFile(f)
		if err != nil {
			t.Logf("### %s: read err %v", name, err)
			continue
		}
		p := parsePhpt(string(src))
		got, rerr := runPhptAt(p, f)
		if throwableOf(rerr) != nil {
			rerr = nil
		}
		if rerr != nil {
			t.Logf("### %s: RUN ERROR %v", name, rerr)
			continue
		}
		g := stripStartup(phpTrim(strings.ReplaceAll(got, "\r\n", "\n")))
		want := ""
		if out, ok := runReferencePHP(p, f); ok {
			want = stripStartup(phpTrim(strings.ReplaceAll(out, "\r\n", "\n")))
		}
		if want == "" {
			if p.HasExpect {
				want = stripStartup(phpTrim(strings.ReplaceAll(p.Expect, "\r\n", "\n")))
			} else {
				want = stripStartup(phpTrim(strings.ReplaceAll(p.Expectf, "\r\n", "\n")))
			}
		}
		gl := strings.Split(g, "\n")
		wl := strings.Split(want, "\n")
		n := 0
		var diffs []string
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var gv, wv string
			if i < len(gl) {
				gv = gl[i]
			}
			if i < len(wl) {
				wv = wl[i]
			}
			if gv == wv {
				continue
			}
			diffs = append(diffs, fmt.Sprintf("  L%d got =%s\n        want=%s", i+1, strconv.Quote(gv), strconv.Quote(wv)))
			n++
			if n >= 6 {
				break
			}
		}
		if n == 0 {
			t.Logf("### %s: lines equal (EXPECTF regex issue?)", name)
			continue
		}
		t.Logf("### %s\n%s", name, strings.Join(diffs, "\n"))
	}
}

// TestStrEnumerate — перечисляет все провалы в PHPT_DIR (для агентской работы).
//
//	PHPT_DIR=/path go test -run TestStrEnumerate -v ./internal/php/
func TestStrEnumerate(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	out := os.Getenv("OUT_FILE")
	if out == "" {
		out = "/home/dev/work/strings_fails.txt"
	}
	var files []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	var b strings.Builder
	pass, fail, skip := 0, 0, 0
	for _, f := range files {
		data, _ := os.ReadFile(f)
		p := parsePhpt(string(data))
		if p.HasSkipIf || p.HasExpectRe || p.HasFileExt || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			skip++
			continue
		}
		got, err := runPhptAt(p, f)
		if throwableOf(err) != nil {
			err = nil
		}
		ok := err == nil
		if ok {
			g := stripStartup(strings.TrimSpace(strings.ReplaceAll(got, "\r\n", "\n")))
			if p.HasExpect {
				ok = g == stripStartup(strings.TrimSpace(p.Expect))
			}
			if ok && p.HasExpectf {
				ok = expectfMatch(stripStartup(strings.TrimSpace(p.Expectf)), g)
			}
		}
		if ok {
			pass++
			continue
		}
		fail++
		why := "mismatch"
		if err != nil {
			why = "run: " + err.Error()
		} else {
			want := p.Expect
			if p.HasExpectf {
				want = p.Expectf
			}
			why = "mismatch got=" + clip(escapeNL(got)) + " want=" + clip(escapeNL(want))
		}
		b.WriteString(filepath.Base(f) + ": " + why + "\n")
	}
	os.WriteFile(out, []byte(b.String()), 0644)
	t.Logf("ИТОГО: %d / %d (%.1f%%), пропущено %d -> %s", pass, pass+fail, 100*float64(pass)/float64(pass+fail), skip, out)
}
