package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gen2Diff прогоняет исходник и в живом php 8.3, и в интерпретаторе, и
// сравнивает вывод побайтово. Deprecation-шум подавляется. Паника php/VM или
// несовпадение — ошибка теста. Служит для проверки ЧЕСТНОЙ ленивой модели
// генераторов: порядок исполнения тела и побочные эффекты должны совпадать.
func gen2Diff(t *testing.T, src string) string {
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
	want, werr := exec.Command(phppath, "-d", "error_reporting=0", f).CombinedOutput()
	if werr != nil {
		t.Fatalf("php failed for %q: %v\noutput: %s", src, werr, want)
	}
	got, rerr := runFileAt(src, f)
	if rerr != nil {
		t.Fatalf("vm failed for %q: %v\noutput: %q", src, rerr, got)
	}
	if got != string(want) {
		t.Errorf("generator mismatch:\n vm  = %q\n php = %q", got, string(want))
	}
	return got
}

// TestGen2LazyOneByOneVsPHP — ОБЯЗАТЕЛЬНАЯ проверка «по одному элементу»:
// при создании генератора тело не исполняется, на каждой итерации тело
// доходит ровно до следующего yield, а после break «B/C» не исполняются.
func TestGen2LazyOneByOneVsPHP(t *testing.T) {
	got := gen2Diff(t, `<?php
function g(){ echo "A"; yield 1; echo "B"; yield 2; echo "C"; }
$it = g();
echo "start;";
foreach ($it as $v) { echo "got$v;"; if ($v==1) { echo "break;"; break; } }
`)
	if want := "start;Agot1;break;"; got != want {
		t.Errorf("laziness mismatch: got %q want %q", got, want)
	}
}

// TestGen2NoExecutionOnCreateVsPHP — вызов генератора не даёт побочных
// эффектов; они появляются только при advancing. Счётчик снаружи фиксирует,
// сколько шагов тела реально выполнено.
func TestGen2NoExecutionOnCreateVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(&$log){ for ($i = 0; $i < 5; $i++) { $log[] = $i; yield $i; } }
$log = [];
$it = g($log);
echo "created:", count($log), "\n";
var_dump($it->current());
echo "after first:", count($log), "\n";
$it->next(); $it->next();
echo "after 3rd:", count($log), "\n";
$it->next(); $it->next();
echo "after all:", count($log), " valid=", var_export($it->valid(), true), "\n";
`)
}

// TestGen2SendSequenceVsPHP — значение send() попадает ровно в yield-выражение.
func TestGen2SendSequenceVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ echo "A"; $x = yield 1; echo "[x=$x]"; $y = yield 2; echo "[y=$y]"; return $x.$y; }
$it = g();
var_dump($it->current());
var_dump($it->send("S1"));
var_dump($it->send("S2"));
var_dump($it->valid());
var_dump($it->getReturn());
`)
}

// TestGen2SendBeforeStartVsPHP — send() до старта запускает тело и прокидывает
// значение в первый yield (как Zend).
func TestGen2SendBeforeStartVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ echo "A"; $x = yield 1; echo "[x=$x]"; yield 2; return 42; }
$it = g();
var_dump($it->send("S0"));
var_dump($it->current());
`)
}

// TestGen2NextPrimesVsPHP — next() до старта запускает тело и сразу
// продвигает мимо первого yield.
func TestGen2NextPrimesVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ echo "A"; yield 1; echo "B"; yield 2; echo "C"; }
$it = g();
$it->next();
var_dump($it->current());
var_dump($it->key());
`)
}

// TestGen2GetReturnAutoPrimeVsPHP — getReturn() у неначатого генератора сам
// прогоняет тело; если оно сразу вернулось — возвращает значение.
func TestGen2GetReturnAutoPrimeVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function gen1(){ return; yield; }
function gen2(){ return "str"; yield; }
function gen5($v){ return (int)$v; yield; }
var_dump(gen1()->getReturn());
var_dump(gen2()->getReturn());
var_dump(gen5("42")->getReturn());
`)
}

// TestGen2GetReturnErrorsVsPHP — getReturn() до return и после abort'а.
func TestGen2GetReturnErrorsVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ yield 1; yield 2; return 3; }
$g = g();
try { $g->getReturn(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
function h(){ throw new Exception("boom"); yield 1; }
$h = h();
try { $h->next(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
try { $h->getReturn(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
`)
}

// TestGen2YieldFromReturnVsPHP — значение yield from — return внутреннего.
func TestGen2YieldFromReturnVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function inner(){ yield 1; yield 2; return 'R'; }
function outer(){ $r = yield from inner(); echo "r=$r\n"; yield 'z'; }
foreach (outer() as $k => $v) echo "$k=>$v\n";
`)
}

// TestGen2YieldFromArrayKeysVsPHP — yield from массива не сдвигает авто-ключ.
func TestGen2YieldFromArrayKeysVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ yield 0 => 'x'; yield from [3 => 'y', 'z']; yield 'w'; }
foreach (g() as $k => $v) echo "$k=>$v\n";
`)
}

// TestGen2NestedGeneratorsVsPHP — вложенные yield from и повторные ключи.
func TestGen2NestedGeneratorsVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function arr(){ yield from [1, 2]; yield from [3, 4]; }
function nested(){ yield from arr(); yield 'z'; }
foreach (nested() as $k => $v) echo "$k=$v,";
echo "\n";
function rec($n){ if ($n > 0) { yield $n; yield from rec($n - 1); } }
foreach (rec(3) as $k => $v) echo "$k=$v,";
echo "\n";
`)
}

// TestGen2YieldFromIteratorVsPHP — yield from пользовательского Iterator.
func TestGen2YieldFromIteratorVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
class Seq implements Iterator {
    private array $items; private int $i = 0;
    public function __construct(array $it) { $this->items = $it; }
    public function rewind(): void { $this->i = 0; }
    public function valid(): bool { return $this->i < count($this->items); }
    public function current(): mixed { return $this->items[$this->i]; }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
}
function g(){ yield from new Seq(['p', 'q']); yield 'r'; }
foreach (g() as $k => $v) echo "$k=>$v\n";
`)
}

// TestGen2ClassAndInstanceOfVsPHP — get_class/instanceof для Generator.
func TestGen2ClassAndInstanceOfVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ yield 1; }
$g = g();
var_dump($g instanceof Generator);
var_dump($g instanceof Iterator);
var_dump($g instanceof Traversable);
var_dump(get_class($g));
`)
}

// TestGen2RewindErrorVsPHP — повторный rewind после next() запрещён, а
// повторный rewind в точке первого yield разрешён.
func TestGen2RewindErrorVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){ yield 1; yield 2; }
$g = g();
$g->rewind();
var_dump($g->current());
$g->rewind();
var_dump($g->current());
$g->next();
try { $g->rewind(); } catch (Throwable $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
`)
}

// TestGen2ThrowIntoGeneratorVsPHP — throw() прокидывает исключение в точку
// yield; try/catch/finally продолжают работать через границу yield.
func TestGen2ThrowIntoGeneratorVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){
    try { yield 1; echo "in try\n"; yield 2; }
    catch (Exception $e) { echo "caught: ", $e->getMessage(), "\n"; yield 99; }
    finally { echo "finally\n"; }
    yield 3;
}
$it = g();
foreach ($it as $v) {
    echo "v=$v\n";
    if ($v == 1) { $it->throw(new Exception("boom")); }
}
`)
}

// TestGen2TryCatchAcrossYieldVsPHP — исключение из тела, брошенное между
// yield'ами, перехватывается catch через границу приостановки.
func TestGen2TryCatchAcrossYieldVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g(){
    try { yield 1; throw new Exception("mid"); yield 2; }
    catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; yield 3; }
}
$it = g();
foreach ($it as $v) echo "v=$v\n";
echo "done\n";
`)
}

// TestGen2MethodAndClosureVsPHP — методы и замыкания-генераторы ленивы.
func TestGen2MethodAndClosureVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
class C {
    public string $log = "";
    public function gen(){ $this->log .= "M"; yield 'a' => 1; $this->log .= "N"; yield 'b' => 2; }
}
$c = new C();
$it = $c->gen();
echo "created:", $c->log, "\n";
foreach ($it as $k => $v) { echo "$k=$v:", $c->log, "\n"; if ($v == 1) break; }
$cl = function(){ echo "C"; yield 10; echo "D"; yield 20; };
$g = $cl();
echo "|";
foreach ($g as $v) { echo $v; if ($v == 10) break; }
echo "\n";
`)
}

// TestGen2ThrowBeforeStartAndClosedVsPHP — throw() до старта праймит тело,
// а на закрытом генераторе исключение уходит вызывающему.
func TestGen2ThrowBeforeStartAndClosedVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function h(){ echo "start"; yield 1; }
$it = h();
try { $it->throw(new Exception("early")); } catch (Throwable $e) { echo "/", $e->getMessage(), "\n"; }
function k(){ yield 1; }
$it2 = k();
$it2->next(); $it2->next();
try { $it2->throw(new Exception("late")); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump($it2->send("x"));
var_dump($it2->next());
`)
}

// TestGen2ForeachReuseErrorVsPHP — повторный foreach по исчерпанному
// генератору — исключение.
func TestGen2ForeachReuseErrorVsPHP(t *testing.T) {
	got := gen2Diff(t, `<?php
function g(){ yield 1; yield 2; }
$g = g();
foreach ($g as $v) { echo "$v\n"; }
try { foreach ($g as $v) { echo "again\n"; } }
catch (Throwable $e) { echo "Exception\n"; }
`)
	if got != "1\n2\nException\n" {
		t.Errorf("unexpected reuse output: %q", got)
	}
}

// TestGen2SideEffectOrderVsPHP — порядок побочных эффектов в циклах и
// ветвлениях: тело исполняется порциями, между внешними действиями.
func TestGen2SideEffectOrderVsPHP(t *testing.T) {
	gen2Diff(t, `<?php
function g($n){
    for ($i = 0; $i < $n; $i++) {
        if ($i % 2 == 0) { echo "e$i;"; yield $i; }
        else { echo "o$i;"; yield $i * 10; }
    }
    echo "end;";
}
$it = g(4);
foreach ($it as $v) {
    echo "got$v;";
    if ($v == 20) break;
}
echo "|after\n";
`)
}
