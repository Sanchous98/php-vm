package php

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// runWarnVM компилирует и исполняет исходник, как будто он лежит в file.
func runWarnVM(t *testing.T, src, file string) (string, error) {
	t.Helper()
	c, err := CompileFile(src, file)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(5000000)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	runErr := m.Run()
	if th := throwableOf(runErr); th != nil {
		runErr = nil
	}
	return buf.String(), runErr
}

// runWarnPHP исполняет тот же файл живым php.
func runWarnPHP(t *testing.T, file string) string {
	t.Helper()
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	cmd := exec.Command(php, "-d", "display_errors=1", "-d", "error_reporting=E_ALL", file)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	_ = cmd.Run()
	return out.String()
}

// TestWarnDiff сверяет полный вывод VM и живого PHP для типовых диагностик.
func TestWarnDiff(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"undefined_var", "<?php\nerror_reporting(E_ALL);\n$a = 1;\necho $foo;\necho \"|\\n\";\n"},
		{"undefined_var_2", "<?php\necho $undef, \"|\\n\";\n"},
		{"undefined_array_key_str", "<?php\n$a = [];\necho $a[\"k\"];\necho \"|\\n\";\n"},
		{"undefined_array_key_int", "<?php\n$a = [1];\necho $a[5];\necho \"|\\n\";\n"},
		{"array_offset_null", "<?php\n$n = null;\necho $n[\"x\"];\necho \"|\\n\";\n"},
		{"array_offset_int", "<?php\n$i = 5;\necho $i[0];\necho \"|\\n\";\n"},
		{"array_offset_bool", "<?php\n$b = false;\necho $b[1];\necho \"|\\n\";\n"},
		{"array_to_string_echo", "<?php\n$a = [1, 2];\necho $a;\necho \"|\\n\";\n"},
		{"array_to_string_concat", "<?php\n$a = [1, 2];\necho \"x\" . $a;\necho \"|\\n\";\n"},
		{"suppress_at", "<?php\necho @$undef, \"|\\n\";\necho \"after\\n\";\n"},
		{"coalesce", "<?php\n$x = $undef ?? \"d\";\necho $x, \"|\\n\";\n"},
		{"empty", "<?php\necho empty($undef) ? \"y\\n\" : \"n\\n\";\n"},
		{"isset", "<?php\necho isset($undef) ? \"y\\n\" : \"n\\n\";\n"},
		{"unset_then_read", "<?php\n$x = 1;\nunset($x);\necho $x, \"|\\n\";\n"},
		{"null_defined_no_warn", "<?php\n$x = null;\necho var_export($x, true), \"|\\n\";\n"},
		{"error_reporting_zero", "<?php\nerror_reporting(0);\necho $undef;\necho \"|\\n\";\n"},
		{"multiline_echo", "<?php\necho\n  $undef;\necho \"|\\n\";\n"},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(dir, tc.name+".php")
			if err := os.WriteFile(file, []byte(tc.src), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := runWarnVM(t, tc.src, file)
			if err != nil {
				t.Fatalf("run: %v\nout=%q", err, got)
			}
			want := runWarnPHP(t, file)
			if got != want {
				t.Errorf("output mismatch\n vm =%q\n php=%q", got, want)
			}
		})
	}
}

// TestWarnSuppressNested проверяет, что @ подавляет и внутри вложенных
// выражений, и что после @ предупреждения снова печатаются.
func TestWarnSuppressNested(t *testing.T) {
	src := "<?php\necho @($undef . $undef2), \"|\\n\";\necho $undef3, \"|\\n\";\n"
	dir := t.TempDir()
	file := filepath.Join(dir, "nested.php")
	os.WriteFile(file, []byte(src), 0644)
	got, _ := runWarnVM(t, src, file)
	want := runWarnPHP(t, file)
	if got != want {
		t.Errorf("mismatch\n vm =%q\n php=%q", got, want)
	}
	if !strings.Contains(got, "Undefined variable $undef3") {
		t.Errorf("после @ предупреждение должно печататься: %q", got)
	}
}
