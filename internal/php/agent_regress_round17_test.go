package php

import (
	"os"
	"strings"
	"testing"
)

// --- r17: завершение запроса (shutdown-порядок, возврат main) ---

// TestR17ShutdownOrderAgent: Zend-порядок завершения запроса —
// значение return освобождается первым (деструктор возвращённого объекта),
// затем shutdown-функции, затем деструкторы глобалов.
func TestR17ShutdownOrderAgent(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "return value dtor before shutdown fn",
			src: `<?php
class A { function __destruct(){ echo "dtor; "; } }
register_shutdown_function(function(){ echo "shutdown; "; });
return new A;`,
			want: []string{"dtor; ", "shutdown; "},
		},
		{
			name: "global dtor after shutdown fn",
			src: `<?php
class A { function __destruct(){ echo "dtor; "; } }
register_shutdown_function(function(){ echo "shutdown; "; });
$a = new A;`,
			want: []string{"shutdown; ", "dtor; "},
		},
		{
			name: "ob level in shutdown fn",
			src: `<?php
ob_start();
echo "A";
register_shutdown_function(function(){ echo "level=", ob_get_level(), ";"; });
echo "B";`,
			want: []string{"ABlevel=1;"},
		},
		{
			name: "exit in shutdown fn stops later fns, dtors run",
			src: `<?php
class T1 { function shutdown(){ exit("s1"); } }
class T2 extends T1 { function __destruct(){ echo "d2"; } }
register_shutdown_function([new T1, "shutdown"]);
$keep = new T2;`,
			want: []string{"s1", "d2"},
		},
	}
	for _, c := range cases {
		got := runSrcAgent(t, c.src)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: got %q, want contains %q", c.name, got, w)
			}
		}
	}
	if got := runSrcAgent(t, `<?php
class A { function __destruct(){ echo "dtor; "; } }
register_shutdown_function(function(){ echo "shutdown; "; });
$a = new A;`); strings.Count(got, "dtor") != 1 || strings.Count(got, "shutdown") != 1 {
		t.Errorf("global dtor count: %q", got)
	}
}

// TestR17ReturnValueDtorSiteAgent: деструктор объекта-значения return получает
// локацию return-инструкции (файл/строка), а не "[internal function]"
// (fibers/no-switch-dtor-resume).
func TestR17ReturnValueDtorSiteAgent(t *testing.T) {
	src := `<?php
$fiber = new Fiber(function () { Fiber::suspend(); });
$fiber->start();
return new class ($fiber) {
    private $fiber;
    public function __construct(Fiber $fiber) { $this->fiber = $fiber; }
    public function __destruct() { $this->fiber->resume(1); }
};`
	got := runSrcAgent(t, src)
	if !strings.Contains(got, "Cannot switch fibers in current execution context") {
		t.Fatalf("no FiberError: %q", got)
	}
	if !strings.Contains(got, "): class@anonymous->__destruct()") ||
		strings.Contains(got, "[internal function]: class@anonymous->__destruct()") {
		t.Fatalf("destructor frame has no site line: %q", got)
	}
	if strings.Contains(got, "#1 [internal function]: class@anonymous->__destruct()") {
		t.Fatalf("destructor frame marked internal: %q", got)
	}
}

// TestR17DtorNoSwitchAgent: переключение файберов запрещено в ЛЮБОМ
// деструкторе (в т.ч. при unset в main), объект при этом живёт до shutdown.
func TestR17DtorNoSwitchAgent(t *testing.T) {
	src := `<?php
$f = new Fiber(function(){ echo "run; "; Fiber::suspend(); });
$f->start();
class A { public $f; function __destruct(){ echo "dtor; "; try { $this->f->resume(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "; "; } } }
$a = new A; $a->f = $f;
unset($a);
echo "after; ";
var_dump($f->isSuspended());`
	got := runSrcAgent(t, src)
	want := "run; dtor; FiberError: Cannot switch fibers in current execution context; after; bool(true)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// --- r17: атрибуты ---

// TestR17AttrStrictUseSiteAgent: strict_types=1 включаемого файла действует
// на newInstance() атрибута, объявленного в этом файле (030_strict_types).
func TestR17AttrStrictUseSiteAgent(t *testing.T) {
	p := parsePhpt("--FILE--\n" + `<?php
#[Attribute]
class MyAttribute { public function __construct(public int $value) {} }
#[MyAttribute("42")]
class TestWeak {}
require 'r17_strict.inc';
var_dump((new ReflectionClass(TestWeak::class))->getAttributes()[0]->newInstance());
var_dump((new ReflectionClass(TestStrict::class))->getAttributes()[0]->newInstance());
`)
	dir := t.TempDir()
	abs := dir + "/r17_strict.inc"
	if err := os.WriteFile(abs, []byte("<?php\ndeclare(strict_types=1);\n\n#[MyAttribute(\"42\")]\nclass TestStrict {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p.File = strings.ReplaceAll(p.File, "require 'r17_strict.inc';", "require '"+abs+"';")
	got, err := runFileAt(p.File, dir+"/main.php")
	if err != nil && throwableOf(err) == nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "MyAttribute::__construct(): Argument #1 ($value) must be of type int, string given") {
		t.Fatalf("no strict TypeError: %q", got)
	}
	if !strings.Contains(got, "r17_strict.inc on line 4") {
		t.Fatalf("use-site file/line wrong: %q", got)
	}
	if strings.Count(got, `int(42)`) != 1 {
		t.Fatalf("weak use should succeed once: %q", got)
	}
}

// --- r17: класс-значение ::class ---

// TestR17ClassOfExprAgent: `$obj::class`, литерал-строка и TypeError для
// строкового значения (PHP 8.3).
func TestR17ClassOfExprAgent(t *testing.T) {
	got := runSrcAgent(t, `<?php
class Foo {}
$e = new Foo;
echo $e::class, "|", "Foo"::class, "|", ("F"."oo")::class, "|";
const C = "Foo";
echo C::class, "|";
$c = "Foo";
try { echo $c::class; } catch (TypeError $e) { echo "TE"; }`)
	want := "Foo|Foo|Foo|C|TE"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// --- r17: disable_classes ---

// TestR17DisableClassesAgent: предупреждение при new, объект создаётся и
// конструктор выполняется; подкласс наследует запрет (errmsg_021).
func TestR17DisableClassesAgent(t *testing.T) {
	p := parsePhpt("--INI--\ndisable_classes=stdclass\n--FILE--\n" + `<?php
class test extends stdclass { public $x = 1; }
$t = new test;
echo "Done\n";`)
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Warning: test() has been disabled for security reasons") {
		t.Fatalf("no warning: %q", got)
	}
	if !strings.HasSuffix(got, "Done\n") {
		t.Fatalf("object creation failed: %q", got)
	}
}

// --- r17: stream-фильтры ---

// TestR17FilterRegistryIsolatedAgent: реестр пользовательских фильтров не
// переносится между прогонами (read.phpt после bug35916).
func TestR17FilterRegistryIsolatedAgent(t *testing.T) {
	src := `<?php
class f1 extends php_user_filter { function filter($in, $out, &$consumed, $closing): int { return PSFS_PASS_ON; } }
var_dump(stream_filter_register("dup.filter", "f1"));
var_dump(stream_filter_register("dup.filter", "f1"));`
	got := runSrcAgent(t, src)
	if got != "bool(true)\nbool(false)\n" {
		t.Fatalf("got %q", got)
	}
	// Новый «запрос»: регистрация снова возможна.
	got = runSrcAgent(t, src)
	if got != "bool(true)\nbool(false)\n" {
		t.Fatalf("after reset got %q", got)
	}
}

// TestR17PSFSConstantsAgent: значения констант PSFS_* как в PHP.
func TestR17PSFSConstantsAgent(t *testing.T) {
	got := runSrcAgent(t, `<?php var_dump(PSFS_PASS_ON, PSFS_FEED_ME, PSFS_ERR_FATAL);`)
	if got != "int(1)\nint(0)\nint(2)\n" {
		t.Fatalf("got %q", got)
	}
}

// TestR17BucketAppendDedupeAgent: повторный append той же корзины в ту же
// бригаду не дублирует данные (bug35916).
func TestR17BucketAppendDedupeAgent(t *testing.T) {
	got := runSrcAgent(t, `<?php
class up_filter extends php_user_filter {
    function filter($in, $out, &$consumed, $closing): int {
        while ($bucket = stream_bucket_make_writeable($in)) {
            $bucket->data = strtoupper($bucket->data);
            $consumed += $bucket->datalen;
            stream_bucket_append($out, $bucket);
            stream_bucket_append($out, $bucket);
        }
        return PSFS_PASS_ON;
    }
}
stream_filter_register('up2', 'up_filter');
$f = fopen('php://memory', 'w');
stream_filter_append($f, 'up2');
fwrite($f, "abc");
rewind($f);
echo fread($f, 100), "
";`)
	if got != "ABC\n" {
		t.Fatalf("got %q", got)
	}
}

// TestR17FcloseInFilterAgent: fclose($this->stream) изнутри filter() не
// закрывает поток: warning + ресурс гаснет, повторный fclose даёт TypeError
// (bug54350).
func TestR17FcloseInFilterAgent(t *testing.T) {
	got := runSrcAgent(t, `<?php
class uf extends php_user_filter {
    function filter($in, $out, &$consumed, $closing): int {
        while ($bucket = stream_bucket_make_writeable($in)) {}
        try { fclose($this->stream); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
        return 0;
    }
}
stream_filter_register('uf','uf');
$fd = fopen('php://memory','w');
stream_filter_append($fd, 'uf');
fwrite($fd, "foo");`)
	if !strings.Contains(got, "fclose(): supplied resource is not a valid stream resource") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "is not a valid stream resource") {
		t.Fatalf("got %q", got)
	}
}

// TestR17FilterURLFailAgent: php://filter с пользовательским фильтром, объект
// которого не создаётся (ленивый дефолт с несуществующей константой):
// две диагностики, затем исходная ошибка (object_init_failure).
func TestR17FilterURLFailAgent(t *testing.T) {
	got := runSrcAgent(t, `<?php
class SampleFilter extends php_user_filter { private $data = \FOO; }
stream_filter_register('sample.filter', SampleFilter::class);
try {
    var_dump(file_get_contents('php://filter/read=sample.filter/resource=php://memory'));
} catch (Error $e) { echo $e->getMessage(), "\n"; }`)
	for _, w := range []string{
		`Unable to create or locate filter "sample.filter"`,
		`Unable to create filter (sample.filter)`,
		`Undefined constant "FOO"`,
	} {
		if !strings.Contains(got, w) {
			t.Fatalf("got %q, want %q", got, w)
		}
	}
}

// TestR17GH20370Agent: $this->stream присваивается только объявленному
// (public/protected) или существующему динамическому свойству; для
// типизированного int — TypeError при первой операции (GH-20370).
func TestR17GH20370Agent(t *testing.T) {
	typed := runSrcAgent(t, `<?php
class pass_filter { public $filtername; public $params; public int $stream = 1;
    function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) { $consumed += $b->datalen; stream_bucket_append($out, $b); }
        return PSFS_PASS_ON; } }
stream_filter_register("pass", "pass_filter");
$fp = fopen("php://memory", "w");
stream_filter_append($fp, "pass");
try { fwrite($fp, "data"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`)
	if !strings.Contains(typed, "fwrite(): Unprocessed filter buckets remaining on input brigade") {
		t.Fatalf("no fwrite warning: %q", typed)
	}
	if !strings.Contains(typed, "Cannot assign resource to property pass_filter::$stream of type int") {
		t.Fatalf("no type error: %q", typed)
	}

	silent := runSrcAgent(t, `<?php
class pf2 { public $filtername; public $params;
    function filter($in, $out, &$consumed, $closing): int {
        var_dump(property_exists($this, 'stream'));
        while ($b = stream_bucket_make_writeable($in)) { $consumed += $b->datalen; stream_bucket_append($out, $b); }
        return PSFS_PASS_ON; } }
stream_filter_register("pf2", "pf2");
$fp = fopen("php://memory", "w");
stream_filter_append($fp, "pf2");
fwrite($fp, "data");
rewind($fp);
echo fread($fp, 1024), "\n";`)
	if !strings.HasPrefix(silent, "bool(false)\n") {
		t.Fatalf("dynamic stream prop created: %q", silent)
	}
}

// TestR17FiberStackSizeIniAgent: fiber.stack_size из INI вызывает исключение
// при start() (gh20483).
func TestR17FiberStackSizeIniAgent(t *testing.T) {
	p := parsePhpt("--INI--\nfiber.stack_size=1024\n--FILE--\n" + `<?php
$fiber = new Fiber(function () {});
try { $fiber->start(); } catch (Exception $e) { echo $e->getMessage(), "\n"; }`)
	got, err := runPhptAt(p, "test.phpt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Fiber stack size is too small, it needs to be at least ") {
		t.Fatalf("got %q", got)
	}
}

// TestR17UnwindTempAgent: временный генератор foreach закрывается при
// раскрутке непойманного исключения до печати ошибки (bug69740), а
// именованная переменная — на shutdown после ошибки (gen1).
func TestR17UnwindTempAgent(t *testing.T) {
	runThrow := func(src string) string {
		t.Helper()
		ResetResourceState()
		out, err := runFile(src)
		if err != nil && throwableOf(err) == nil {
			t.Fatalf("run: %v", err)
		}
		return out
	}
	tmp := runThrow(`<?php
function gen(){ try { yield 1; yield 2; } finally { echo "finally\n"; } }
foreach (gen() as $i) { echo $i, "\n"; throw new Exception(); }`)
	if i, j := strings.Index(tmp, "finally"), strings.Index(tmp, "Fatal error"); i < 0 || j < 0 || i > j {
		t.Fatalf("temp generator order: %q", tmp)
	}
	named := runThrow(`<?php
function gen(){ try { yield 1; yield 2; } finally { echo "finally\n"; } }
$g = gen();
echo $g->current(), "\n";
throw new Exception();`)
	if i, j := strings.Index(named, "finally"), strings.Index(named, "Fatal error"); i < 0 || j < 0 || i < j {
		t.Fatalf("named generator order: %q", named)
	}
}

// TestR17FilterClosingOnceAgent: filter(closing=true), полученный на EOF, не
// повторяется при явном fclose (bug79984), но повторяется при уничтожении
// ресурса в конце запроса (gh20370).
func TestR17FilterClosingOnceAgent(t *testing.T) {
	got := runSrcAgent(t, `<?php
class F extends php_user_filter {
    public function onClose(): void { echo "onClose\n"; }
    public function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) { $consumed += $b->datalen; stream_bucket_append($out, $b); }
        echo "call closing=", var_export($closing, true), "\n";
        return PSFS_PASS_ON;
    }
}
stream_filter_register('f2','F');
$fp = fopen('php://temp','r+b');
fwrite($fp, str_repeat('a', 8300));
fseek($fp, 0, SEEK_SET);
stream_filter_append($fp, 'f2', STREAM_FILTER_READ);
strlen(stream_get_contents($fp));
fclose($fp);`)
	if n := strings.Count(got, "closing=true"); n != 1 {
		t.Fatalf("explicit fclose: want one closing=true, got %q", got)
	}
	if !strings.Contains(got, "onClose") {
		t.Fatalf("no onClose: %q", got)
	}
	got2 := runSrcAgent(t, `<?php
class F3 extends php_user_filter {
    public function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) { $consumed += $b->datalen; stream_bucket_append($out, $b); }
        if ($closing) { echo "C"; }
        return PSFS_PASS_ON;
    }
}
stream_filter_register('f3','F3');
$fp = fopen('php://temp','r+b');
fwrite($fp, "hello");
fseek($fp, 0, SEEK_SET);
stream_filter_append($fp, 'f3', STREAM_FILTER_READ);
echo stream_get_contents($fp);`)
	if n := strings.Count(got2, "C"); n != 2 {
		t.Fatalf("request end: want closing at EOF and at request end, got %q", got2)
	}
}
