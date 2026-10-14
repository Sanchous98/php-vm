package php

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTypes8Diff — дифференциальный прогон одного .phpt из
// Zend/tests/type_declarations. Печатает GOT/EXPECT и результат EXPECTF-матчинга.
//
//	PHPT_TYPES8=<файл|имя> go test -run TestTypes8Diff -v ./internal/php/
//
// Если задан DIFF_FILE — используется он (полный путь). Иначе значение
// PHPT_TYPES8 трактуется как имя файла относительно Zend/tests/type_declarations.
func TestTypes8Diff(t *testing.T) {
	f := os.Getenv("DIFF_FILE")
	if f == "" {
		name := os.Getenv("PHPT_TYPES8")
		if name == "" {
			t.Skip("DIFF_FILE или PHPT_TYPES8 не заданы")
		}
		root := os.Getenv("PHPT_DIR")
		if root == "" {
			root = "/home/dev/phpsrc/Zend/tests/type_declarations"
		}
		f = filepath.Join(root, name)
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	got, rerr := runPhptAt(p, f)
	raw := stripStartup(phpTrim(got))
	t.Logf("--- GOT ---\n%s", raw)
	if p.HasExpect {
		w := stripStartup(phpTrim(p.Expect))
		t.Logf("--- EXPECT ---\n%s", w)
		t.Logf("expect equal: %v", raw == w)
	}
	if p.HasExpectf {
		w := stripStartup(phpTrim(p.Expectf))
		t.Logf("--- EXPECTF ---\n%s", w)
		t.Logf("expectf match: %v", expectfMatch(w, raw))
	}
	if rerr != nil {
		t.Logf("run err: %v", rerr)
	}
}
