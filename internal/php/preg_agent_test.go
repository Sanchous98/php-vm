package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPregPHP — дифференциальный прогон: вывод интерпретатора должен побайтово
// совпасть с живым php 8.3. Используется для проверки preg_*.
func runPregPHP(t *testing.T, src string) string {
	t.Helper()
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "case.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, f).Output()
	if werr != nil {
		t.Fatalf("php failed for %q: %v\noutput: %s", src, werr, want)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm failed for %q: %v\noutput: %q", src, err, got)
	}
	if got != string(want) {
		t.Errorf("preg mismatch for %q:\n vm  = %q\n php = %q", src, got, string(want))
	}
	return got
}

// runPregVM прогоняет исходник только в интерпретаторе (без сверки с php).
func runPregVM(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "case.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm failed for %q: %v\noutput: %q", src, err, got)
	}
	return got
}

func TestPregMatchVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_match("/abc/", "xxabcxx"));
var_dump(preg_match("/abc/", "xxabx"));
$m = [];
var_dump(preg_match("/(a)(b)?/", "a", $m), $m);
$m = [];
var_dump(preg_match("/(a)(b)?/", "ab", $m), $m);
$m = [];
var_dump(preg_match("/(?<x>a)(b)/", "ab", $m), $m);
$m = [];
var_dump(preg_match("/a/", "b", $m), $m);
$m = [];
var_dump(preg_match("/a/", "xyzab", $m, 0, 3), $m);
$m = [];
var_dump(preg_match("/a/", "abcabc", $m, 0, 4), $m);
$m = [];
var_dump(preg_match("/(a)/", "xxa", $m, PREG_OFFSET_CAPTURE), $m);
$m = [];
var_dump(preg_match("/(a)(b)?/", "a", $m, PREG_UNMATCHED_AS_NULL), $m);
$m = [];
var_dump(preg_match("/(a)(b)?/", "a", $m, PREG_OFFSET_CAPTURE | PREG_UNMATCHED_AS_NULL), $m);
`)
}

func TestPregFlagsVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_match("#a#i", "A"));
var_dump(preg_match("#^b#m", "a\nb"));
var_dump(preg_match("#a.c#s", "a\nc"));
var_dump(preg_match("#A#", "a"));
var_dump(preg_match("/a  b/x", "ab"));
var_dump(preg_match("/foo # comment\n bar/x", "foobar"));
var_dump(preg_match("/\d+/", "abc123"));
var_dump(preg_match("/[[:digit:]]+/", "abc123"));
var_dump(preg_match('/\p{L}+/u', "привет"));
var_dump(preg_match("/a+?/", "aaa"));
var_dump(preg_match("/(?U)a+/", "aaa"));
var_dump(preg_match("/\Aabc\z/", "abc"));
var_dump(preg_match("/\R/", "a\nb"));
var_dump(preg_match("/\h/", "a b"));
var_dump(preg_match("#\Z#", "abc"));
`)
}

func TestPregMatchAllVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
$m = [];
var_dump(preg_match_all("/(a)(b)/", "abab", $m), $m);
$m = [];
var_dump(preg_match_all("/(?<x>a)(b)?/", "a ab", $m), $m);
$m = [];
var_dump(preg_match_all("/(a)(b)?/", "a ab", $m, PREG_OFFSET_CAPTURE), $m);
$m = [];
var_dump(preg_match_all("/x/", "abc", $m), $m);
$m = [];
var_dump(preg_match_all("/\w+/", "hi there yo", $m), $m);
`)
}

func TestPregReplaceVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
echo preg_replace("/(a)(b)/", '[\2\1]', "ab ab"), "\n";
echo preg_replace("/(a)(b)/", '[$2$1]', "ab"), "\n";
echo preg_replace('/(?<x>a)/', '<${x}>', 'a'), "\n";
echo preg_replace('/(?<x>a)/', '<$x>', 'a'), "\n";
echo preg_replace("/(a)/", '\0!', "a"), "\n";
echo preg_replace("/a/", "X", "aaa", 2, $c), " c=$c\n";
echo preg_replace("/a/", "X", "aaa", 0, $c), " c=$c\n";
echo preg_replace(["/(a)/", "/(b)/"], ["A", "B"], "ab", -1, $c), " c=$c\n";
echo preg_replace(["/a/", "/b/"], "X", "ab", -1, $c), " c=$c\n";
try { preg_replace("/(a)/", ["X", "Y"], ["a", "a"], -1, $c); }
catch (TypeError $e) { echo $e->getMessage(), "\n"; }
echo preg_replace("/z/", "X", "aaa", -1, $c), " c=$c\n";
echo preg_replace('/a/', '$nope', 'a'), "\n";
echo preg_replace('/a/', '\q', 'a'), "\n";
`)
}

func TestPregReplaceCallbackVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
echo preg_replace_callback("/(\w+)/", fn($m) => strtoupper($m[1]), "hello world"), "\n";
$c = 0;
echo preg_replace_callback("/a/", fn($m) => $m[0] . "-", "a a a", -1, $c), " c=$c\n";
echo preg_replace_callback("/a/", fn($m) => "Z", "aaa", 2, $c), " c=$c\n";
echo preg_replace_callback(["/(a)/", "/(b)/"], fn($m) => "X", "ab", -1, $c), " c=$c\n";
echo preg_replace_callback("/(a)(b)?/", fn($m) => "[" . count($m) . ":" . $m[0] . "]", "a ab"), "\n";
echo preg_replace_callback("/z/", fn($m) => "X", "abc", -1, $c), " c=$c\n";
`)
}

func TestPregSplitVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_split("/,/", "a,b,c"));
var_dump(preg_split("/,/", "a,b,,c", -1, PREG_SPLIT_NO_EMPTY));
var_dump(preg_split("/(,)/", "a,b,c", -1, PREG_SPLIT_DELIM_CAPTURE));
var_dump(preg_split("/(,)|(;)/", "a,b", -1, PREG_SPLIT_DELIM_CAPTURE));
var_dump(preg_split("/,/", "a,b,c", 2));
var_dump(preg_split("/,/", "a,b,c", 1));
var_dump(preg_split("/,/", "a,b,c", 0));
var_dump(preg_split("/,/", "a,b,c", -1, PREG_SPLIT_OFFSET_CAPTURE));
var_dump(preg_split("//", "abc"));
var_dump(preg_split("/\s+/", "a b  c", -1, PREG_SPLIT_NO_EMPTY));
var_dump(preg_split("/x/", "abc"));
`)
}

func TestPregSplitLimitDelimVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_split('/(,)/', 'a,b,c,d', 2, PREG_SPLIT_DELIM_CAPTURE));
var_dump(preg_split('/(,)/', 'a,b,c,d', 3, PREG_SPLIT_DELIM_CAPTURE));
var_dump(preg_split('/,/', 'a,b,c,d', 3));
var_dump(preg_split('/(,)/', 'a,b,c', -1, PREG_SPLIT_DELIM_CAPTURE | PREG_SPLIT_OFFSET_CAPTURE));
`)
}

func TestPregAnchorsAndOffsetsVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_match('/^abc/', 'abc'));
var_dump(preg_match('/abc/A', 'xabc'));
var_dump(preg_match('/abc/A', 'abcx'));
var_dump(preg_match('/^b/m', "a\nb"));
var_dump(preg_match('/abc/', 'xabcxx', $m, 0, 1), $m);
var_dump(preg_match('/a/', 'abc', $m, 0, -1), $m);
var_dump(preg_match('/a/', 'abc', $m, 0, -3), $m);
var_dump(preg_match('/c/', 'abc', $m, 0, -1), $m);
var_dump(preg_match('/(b)(?<n>c)/', 'abc', $m, PREG_OFFSET_CAPTURE), $m);
`)
}

func TestPregReplaceNamedLiteralVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
echo preg_replace('/(?<x>a)/', '${x}', 'a'), "\n";
echo preg_replace('/(?<x>a)/', '$x', 'a'), "\n";
echo preg_replace('/(a)/', '${1}', 'a'), "\n";
echo preg_replace('/(a)/', '${0}', 'a'), "\n";
echo preg_replace('/(a)/', '$0', 'a'), "\n";
echo preg_replace('/(a)/', '\g<x>', 'a'), "\n";
`)
}

func TestPregGrepVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_grep("/^a/", ["x", "ab", "ac", "b"]));
var_dump(preg_grep("/^a/", ["x", "ab", "ac", "b"], PREG_GREP_INVERT));
var_dump(preg_grep("/\d/", ["a1", "bb", "c3"]));
var_dump(preg_grep("#A#i", ["a", "B"]));
`)
}

func TestPregQuoteVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
echo preg_quote(".\+*?[^]$(){}=!<>|:-#/ "), "\n";
echo preg_quote(".\+*?[^]$(){}=!<>|:-#/ ", "/"), "\n";
echo preg_quote("a-b"), "\n";
echo preg_quote("a/b"), "\n";
echo preg_quote("a/b", "/"), "\n";
echo preg_quote("a#b", "#"), "\n";
echo preg_quote("hello world"), "\n";
`)
}

func TestPregLastErrorVsPHP(t *testing.T) {
	runPregPHP(t, `<?php
var_dump(preg_last_error(), preg_last_error_msg());
$m = [];
preg_match("/(a)/", "a", $m);
var_dump(preg_last_error(), preg_last_error_msg());
preg_match("/(a/", "a", $m);
var_dump(preg_last_error(), preg_last_error_msg());
preg_match("/a/", "abc", $m, 0, 10);
var_dump(preg_last_error(), preg_last_error_msg());
preg_match('/\d/u', chr(255), $m);
var_dump(preg_last_error(), preg_last_error_msg());
`)
}

// TestPregUnsupportedRE2 — конструкции PCRE, неподдерживаемые RE2: функции
// возвращают false, ошибка фиксируется, паники нет. Сверки с php тут нет
// (php такие паттерны исполняет), поэтому проверяем только VM.
func TestPregUnsupportedRE2(t *testing.T) {
	src := `<?php
$m = [];
var_dump(preg_match("/(?=a)a/", "a", $m));
echo preg_last_error(), " ", preg_last_error_msg(), "\n";
var_dump(preg_match("/(a)\1/", "aa", $m));
echo preg_last_error(), " ", preg_last_error_msg(), "\n";
var_dump(preg_match("/(?>a)/", "a", $m));
echo preg_last_error(), " ", preg_last_error_msg(), "\n";
var_dump(preg_match("/a*+/", "aaa", $m));
echo preg_last_error(), " ", preg_last_error_msg(), "\n";
var_dump(preg_split("/(?<=a)b/", "ab"));
echo preg_last_error(), " ", preg_last_error_msg(), "\n";
`
	got := runPregVM(t, src)
	for _, want := range []string{
		"bool(false)",
		"1 Internal error",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unsupported RE2: output %q missing %q", got, want)
		}
	}
}

// TestPregNoPanicFuzz — набор «плохих» паттернов не должен паниковать.
func TestPregNoPanicFuzz(t *testing.T) {
	src := `<?php
$pats = ["", "/", "//", "/(/", "/)/", "/(a/", "#a", "abc", "/[a/", "/(?#x)/",
         "/a/iZ", "/a/", "{}", "<a>", "[a-z]", "/\p{Bogus}/u"];
foreach ($pats as $p) {
    $m = [];
    $r = @preg_match($p, "subject", $m);
    echo gettype($r), " ", preg_last_error(), "\n";
}
`
	_ = runPregVM(t, src)
}
