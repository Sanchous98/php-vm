package php

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runBuiltinScript компилирует и запускает код с заполнением INI/суперглобалов.
func runBuiltinScript(t *testing.T, raw string) string {
	t.Helper()
	p := parsePhpt(raw)
	got, err := runPhptAt(p, "builtins2_test.phpt")
	if err != nil {
		if th := throwableOf(err); th == nil {
			t.Fatalf("run: %v", err)
		}
	}
	return strings.TrimSpace(stripStartup(got))
}

// --- INI / sys_get_temp_dir ---

func TestBuiltins2SysGetTempDirIni(t *testing.T) {
	got := runBuiltinScript(t, "--INI--\nsys_temp_dir=/path/to/temp/dir\n--FILE--\n<?php echo sys_get_temp_dir();")
	if got != "/path/to/temp/dir" {
		t.Fatalf("sys_get_temp_dir() = %q, want /path/to/temp/dir", got)
	}
	// Без sys_temp_dir — реальный os.TempDir().
	got = runBuiltinScript(t, "--FILE--\n<?php echo sys_get_temp_dir();")
	if got != os.TempDir() {
		t.Fatalf("sys_get_temp_dir() = %q, want %q", got, os.TempDir())
	}
}

func TestBuiltins2IniGetSet(t *testing.T) {
	got := runBuiltinScript(t, "--INI--\nprecision=17\nserialize_precision=-1\n--FILE--\n"+
		"<?php echo ini_get('precision'), '|', ini_get('serialize_precision'), '|', ini_get('nosuchopt') === false ? 'F' : 'T';")
	if got != "17|-1|F" {
		t.Fatalf("ini_get = %q, want 17|-1|F", got)
	}
	got = runBuiltinScript(t, "--FILE--\n<?php ini_set('precision', 9); echo ini_get('precision');")
	if got != "9" {
		t.Fatalf("ini_set/ini_get = %q, want 9", got)
	}
	// Дефолты CLI.
	got = runBuiltinScript(t, "--FILE--\n<?php echo ini_get('precision'), '|', ini_get('variables_order');")
	if got != "14|EGPCS" {
		t.Fatalf("defaults = %q, want 14|EGPCS", got)
	}
}

func TestBuiltins2IniParseQuantity(t *testing.T) {
	got := runBuiltinScript(t, "--FILE--\n<?php echo ini_parse_quantity('1k'), ' ', ini_parse_quantity('14.2bm'), ' ',"+
		" ini_parse_quantity('14.2mb'), ' ', ini_parse_quantity('boat'), ' ', ini_parse_quantity('-0x412');")
	// PHP 8.2+ печатает предупреждения о некорректных величинах (значения —
	// как раньше; проверено против php 8.3).
	want := "1024 \n" +
		"Warning: Invalid quantity \"14.2bm\", interpreting as \"14m\" for backwards compatibility in builtins2_test.php on line 1\n" +
		"14680064 \n" +
		"Warning: Invalid quantity \"14.2mb\": unknown multiplier \"b\", interpreting as \"14\" for backwards compatibility in builtins2_test.php on line 1\n" +
		"14 \n" +
		"Warning: Invalid quantity \"boat\": no valid leading digits, interpreting as \"0\" for backwards compatibility in builtins2_test.php on line 1\n" +
		"0 -1042"
	if got != want {
		t.Fatalf("ini_parse_quantity = %q", got)
	}
}

func TestBuiltins2GetCfgVarArray(t *testing.T) {
	got := runBuiltinScript(t, "--INI--\nary[a] = 1\nary[b] = 2\n--FILE--\n<?php var_dump(get_cfg_var('ary'));")
	want := "array(2) { [\"a\"]=> string(1) \"1\" [\"b\"]=> string(1) \"2\" }"
	if strings.Join(strings.Fields(got), " ") != want {
		t.Fatalf("get_cfg_var = %q", got)
	}
}

// --- расширения ---

func TestBuiltins2Extensions(t *testing.T) {
	got := runBuiltinScript(t, "--FILE--\n<?php echo extension_loaded('standard') ? 'Y' : 'N', extension_loaded('nope') ? 'Y' : 'N',"+
		" count(get_loaded_extensions()) > 0 ? 'E' : 'X';")
	if got != "YNE" {
		t.Fatalf("extensions = %q, want YNE", got)
	}
}

// --- get_defined_functions / get_defined_constants ---

func TestBuiltins2DefinedFunctionsDisabled(t *testing.T) {
	got := runBuiltinScript(t, "--INI--\ndisable_functions=dl\n--FILE--\n"+
		"<?php var_dump(in_array('dl', get_defined_functions()['internal']));")
	if got != "bool(false)" {
		t.Fatalf("get_defined_functions disabled = %q, want bool(false)", got)
	}
}

func TestBuiltins2DefinedConstants(t *testing.T) {
	got := runBuiltinScript(t, "--FILE--\n"+
		"<?php $n1 = count(get_defined_constants()); define('USER_CONSTANT','test'); $a = get_defined_constants();"+
		" echo (count($a) == $n1 + 1 && array_key_exists('USER_CONSTANT', $a)) ? 'PASS' : 'FAIL';"+
		" echo '|', gettype(get_defined_constants(true));")
	if got != "PASS|array" {
		t.Fatalf("get_defined_constants = %q, want PASS|array", got)
	}
}

// --- php://input ---

func TestBuiltins2PHPInput(t *testing.T) {
	got := runBuiltinScript(t, "--POST--\na=1&b=ZYX\n--FILE--\n"+
		"<?php var_dump(file_get_contents('php://input'), $_POST['b']);")
	want := "string(9) \"a=1&b=ZYX\"\nstring(3) \"ZYX\""
	if got != want {
		t.Fatalf("php://input = %q, want %q", got, want)
	}
}

func TestBuiltins2PHPInputStream(t *testing.T) {
	got := runBuiltinScript(t, "--POST_RAW--\nContent-Type: application/unknown\nOne line\n--FILE--\n"+
		"<?php $f = fopen('php://input','r'); echo fgets($f);")
	if got != "One line" {
		t.Fatalf("php://input stream = %q, want One line", got)
	}
}

// --- загруженные файлы ---

func TestBuiltins2UploadedFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	RegisterUploadedFile(src)
	dst := filepath.Join(dir, "dst.bin")
	script := fmt.Sprintf("<?php var_dump(is_uploaded_file(%q), is_uploaded_file('/nope'), move_uploaded_file(%q, %q), file_get_contents(%q));",
		src, src, dst, dst)
	got := runBuiltinScript(t, "--FILE--\n"+script)
	want := "bool(true)\nbool(false)\nbool(true)\nstring(7) \"payload\""
	if got != want {
		t.Fatalf("uploaded file = %q, want %q", got, want)
	}
}

// --- getenv из --ENV-- ---

func TestBuiltins2GetenvEnvSection(t *testing.T) {
	got := runBuiltinScript(t, "--ENV--\nFOO=bar\n--FILE--\n<?php echo getenv('FOO'), '-', getenv()['FOO'];")
	if got != "bar-bar" {
		t.Fatalf("getenv = %q, want bar-bar", got)
	}
}

// --- ignore_user_abort ---

func TestBuiltins2IgnoreUserAbort(t *testing.T) {
	ignoreUserAbortState = 0
	got := runBuiltinScript(t, "--FILE--\n"+
		"<?php var_dump(ignore_user_abort(true), ignore_user_abort(), ini_get('ignore_user_abort'));")
	want := "int(0)\nint(1)\nstring(1) \"1\""
	if got != want {
		t.Fatalf("ignore_user_abort = %q, want %q", got, want)
	}
}

// --- serialize_precision ---

func TestBuiltins2SerializePrecision(t *testing.T) {
	SetIni(map[string]string{"serialize_precision": "17"})
	if got := phpFloat(-0.1); got != "-0.10000000000000001" {
		t.Fatalf("phpFloat(-0.1) = %q", got)
	}
	if got := phpFloat(1050000.0); got != "1050000" {
		t.Fatalf("phpFloat(1050000.0) = %q", got)
	}
	SetIni(nil)
	if got := phpFloat(0.1); got != "0.1" {
		t.Fatalf("phpFloat(0.1) default = %q", got)
	}
}

// --- header ---

func TestBuiltins2HeaderMultiLine(t *testing.T) {
	p := parsePhpt("--FILE--\n<?php header(\"X: a\\nb\"); echo 'foo';")
	got, err := runPhptAt(p, "hdr.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Header may not contain more than a single header") || !strings.HasSuffix(got, "foo") {
		t.Fatalf("header warning = %q", got)
	}
	// Трейлинг-пробелы после перевода строки — допустимы.
	got, _ = runPhptAt(parsePhpt("--FILE--\n<?php header(\"X: a\\n \" ); echo 'foo';"), "hdr.phpt")
	if strings.Contains(got, "Warning") || !strings.HasSuffix(got, "foo") {
		t.Fatalf("header false positive = %q", got)
	}
}

// --- утилиты ---

func TestBuiltins2Uniqid(t *testing.T) {
	p := parsePhpt("--FILE--\n<?php echo strlen(uniqid()), ' ', strlen(uniqid('', true)), ' ', strlen(uniqid('ab'));")
	got, err := runPhptAt(p, "u.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.TrimSpace(got) != "13 23 15" {
		t.Fatalf("uniqid lengths = %q, want 13 23 15", got)
	}
}

func TestBuiltins2CanonicalDecimalKey(t *testing.T) {
	cases := map[string]struct {
		v  int64
		ok bool
	}{
		"0":   {0, true},
		"123": {123, true},
		"-5":  {-5, true},
		"09":  {0, false},
		"1.0": {0, false},
		"":    {0, false},
		"12a": {0, false},
		"-":   {0, false},
	}
	for in, want := range cases {
		v, ok := canonicalDecimalKey(in)
		if ok != want.ok || (ok && v != want.v) {
			t.Errorf("canonicalDecimalKey(%q) = (%d,%v), want (%d,%v)", in, v, ok, want.v, want.ok)
		}
	}
}

// Проверяем, что shell-заглушки зарегистрированы и не падают.
func TestBuiltins2ShellStubsCompile(t *testing.T) {
	got := runBuiltinScript(t, "--FILE--\n<?php var_dump(shell_exec('x'), system('x'), passthru('x'));")
	_ = got
}
