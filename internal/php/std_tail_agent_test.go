package php

import (
	"os"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// Файл регрессионных тестов std-tail-агента: proc_get_status/proc_terminate,
// HTTP-обёртка, lookbehind в preg_*, system(), HTML5-сущности, парсинг cookie,
// time_nanosleep, getimagesize через обёртки.

func mustRunTail(t *testing.T, src string) string {
	t.Helper()
	SetSAPIEmulation("cli", true)
	return mustRunTailSAPI(t, src)
}

func mustRunTailSAPI(t *testing.T, src string) string {
	t.Helper()
	SetIniString("")
	ResetResourceState()
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v\nout=%s", err, out)
	}
	return out
}

func eqTail(t *testing.T, src, want string) {
	t.Helper()
	got := mustRunTail(t, src)
	if got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

// TestStdTailPregLookbehind — (?<!\r) в начале шаблона (RE2 не умеет
// lookaround): preg_match_all считает только \n без предшествующего \r.
func TestStdTailPregLookbehind(t *testing.T) {
	eqTail(t, `<?php
$s = "a\r\nb\nc\r\n";
var_dump(preg_match_all('/(?<!\r)\n/', $s));
var_dump(preg_match('/(?<!\r)\n/', $s));
var_dump(preg_match_all('/\n/', $s));
var_dump(preg_match_all('/(?<!x)a/', "xaa"));
`, `int(1)
int(1)
int(3)
int(1)
`)
}

// TestStdTailProcStatus — семантика proc_get_status/proc_terminate/proc_close
// (waitpid_cached: сигнальный статус не кэшируется, WIFEXITED кэшируется).
func TestStdTailProcStatus(t *testing.T) {
	eqTail(t, `<?php
$h = proc_open(["/bin/sh","-c","exit 7"], [1=>["pipe","w"]], $p);
usleep(200000);
$st = proc_get_status($h);
var_dump($st["running"], $st["exitcode"], $st["cached"], $st["command"]);
$st = proc_get_status($h);
var_dump($st["running"], $st["exitcode"], $st["cached"]);
var_dump(proc_close($h));
$h2 = proc_open("sleep 30", [1=>["pipe","w"]], $p2);
var_dump(proc_terminate($h2));
usleep(200000);
$st = proc_get_status($h2);
var_dump($st["running"], $st["signaled"], $st["termsig"], $st["exitcode"]);
var_dump(proc_close($h2));
`, `bool(false)
int(7)
bool(true)
string(7) "/bin/sh"
bool(false)
int(7)
bool(true)
int(7)
bool(true)
bool(false)
bool(true)
int(15)
int(-1)
int(-1)
`)
}

// TestStdTailShutdownKeepsProcess — аргумент shutdown-функции удерживает
// ресурс процесса (не финализируется при потере регистра).
func TestStdTailShutdownKeepsProcess(t *testing.T) {
	eqTail(t, `<?php
function start() {
    $h = proc_open("sleep 30", [1=>["pipe","w"]], $p);
    register_shutdown_function(function($handle) {
        echo get_resource_type($handle), "\n";
        proc_terminate($handle);
    }, $h);
}
start();
echo "done\n";
`, `done
process
`)
}

// TestStdTailSystem — system() печатает вывод команды и возвращает последнюю
// строку; результат доступен через ob_*.
func TestStdTailSystem(t *testing.T) {
	eqTail(t, `<?php
ob_start();
$r = system("printf 'a\\nb\\n'", $code);
var_dump($r, $code, ob_get_clean());
`, `string(1) "b"
int(0)
string(4) "a
b
"
`)
}

// TestStdTailHTML5Entities — HTML5-таблица без отката на HTML4 (U+2329/U+232A)
// и многокодовые сущности (&nvlt;).
func TestStdTailHTML5Entities(t *testing.T) {
	eqTail(t, `<?php
function u8($k) {
    if ($k < 0x80) return pack('C', $k);
    if ($k < 0x800) return pack('C2', 0xc0 | ($k >> 6), 0x80 | ($k & 0x3f));
    if ($k < 0x10000) return pack('C3', 0xe0 | ($k >> 12), 0x80 | (($k >> 6) & 0x3f), 0x80 | ($k & 0x3f));
    return pack('C4', 0xf0 | ($k >> 18), 0x80 | (($k >> 12) & 0x3f), 0x80 | (($k >> 6) & 0x3f), 0x80 | ($k & 0x3f));
}
var_dump(htmlentities(u8(0x2329), ENT_QUOTES | ENT_HTML5, 'UTF-8'));
var_dump(htmlentities(u8(0x27E8), ENT_QUOTES | ENT_HTML5, 'UTF-8'));
var_dump(htmlentities(u8(0x3C) . u8(0x20D2), ENT_QUOTES | ENT_HTML5, 'UTF-8'));
var_dump(htmlentities(u8(0x2329), ENT_QUOTES | ENT_HTML401, 'UTF-8'));
`, `string(3) "〈"
string(8) "&langle;"
string(6) "&nvlt;"
string(6) "&lang;"
`)
}

// TestStdTailCookieMangle — манглинг имён cookie и отбрасывание имён, которые
// после мангла притворяются __Host-/__Secure- (GHSA-wpj3-hf5j-x4v4).
func TestStdTailCookieMangle(t *testing.T) {
	arr := vm.NewArray()
	parseCookiesInto(arr, "..Host-test=ignore; __Host-test=correct; . Secure-test=ignore; . Elephpant=Awesome;")
	// Имена с пустой базой ([...) отбрасываются, как var_len==0 в php-src.
	parseCookiesInto(arr, "[.Host-test[]=ignore; [_Host-test[]=ignore;")
	if _, ok := arr.Get(vm.MakeStrKey("")); ok {
		t.Fatalf("пустая база не отброшена")
	}
	var b strings.Builder
	for _, k := range []string{"__Host-test", "__Elephpant", "__Secure-test"} {
		v, ok := arr.Get(vm.MakeStrKey(k))
		if ok {
			b.WriteString(k + "=" + v.ToString() + ";")
		}
	}
	if b.String() != "__Host-test=correct;__Elephpant=Awesome;" {
		t.Fatalf("cookie parse: %q", b.String())
	}
}

// TestStdTailTimeNanosleep — валидация параметров time_nanosleep().
func TestStdTailTimeNanosleep(t *testing.T) {
	eqTail(t, `<?php
try { time_nanosleep(-1, 0); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
try { time_nanosleep(0, -1); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
var_dump(time_nanosleep(0, 1000));
`, `time_nanosleep(): Argument #1 ($seconds) must be greater than or equal to 0
time_nanosleep(): Argument #2 ($nanoseconds) must be greater than or equal to 0
bool(true)
`)
}

// TestStdTailGetimagesizeWrapper — getimagesize() читает данные через
// пользовательскую обёртку и php://filter (APP1 целиком).
func TestStdTailGetimagesizeWrapper(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/img.jpg"
	src := `<?php
class W {
    public $context; public $h;
    function stream_open($f, $m) { $this->h = fopen(str_replace('wtest://', '', $f), $m); return true; }
    function stream_read($n) { return fread($this->h, $n); }
    function stream_eof() { return feof($this->h); }
    function stream_seek($o, $w) { return fseek($this->h, $o, $w) === 0; }
    function stream_tell() { return ftell($this->h); }
    function stream_close() { fclose($this->h); }
}
stream_register_wrapper('wtest', 'W');
$info = null;
var_dump(getimagesize('wtest://` + file + `', $info));
var_dump(strlen($info['APP1']));
`
	data := "\xFF\xD8" + "\xFF\xE1" + "\x00\x20" + strings.Repeat("A", 30) +
		"\xFF\xC0\x00\x0B\x08\x00\x01\x00\x01\x01\x11\x00" + "\xFF\xD9"
	if err := os.WriteFile(file, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	got := mustRunTail(t, src)
	if !strings.Contains(got, "int(30)") {
		t.Fatalf("APP1 не прочитан: %q", got)
	}
}

// TestStdTailSAPISwitch — phpinfo()/php_sapi_name() в CLI- и CGI-режиме.
func TestStdTailSAPISwitch(t *testing.T) {
	SetSAPIEmulation("cli", true)
	out := mustRunTail(t, `<?php
echo php_sapi_name(), "\n";
`)
	if out != "cli\n" {
		t.Fatalf("cli: %q", out)
	}
	SetSAPIEmulation("cgi-fcgi", false)
	defer SetSAPIEmulation("cli", true)
	out = mustRunTailSAPI(t, `<?php
echo php_sapi_name(), "\n";
ob_start(); phpcredits(0); $c = ob_get_clean();
var_dump(strpos($c, "<h1>PHP Credits</h1>") !== false);
`)
	if !strings.HasPrefix(out, "cgi-fcgi\n") || !strings.Contains(out, "bool(true)") {
		t.Fatalf("cgi: %q", out)
	}
}
