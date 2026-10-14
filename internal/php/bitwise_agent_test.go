package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runTestsNorm повторяет нормализацию run-tests.php: php-cli пишет диагно-
// стики в форме "PHP Deprecated:  msg", а EXPECT-файлы содержат "\nDeprecated: msg".
var phpDiagRe = regexp.MustCompile(`(?m)^PHP (Warning|Notice|Deprecated|Fatal error|Parse error):[ \t]+`)

func runTestsNorm(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return phpDiagRe.ReplaceAllString(s, "\n$1: ")
}

// runBitwisePHP — дифференциальный прогон: вывод интерпретатора должен
// побайтово совпасть с живым php 8.3.
func runBitwisePHP(t *testing.T, src string) string {
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
	want, werr := exec.Command(phppath, "-d", "display_errors=1", "-d", "error_reporting=E_ALL", f).Output()
	if werr != nil {
		t.Fatalf("php failed for %q: %v\noutput: %s", src, werr, want)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm failed for %q: %v\noutput: %q", src, err, got)
	}
	if wantNorm := runTestsNorm(string(want)); got != wantNorm {
		t.Errorf("bitwise mismatch for %q:\n vm  = %q\n php = %q", src, got, wantNorm)
	}
	return got
}

// TestBitwiseIntVsPHP — целочисленные &,|,^,~,<<,>> и смешанные типы.
func TestBitwiseIntVsPHP(t *testing.T) {
	src := `<?php
var_dump(5 & 3);
var_dump(5 | 3);
var_dump(5 ^ 3);
var_dump(~5);
var_dump(~0);
var_dump(~-1);
var_dump(~PHP_INT_MIN);
var_dump(1 << 4);
var_dump(256 >> 2);
var_dump(-8 >> 1);
var_dump(-8 << 1);
var_dump(1 << 63);
var_dump(1 << 64);
var_dump(1 << 100);
var_dump(-1 >> 64);
var_dump(-8 >> 64);
var_dump(-100 >> 100);
var_dump(8 >> 63);
var_dump(1.9 & 3);
var_dump(1.5 & 2.5);
var_dump(1.5 | 2.5);
var_dump(1.5 ^ 2.5);
var_dump(~1.9);
var_dump(true & 3);
var_dump(true | 3);
var_dump(true ^ 3);
var_dump(null & 3);
var_dump(null | 3);
var_dump(null ^ 3);
var_dump(true << 2);
var_dump(null << 2);
var_dump(true >> 2);
var_dump(null >> 2);
var_dump(3.9 << 1);
var_dump(1.9 >> 1);
`
	runBitwisePHP(t, src)
}

// TestBitwiseStringVsPHP — строковые &,|,^,~ (включая разную длину).
func TestBitwiseStringVsPHP(t *testing.T) {
	src := `<?php
var_dump("A" & "B");
var_dump("A" | " ");
var_dump("A" ^ "B");
var_dump("AB" & "B");
var_dump("ABCD" & "B");
var_dump("ABCD" | "B");
var_dump("AB" | "CDEF");
var_dump("ABCD" ^ "B");
var_dump("A" & "");
var_dump("" & "A");
var_dump("A" | "BC" | "DEF");
var_dump(~"");
var_dump(~"A");
var_dump(~"AB");
var_dump(~"ABC");
var_dump("5" & "3");
var_dump("5" | "3");
var_dump("5" ^ "3");
`
	runBitwisePHP(t, src)
}

// TestBitwiseNumericStringVsPHP — строка + число: numeric-строки приводятся к int.
func TestBitwiseNumericStringVsPHP(t *testing.T) {
	src := `<?php
var_dump("5" & 3);
var_dump(3 & "5");
var_dump("5.9" & 3);
var_dump(" 5" & 3);
var_dump("5 " & 3);
var_dump("1e2" & 3);
var_dump("1.9" & 3);
var_dump("1" & true);
var_dump(true & "1");
var_dump("1" & null);
var_dump("1" & 1.9);
var_dump("1e20" & 3);
var_dump("9223372036854775808" & 3);
var_dump("12" << 2);
`
	runBitwisePHP(t, src)
}

// TestBitwiseCompoundAssignVsPHP — составные присваивания &=,|=,^=,<<=,>>=.
func TestBitwiseCompoundAssignVsPHP(t *testing.T) {
	src := `<?php
$a = 5; $a &= 3; var_dump($a);
$a = 5; $a |= 3; var_dump($a);
$a = 5; $a ^= 3; var_dump($a);
$a = 1; $a <<= 4; var_dump($a);
$a = 256; $a >>= 2; var_dump($a);
$s = "A"; $s &= "B"; var_dump($s);
$s = "A"; $s |= " "; var_dump($s);
$s = "A"; $s ^= "B"; var_dump($s);
$s = "AB"; $s |= "CDEF"; var_dump($s);
$a = 5; var_dump($a &= 3);
$b = 1; var_dump($b <<= 3);
$arr = [5]; $arr[0] &= 3; var_dump($arr[0]);
function f(&$x) { $x |= 8; }
$c = 1; f($c); var_dump($c);
`
	runBitwisePHP(t, src)
}

// TestBitwiseErrorsVsPHP — TypeError/ArithmeticError и их сообщения.
func TestBitwiseErrorsVsPHP(t *testing.T) {
	src := `<?php
try { $x = "A" & 3; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = 3 & "A"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = "A" | 0; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = "A" ^ 0; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = "A" << 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = 1 << "A"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = ~true; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = ~false; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = ~null; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = ~[]; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = [] & 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = 1 & []; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = [] << 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $x = 1 << -1; } catch (ArithmeticError $e) { echo $e->getMessage(), "\n"; }
try { $x = 8 >> -1; } catch (ArithmeticError $e) { echo $e->getMessage(), "\n"; }
`
	runBitwisePHP(t, src)
}

// TestBitwisePrecedenceVsPHP — приоритеты битовых операторов.
func TestBitwisePrecedenceVsPHP(t *testing.T) {
	src := `<?php
var_dump(1 | 2 & 3);
var_dump((1 | 2) & 3);
var_dump(1 ^ 3 & 1);
var_dump(1 << 2 + 1);
var_dump(2 + 1 << 2);
var_dump(1 & 2 == 2);
var_dump((1 & 2) == 2);
var_dump(1 | 2 ^ 3);
var_dump(~1 & 3);
var_dump(-1 >> 1 & 3);
`
	runBitwisePHP(t, src)
}
