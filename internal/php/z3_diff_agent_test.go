package php

// z3_diff_agent_test.go — вспомогательный инструмент агента «z3_subdirs».
//
// Z3_SNIPPET=<файл.php> (или Z3_SRC=<код>) — прогоняет код у нас и живым php,
// печатает оба вывода. Секция --INI-- из Z3_INI не нужна: для быстрых проверок.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

func z3RunOurs(t *testing.T, src string) string {
	t.Helper()
	c, err := CompileFile(src, "z3.php")
	if err != nil {
		return "COMPILE ERROR: " + err.Error()
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(0)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	err = m.Run()
	out := buf.String()
	if err != nil {
		if th := throwableOf(err); th == nil {
			out += fmt.Sprintf("<<RUNERR: %v>>\n", err)
		}
	}
	return out
}

// TestZ3Full пишет полные got/want/php по каждому файлу списка в Z3_OUTDIR.
func TestZ3Full(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	lp := os.Getenv("PHPT_LIST")
	out := os.Getenv("Z3_OUTDIR")
	if root == "" || lp == "" || out == "" {
		t.Skip("нет PHPT_DIR/PHPT_LIST/Z3_OUTDIR")
	}
	_ = os.MkdirAll(out, 0o755)
	data, err := os.ReadFile(lp)
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		f := filepath.Join(root, ln)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("нет файла %s", f)
			continue
		}
		p := parsePhpt(string(b))
		got, rerr := runPhptAt(p, f)
		php, perr := runRealPHP(p)
		php = strings.ReplaceAll(php, "\r\n", "\n")
		name := strings.ReplaceAll(ln, "/", "__")
		var sb strings.Builder
		fmt.Fprintf(&sb, "===== %s =====\n--- GOT (err=%v) ---\n%s\n--- WANT ---\n%s\n--- PHP (err=%v) ---\n%s\n", ln, rerr, got, p.Expect+p.Expectf, perr, php)
		_ = os.WriteFile(filepath.Join(out, name+".txt"), []byte(sb.String()), 0o644)
	}
}

// TestZ3Diff — прогон произвольного сниппета: Z3_SNIPPET=файл или Z3_SRC=код.
func TestZ3Diff(t *testing.T) {
	src := os.Getenv("Z3_SRC")
	if f := os.Getenv("Z3_SNIPPET"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = string(b)
	}
	if src == "" {
		t.Skip("Z3_SNIPPET/Z3_SRC не заданы")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "z3.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-d", "display_errors=1", "-d", "error_reporting=E_ALL", f}
	if ini := os.Getenv("Z3_INI"); ini != "" {
		for _, ln := range strings.Split(ini, ",") {
			if ln = strings.TrimSpace(ln); ln != "" {
				args = append([]string{"-d", ln}, args...)
			}
		}
	}
	real, _ := exec.Command("php", args...).CombinedOutput()
	ours := z3RunOurs(t, src)
	rs := strings.ReplaceAll(string(real), dir+"/", "")
	os_ := strings.ReplaceAll(ours, "z3.php", "$F")
	rs2 := strings.ReplaceAll(rs, "z3.php", "$F")
	fmt.Printf("\n===== LIVE PHP =====\n%s\n===== OURS =====\n%s\n===== DIFF =====\n%s\n===== END =====\n",
		rs2, os_, firstDiff(os_, rs2))
}
