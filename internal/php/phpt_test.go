package php

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// PhpT — тест в формате php-src .phpt.
type PhpT struct {
	Test        string
	File        string
	Clean       string
	Expect      string
	Expectf     string
	HasExpect   bool
	HasExpectf  bool
	HasClean    bool
	HasSkipIf   bool
	HasExpectRe bool
	HasFileExt  bool
	HasExternal bool

	// Extensions — список расширений из секции --EXTENSIONS--.
	Extensions []string

	// Секции, заполняющие суперглобалы (сырые строки как в .phpt).
	Get     string // --GET--   query-строка
	Post    string // --POST--  urlencoded-тело
	PostRaw string // --POST_RAW-- заголовки (+ multipart-тело)
	Cookie  string // --COOKIE--
	Env     string // --ENV--
	Server  string // --SERVER--
	Args    string // --ARGS--
	Ini     string // --INI--   настройки, влияющие на разбор входных данных
	Stdin   string // --STDIN--  данные стандартного ввода

	HasGet     bool
	HasPost    bool
	HasPostRaw bool
	HasCookie  bool
	HasEnv     bool
	HasServer  bool
	HasArgs    bool
	HasIni     bool
	HasStdin   bool
}

var phptSectionRe = regexp.MustCompile(`(?m)^--([A-Z][A-Z0-9_]*)--[ \t]*\n`)

// parsePhpt разбирает .phpt на секции.
func parsePhpt(data string) PhpT {
	data = strings.ReplaceAll(data, "\r\n", "\n")
	var p PhpT
	loc := phptSectionRe.FindAllStringSubmatchIndex(data, -1)
	for i, m := range loc {
		name := data[m[2]:m[3]]
		start := m[1]
		end := len(data)
		if i+1 < len(loc) {
			end = loc[i+1][0]
		}
		content := strings.TrimSuffix(data[start:end], "\n")
		switch name {
		case "TEST":
			p.Test = content
		case "FILE", "FILEEOF":
			p.File = content
		case "CLEAN":
			p.Clean, p.HasClean = content, true
		case "EXPECT":
			p.Expect, p.HasExpect = content, true
		case "EXPECTF":
			p.Expectf, p.HasExpectf = content, true
		case "EXPECTREGEX":
			p.HasExpectRe = true
		case "SKIPIF":
			p.HasSkipIf = true
		case "FILE_EXTERNAL":
			p.HasFileExt = true
		case "EXTENSIONS":
			for _, ln := range strings.Split(content, "\n") {
				if ln = strings.TrimSpace(ln); ln != "" {
					p.Extensions = append(p.Extensions, ln)
				}
			}
		case "GET":
			p.Get, p.HasGet = content, true
		case "POST":
			p.Post, p.HasPost = content, true
		case "POST_RAW":
			p.PostRaw, p.HasPostRaw = content, true
		case "COOKIE":
			p.Cookie, p.HasCookie = content, true
		case "ENV":
			p.Env, p.HasEnv = content, true
		case "SERVER":
			p.Server, p.HasServer = content, true
		case "ARGS":
			p.Args, p.HasArgs = content, true
		case "INI":
			p.Ini, p.HasIni = content, true
		case "STDIN":
			p.Stdin, p.HasStdin = content, true
		}
	}
	return p
}

// expectfToRegex повторяет EXPECTF-подстановки php.
func expectfToRegex(want string) *regexp.Regexp {
	w := strings.ReplaceAll(want, "\r\n", "\n")
	w = strings.ToValidUTF8(w, "\uFFFD")
	// %r...%r — сырые регэкспы: снимаем квотирование ДО остальных подстановок.
	var b strings.Builder
	rest := w
	for {
		i := strings.Index(rest, "%r")
		if i < 0 {
			b.WriteString(regexp.QuoteMeta(rest))
			break
		}
		b.WriteString(regexp.QuoteMeta(rest[:i]))
		rest = rest[i+2:]
		j := strings.Index(rest, "%r")
		if j < 0 {
			b.WriteString(regexp.QuoteMeta(rest))
			break
		}
		b.WriteString("(" + rest[:j] + ")")
		rest = rest[j+2:]
	}
	repl := strings.NewReplacer(
		"%e", regexp.QuoteMeta(string('/')),
		"%s", `[^\r\n]+`, "%S", `[^\r\n]*`, "%a", `.+`, "%A", `(?s).*`,
		"%w", `\s*`, "%i", `[+-]?\d+`, "%d", `\d+`, "%x", `[0-9a-fA-F]+`,
		"%f", `[+-]?(?:\d+\.?\d*|\.\d+)(?:[Ee][+-]?\d+)?`, "%c", `.`, "%0", "\x00",
	)
	expr := "(?s)^" + repl.Replace(b.String()) + "$"
	re, err := regexp.Compile(expr)
	if err != nil {
		// некорректный регэксп — тест не должен «случайно» проходить
		return regexp.MustCompile(`\A\z`)
	}
	return re
}

// expectfMatch сопоставляет EXPECTF-шаблон с выводом. Гот также проходит
// через ToValidUTF8, иначе «битые» байтовые последовательности в выводе
// декодируются регэкспом как отдельные U+FFFD и не совпадают с одним
// U+FFFD, полученным при нормализации шаблона.
func expectfMatch(want, got string) bool {
	return expectfToRegex(want).MatchString(strings.ToValidUTF8(got, "\uFFFD"))
}

func (p PhpT) run(t *testing.T) {
	t.Helper()
	c, err := Compile(p.File)
	if err != nil {
		t.Fatalf("%s: compile: %v", p.Test, err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	var out bytes.Buffer
	m.SetOutput(&out)
	if err := m.Run(); err != nil {
		if throwableOf(err) == nil {
			t.Fatalf("%s: run: %v", p.Test, err)
		}
	}
	got := out.String()
	if (p.HasExpect || p.Expect != "") && strings.TrimSpace(got) != strings.TrimSpace(p.Expect) {
		t.Errorf("%s:\n vm  = %q\n php = %q", p.Test, got, p.Expect)
	}
	if (p.HasExpectf || p.Expectf != "") && !expectfMatch(strings.TrimSpace(p.Expectf), strings.TrimSpace(got)) {
		t.Errorf("%s: output %q не матчит EXPECTF %q", p.Test, got, p.Expectf)
	}
}

// TestPhptBasic — встроенные .phpt-тесты.
func TestPhptBasic(t *testing.T) {
	cases := []PhpT{
		{Test: "Hello World", File: `<?php echo "Hello World";`, Expect: "Hello World"},
		{Test: "arith", File: `<?php echo 1+2*3;`, Expect: "7"},
		{Test: "fib", File: `<?php
function fibonacci($n) { if ($n === 0) { return 0; } if ($n === 1) { return 1; } return fibonacci($n-1) + fibonacci($n-2); }
echo fibonacci(10);`, Expect: "55"},
	}
	for _, c := range cases {
		c.run(t)
	}
}

// TestPhptFromPHP — сверка вывода с живым php для --FILE-- из .phpt.
func TestPhptFromPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	raw := "--TEST-- Hello\n--FILE--\n<?php echo \"Hi\";\n--EXPECT--\nHi\n"
	p := parsePhpt(raw)
	c, err := Compile(p.File)
	if err != nil {
		t.Fatal(err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	var out bytes.Buffer
	m.SetOutput(&out)
	if err := m.Run(); err != nil {
		t.Fatal(err)
	}
	want, _ := exec.Command(phppath, "-r", `echo "Hi";`).Output()
	if out.String() != string(want) {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}
