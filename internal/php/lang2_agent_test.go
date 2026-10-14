package php

import (
	"os"
	"strings"
	"testing"
)

// TestLang2Diff — дифференциальный прогон одного .phpt из tests/lang.
//
//	DIFF_FILE=/path/to/test.phpt go test -run TestLang2Diff -v ./internal/php/
func TestLang2Diff(t *testing.T) {
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
	t.Logf("--- GOT ---\n%s", got)
	if p.HasExpect {
		t.Logf("--- EXPECT ---\n%s", p.Expect)
		t.Logf("expect equal: %v", phpTrim(got) == phpTrim(p.Expect))
	}
	if p.HasExpectf {
		t.Logf("--- EXPECTF ---\n%s", p.Expectf)
		t.Logf("expectf match: %v", expectfMatch(phpTrim(p.Expectf), phpTrim(got)))
	}
	if rerr != nil {
		t.Logf("run err: %v", rerr)
	}
}

// TestLang2Eval — прогоняет PHP-исходник из PHPT_SRC и печатает результат.
func TestLang2Eval(t *testing.T) {
	src := os.Getenv("PHPT_SRC")
	if src == "" {
		t.Skip("PHPT_SRC не задан")
	}
	got, err := runFile(src)
	t.Logf("--- GOT ---\n%s", got)
	if err != nil {
		t.Logf("err: %v", err)
	}
}

// TestLang2Lines — построчный diff got vs want (DIFF_FILE).
func TestLang2Lines(t *testing.T) {
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
	if p.HasExpectf {
		want = p.Expectf
	}
	gl := strings.Split(phpTrim(got), "\n")
	wl := strings.Split(phpTrim(want), "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		mark := "  "
		if g != w {
			mark = ">>"
		}
		t.Logf("%s %3d |%s|  want |%s|", mark, i, g, w)
	}
	if rerr != nil {
		t.Logf("run err: %v", rerr)
	}
}
