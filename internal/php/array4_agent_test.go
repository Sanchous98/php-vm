package php

import (
	"os"
	"strings"
	"testing"
)

// TestArray4Got — печатает (или пишет в GOT_OUT) сырой вывод VM для DIFF_FILE.
//
//	DIFF_FILE=/path DIFF_OUT=/tmp/got.txt go test -run TestArray4Got -v ./internal/php/
func TestArray4Got(t *testing.T) {
	f := os.Getenv("DIFF_FILE")
	if f == "" {
		t.Skip("DIFF_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	got, rerr := runPhptAt(p, f)
	if out := os.Getenv("DIFF_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("--- GOT ---\n%s", got)
	if rerr != nil {
		t.Logf("run err: %v", rerr)
	}
}

// TestArray4Lines — компактный построчный diff got vs want для DIFF_FILE.
// Показывает только расходящиеся строки (с окном контекста), чтобы не тонуть
// в больших дампах array-тестов.
//
//	DIFF_FILE=/path/to/test.phpt go test -run TestArray4Lines -v ./internal/php/
func TestArray4Lines(t *testing.T) {
	f := os.Getenv("DIFF_FILE")
	if f == "" {
		t.Skip("DIFF_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	got, rerr := runPhptAt(p, f)
	want := p.Expect
	if p.HasExpect {
		if phpTrim(got) == phpTrim(want) {
			t.Logf("OK (expect equal)")
		}
	}
	if p.HasExpectf {
		want = p.Expectf
		if expectfMatch(phpTrim(want), phpTrim(got)) {
			t.Logf("OK (expectf match)")
		}
	}
	gl := strings.Split(phpTrim(got), "\n")
	wl := strings.Split(phpTrim(want), "\n")
	n := 0
	const maxShow = 80
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g == w {
			continue
		}
		n++
		if n > maxShow {
			continue
		}
		t.Logf(">> %3d got |%s|  want |%s|", i, g, w)
	}
	t.Logf("diff lines: %d (got %d, want %d)", n, len(gl), len(wl))
	if rerr != nil {
		t.Logf("run err: %v", rerr)
	}
}
