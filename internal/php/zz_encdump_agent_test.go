package php

// Дампер вывода одного .phpt нашим движком (диагностика при сверке с php):
//   PHPT_FILE=<файл> DUMP_OUT=<файл> go test -run TestZZEncDump ./internal/php/

import (
	"os"
	"testing"
)

func TestZZEncDump(t *testing.T) {
	f := os.Getenv("PHPT_FILE")
	out := os.Getenv("DUMP_OUT")
	if f == "" || out == "" {
		t.Skip("PHPT_FILE/DUMP_OUT не заданы")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	if p.File == "" {
		t.Fatal("нет FILE section")
	}
	got, rerr := runPhptAt(p, f)
	if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
		t.Fatal(err)
	}
	if rerr != nil {
		t.Logf("run error: %v", rerr)
	}
}
