package php

import (
	"strings"
	"testing"
)

// Регрессии агента волны e2_io (HTTP/streams/filters/общее). Ожидания
// сверены с php 8.3; тесты без внешних серверов.

func e2check(t *testing.T, name, src, want string) {
	t.Helper()
	ResetResourceState()
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil {
		t.Fatalf("%s: run: %v", name, err)
	}
	if got != want {
		t.Errorf("%s:\n got=%q\nwant=%q", name, got, want)
	}
}

// Ресурс proc_open (с OnDrop) должен переживать запись в свойство/массив:
// финализатор закрывал пайпы, пока ресурс был достижим (http-воркеры).
func TestE2IOResourceHold(t *testing.T) {
	e2check(t, "prop", `<?php
class T { public $w = []; }
$t = new T;
$t->w['s'] = proc_open("cat", [['pipe','r'],['pipe','w'],STDERR], $pipes);
var_dump(get_resource_type($t->w['s']));
fwrite($pipes[0], "x\n");
echo fgets($pipes[1]);
fclose($pipes[0]); fclose($pipes[1]); proc_close($t->w['s']);
`, "string(7) \"process\"\nx\n")
	e2check(t, "arr", `<?php
$a = [];
$a['p'] = proc_open("cat", [['pipe','r'],['pipe','w'],STDERR], $pipes);
var_dump(get_resource_type($a['p']));
fwrite($pipes[0], "y\n");
echo fgets($pipes[1]);
fclose($pipes[0]); fclose($pipes[1]); proc_close($a['p']);
`, "string(7) \"process\"\ny\n")
}

// static-переменная и локальная с одним именем: до объявления — локальная,
// после — статическая ячейка.
func TestE2IOStaticBind(t *testing.T) {
	e2check(t, "static_bind", `<?php
function f() { $a = 0; echo $a, "\n"; static $a = 10; echo $a, "\n"; $a++; }
f(); f(); f();
`, "0\n10\n0\n11\n0\n12\n")
}

// eval внутри функции видит и меняет её локальные переменные.
func TestE2IOEvalScope(t *testing.T) {
	e2check(t, "eval_pop", `<?php
function f() { $a = ['x', 'y']; foreach ($a as $v) { eval('array_pop($a);'); } var_dump($a); }
f();
`, "array(0) {\n}\n")
	e2check(t, "eval_set", `<?php
function f() { $a = 1; eval('$a = 5;'); echo $a, "\n"; }
f();
`, "5\n")
}

// URL-Rewriter: output_add_rewrite_var переписывает ссылки/формы.
func TestE2IOUrlRewriter(t *testing.T) {
	e2check(t, "rewrite", `<?php
ini_set('url_rewriter.hosts', 'php.net');
ini_set('url_rewriter.tags', 'a=href,form=');
ob_start();
output_add_rewrite_var('a', 'b');
echo '<a href="">e</a>', "\n";
echo '<a href="?q=1">q</a>', "\n";
echo '<a href="//other.com/x">o</a>', "\n";
echo '<form method="get">F</form>', "\n";
ob_end_flush();
`, "<a href=\"?a=b\">e</a>\n<a href=\"?q=1&a=b\">q</a>\n<a href=\"//other.com/x\">o</a>\n<form method=\"get\"><input type=\"hidden\" name=\"a\" value=\"b\" />F</form>\n")
}

// Конец потока php://memory отличается от php://temp: filter() при EOF
// вызывается отдельно (gh20370) либо вместе с данными (bug79984).
func TestE2IOFilterEofCalls(t *testing.T) {
	src := `<?php
class F extends php_user_filter {
    public function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) { $consumed += $b->datalen; stream_bucket_append($out, $b); }
        echo $closing ? "C" : "P";
        return PSFS_PASS_ON;
    }
}
stream_filter_register('f','F');
$fp = fopen("php://memory", "w"); stream_filter_append($fp, "f", STREAM_FILTER_READ);
fwrite($fp, "d"); rewind($fp); fread($fp, 1024); fclose($fp); echo "\n";
$tp = fopen("php://temp", "r+b"); fwrite($tp, "d"); fseek($tp, 0, SEEK_SET);
stream_filter_append($tp, "f", STREAM_FILTER_READ); stream_get_contents($tp); fclose($tp); echo "\n";
`
	e2check(t, "filter_eof", src, "PC\nC\n")
}

// QP-decode: мягкий перенос "=\r" отбрасывается, невалидная пара — Warning
// и пустой результат.
func TestE2IOQuotedPrintable(t *testing.T) {
	e2check(t, "qp_soft", `<?php
$s = str_repeat('A', 10)."X=\r";
var_dump(strlen(file_get_contents("php://filter/convert.quoted-printable-decode/resource=data:," . urlencode($s))));
`, "int(11)\n")
	e2check(t, "qp_invalid", `<?php
var_dump(@file_get_contents("php://filter/convert.quoted-printable-decode/resource=data:,abc%3DXZ"));
`, "string(0) \"\"\n")
}

// zlib.deflate для коротких данных кодируется фиксированным Huffman-блоком
// (байты совпадают с zlib, bug81294).
func TestE2IOZlibDeflateBytes(t *testing.T) {
	e2check(t, "deflate", `<?php
$f=fopen("/tmp/e2io_deflate.bin","wb+");
$flt=stream_filter_append($f,"zlib.deflate",STREAM_FILTER_WRITE);
fwrite($f,"test");
stream_filter_remove($flt);
fclose($f);
echo bin2hex(file_get_contents("/tmp/e2io_deflate.bin")),"\n";
unlink("/tmp/e2io_deflate.bin");
`, "2b492d2e0100\n")
}

// Фатальная диагностика (E_USER_ERROR) останавливает скрипт, shutdown-функции
// выполняются (gh8409); html_errors добавляет docref-ссылку (bug35176).
func TestE2IOFatalBailout(t *testing.T) {
	e2check(t, "bailout", `<?php
register_shutdown_function(function (): void { echo "OK: shutdown\n"; });
set_error_handler(function (int $errno, string $errstring): never { trigger_error($errstring, E_USER_ERROR); });
trigger_error("boom", E_USER_WARNING);
echo "ERROR: not visible\n";
`, "\nFatal error: boom in diff.php on line 3\nOK: shutdown\n")
	// docref-ссылка при html_errors (как bug35176): INI через --INI--.
	p := PhpT{File: "<?php require_once('nonexistent.php');", HasIni: true,
		Ini: "html_errors=1\ndocref_root=\"/\"\nerror_reporting=4095"}
	ResetResourceState()
	got, _ := runPhptAt(p, "e2io_docref.phpt")
	if !strings.Contains(got, "[<a href='/function.require-once.html'>function.require-once.html</a>]") {
		t.Errorf("docref: нет ссылки в %q", got)
	}
	_ = strings.Contains
}

// eval/include: __FILE__ подключаемого файла; include-кадр в трейсе.
func TestE2IOIncludeFrame(t *testing.T) {
	e2check(t, "include_trace", `<?php
class H { static function error() { debug_print_backtrace(); } }
set_error_handler(array('H', 'error'));
include('nonexistent.php');
`, "#0 diff.php(4): H::error(2, 'include(nonexis...', 'diff.php', 4)\n#1 diff.php(4): include()\n#0 diff.php(4): H::error(2, 'include(): Fail...', 'diff.php', 4)\n#1 diff.php(4): include()\n")
}
