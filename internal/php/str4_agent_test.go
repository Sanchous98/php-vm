package php

import (
	"bytes"
	"os/exec"
	"testing"

	"phpvm/internal/vm"
)

// runPHPCode выполняет PHP-код на этой VM и возвращает вывод.
func runPHPCode(t *testing.T, src string) string {
	t.Helper()
	c, err := CompileFile(src, "t.php")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(5000000)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	if err := m.Run(); err != nil {
		if throwableOf(err) == nil {
			t.Fatalf("run: %v", err)
		}
	}
	return buf.String()
}

// TestHighlightStringFromPHP сверяет highlight_string() с живым php
// (zend_highlight.c): цвета по умолчанию, escape, пробелы/табуляция.
func TestHighlightStringFromPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	SetIni(nil)
	defer SetIni(nil)
	cases := []string{
		`<?php echo "x";`,
		`<?php $a = 1; // comment` + "\n" + `if ($a) { echo 'ok'; }`,
		"<?php\nclass C {\n\tpublic $x = 1;\n}\n?>",
		`no php here <b>bold</b>`,
		`<?php $s = "a$b{c}d";`,
		`<?php echo <<<EOT` + "\nline\nEOT;\n",
	}
	for _, src := range cases {
		prog := `<?php echo highlight_string(` + phpStringLiteral(src) + `);`
		got := runPHPCode(t, prog)
		want, err := exec.Command(phppath, "-r", `echo highlight_string(`+phpStringLiteral(src)+`);`).Output()
		if err != nil {
			t.Fatalf("php: %v", err)
		}
		if got != string(want) {
			t.Errorf("highlight_string(%q):\n got %q\nwant %q", src, got, want)
		}
	}
}

// phpStringLiteral экранирует строку как одинарный PHP-литерал.
func phpStringLiteral(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' || s[i] == '\\' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	out = append(out, '\'')
	return string(out)
}
