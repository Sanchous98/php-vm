package php

import (
	"strings"
	"testing"
)

// runMisc компилирует и исполняет PHP-фрагмент (без открывающего тега).
func runMisc(t *testing.T, src string) string {
	t.Helper()
	out, err := runFile("<?php\n" + src + "\n?>")
	if err != nil {
		t.Fatalf("run error: %v (out=%q)", err, out)
	}
	return out
}

func TestMiscDefinedVarsCompactExtract(t *testing.T) {
	got := runMisc(t, `
$a = 1;
$b = 'x';
$c = 3;
$r = compact('a', ['b']);
echo $r['a'], '-', $r['b'], "\n";
$n = extract(['c' => 30, 'd' => 40]);
echo $n, ':', $c, '-', $d, "\n";
`)
	if got != "1-x\n2:30-40\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMiscTriggerErrorGetLast(t *testing.T) {
	got := runMisc(t, `
var_dump(trigger_error("boom", E_USER_WARNING));
$e = error_get_last();
echo $e['message'], ':', $e['type'], "\n";
error_clear_last();
var_dump(error_get_last());
`)
	if !strings.Contains(got, "Warning: boom") {
		t.Fatalf("missing warning: %q", got)
	}
	if !strings.Contains(got, "boom:512") {
		t.Fatalf("bad last error: %q", got)
	}
	if !strings.Contains(got, "NULL") {
		t.Fatalf("error_clear_last not effective: %q", got)
	}
}

func TestMiscTriggerErrorBadLevel(t *testing.T) {
	got := runMisc(t, `
try { trigger_error("x", 0); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
`)
	want := "trigger_error(): Argument #2 ($error_level) must be one of E_USER_ERROR, E_USER_WARNING, E_USER_NOTICE, or E_USER_DEPRECATED\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMiscAssertOptionsError(t *testing.T) {
	got := runMisc(t, `
try { assert_options(1000); } catch (\ValueError $e) { echo $e->getMessage(); }
`)
	if !strings.Contains(got, "Deprecated: Function assert_options() is deprecated") {
		t.Fatalf("missing deprecation: %q", got)
	}
	if !strings.Contains(got, "assert_options(): Argument #1 ($option) must be an ASSERT_* constant") {
		t.Fatalf("missing ValueError: %q", got)
	}
}

func TestMiscAssertBasic(t *testing.T) {
	got := runMisc(t, `
assert(true);
var_dump(assert(true));
`)
	if got != "bool(true)\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMiscAssertThrows(t *testing.T) {
	out, err := runFile("<?php\nassert(false, \"nope\");\n?>")
	if err == nil {
		t.Fatalf("expected throw, out=%q", out)
	}
	if !strings.Contains(out, "Uncaught AssertionError: nope") {
		t.Fatalf("bad fatal output: %q", out)
	}
}

func TestMiscIconv(t *testing.T) {
	got := runMisc(t, `
echo iconv('UTF-8', 'ISO-8859-1', 'abc'), "\n";
echo iconv_strlen('héllo', 'UTF-8'), "\n";
echo iconv_substr('héllo', 1, 2, 'UTF-8'), "\n";
var_dump(iconv_strpos('héllo', 'l', 0, 'UTF-8'));
`)
	want := "abc\n5\nél\nint(2)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMiscDefinedFunctions(t *testing.T) {
	got := runMisc(t, `
$f = get_defined_functions();
echo is_array($f['internal']) ? 'yes' : 'no', "\n";
echo in_array('strlen', $f['internal']) ? 'has' : 'miss', "\n";
`)
	if got != "yes\nhas\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMiscHostInfo(t *testing.T) {
	got := runMisc(t, `
var_dump(is_string(gethostname()));
var_dump(php_uname('s'));
var_dump(is_string(php_uname()));
var_dump(getmypid() > 0);
`)
	if got != "bool(true)\nstring(5) \"Linux\"\nbool(true)\nbool(true)\n" {
		t.Fatalf("got %q", got)
	}
}
