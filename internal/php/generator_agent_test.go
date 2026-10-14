package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGeneratorPHP — дифференциальный прогон: вывод интерпретатора должен
// побайтово совпасть с живым php 8.3. Deprecation-шум подавляется
// (error_reporting=0), чтобы проверять семантику.
//
// ВАЖНО про модель: генераторы реализованы ЭЙГЕРНО — тело исполняется при
// вызове функции-генератора, значения yield собираются в очередь. Поэтому
// тесты ниже наблюдают только те эффекты, которые совпадают с ленивым PHP:
// побочные эффекты (echo до yield) и send() не проверяются.
func runGeneratorPHP(t *testing.T, src string) string {
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
	want, werr := exec.Command(phppath, "-d", "error_reporting=0", f).Output()
	if werr != nil {
		t.Fatalf("php failed for %q: %v\noutput: %s", src, werr, want)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm failed for %q: %v\noutput: %q", src, err, got)
	}
	if got != string(want) {
		t.Errorf("generator mismatch:\n vm  = %q\n php = %q", got, string(want))
	}
	return got
}

// TestGeneratorBasicVsPHP — yield целых чисел и foreach с ключами.
func TestGeneratorBasicVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 1; yield 2; yield 3; }
foreach (g() as $k => $v) { echo "$k=$v\n"; }
`)
}

// TestGeneratorExplicitKeysVsPHP — явные ключи и продолжение авто-ключа.
func TestGeneratorExplicitKeysVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 'a'; yield 5 => 'b'; yield 'c'; yield 2 => 'd'; yield 'e'; }
foreach (g() as $k => $v) { echo var_export($k, true), ":", $v, "\n"; }
`)
}

// TestGeneratorBareYieldVsPHP — `yield;` даёт NULL со следующим авто-ключом.
func TestGeneratorBareYieldVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield; yield 'x'; yield; }
foreach (g() as $k => $v) { var_dump($k); var_dump($v); }
`)
}

// TestYieldFromArrayVsPHP — разворачивание массива, ключи сохраняются,
// авто-ключ внешнего генератора не сдвигается.
func TestYieldFromArrayVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 0 => 'x'; yield from [3 => 'y', 'z']; yield 'w'; }
foreach (g() as $k => $v) { echo "$k=>$v\n"; }
`)
}

// TestYieldFromGeneratorVsPHP — yield from другого генератора, значение
// выражения — return внутреннего.
func TestYieldFromGeneratorVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function inner(){ yield 1; yield 2; return 'R'; }
function outer(){ $r = yield from inner(); yield $r; }
foreach (outer() as $k => $v) { echo "$k=>$v\n"; }
`)
}

// TestYieldFromIteratorVsPHP — yield from пользовательского Iterator.
func TestYieldFromIteratorVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
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
foreach (g() as $k => $v) { echo "$k=>$v\n"; }
`)
}

// TestGeneratorGetReturnVsPHP — getReturn после полного обхода.
func TestGeneratorGetReturnVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 1; yield 2; return 42; }
$g = g();
foreach ($g as $v) { echo "$v\n"; }
var_dump($g->getReturn());
function h(){ yield 1; }
$h = h();
foreach ($h as $v) {}
var_dump($h->getReturn());
`)
}

// TestGeneratorGetReturnThrowsVsPHP — getReturn до исчерпания — Exception.
func TestGeneratorGetReturnThrowsVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 1; yield 2; return 7; }
$g = g();
try { $g->getReturn(); } catch (Throwable $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
`)
}

// TestIteratorToArrayVsPHP — iterator_to_array с ключами и без.
func TestIteratorToArrayVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 'a' => 1; yield 'b' => 2; }
var_dump(iterator_to_array(g()));
var_dump(iterator_to_array(g(), false));
`)
}

// TestGeneratorInstanceOfVsPHP — instanceof и get_class.
func TestGeneratorInstanceOfVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 1; }
$g = g();
var_dump($g instanceof Generator);
var_dump($g instanceof Iterator);
var_dump($g instanceof Traversable);
var_dump(get_class($g));
`)
}

// TestGeneratorManualMethodsVsPHP — current/key/next/valid без foreach.
func TestGeneratorManualMethodsVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 1; yield 2; yield 3; }
$g = g();
var_dump($g->current());
var_dump($g->key());
var_dump($g->valid());
$g->next();
var_dump($g->current());
var_dump($g->key());
$g->next();
$g->next();
var_dump($g->valid());
var_dump($g->current());
var_dump($g->key());
`)
}

// TestGeneratorRewindErrorVsPHP — повторный rewind после next() запрещён.
func TestGeneratorRewindErrorVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ yield 1; yield 2; }
$g = g();
$g->rewind();
$g->next();
try { $g->rewind(); } catch (Throwable $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
`)
}

// TestGeneratorMethodVsPHP — метод класса-генератор.
func TestGeneratorMethodVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
class C {
    public function gen() { yield 'a' => 1; yield 'b' => 2; }
}
foreach ((new C())->gen() as $k => $v) { echo "$k=$v\n"; }
`)
}

// TestGeneratorClosureVsPHP — анонимная функция-генератор.
func TestGeneratorClosureVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
$g = function () { yield 10; yield 20; };
foreach ($g() as $k => $v) { echo "$k=$v\n"; }
`)
}

// TestGeneratorLoopsVsPHP — yield в циклах и ветвлениях (конечные).
func TestGeneratorLoopsVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ for ($i = 0; $i < 3; $i++) yield $i * 10; if (true) yield 'end'; }
foreach (g() as $k => $v) { echo "$k=>$v\n"; }
function h($n){ while ($n > 0) { yield $n; $n--; } }
foreach (h(3) as $v) { echo $v, ","; }
echo "\n";
`)
}

// TestGeneratorYieldResultVsPHP — результат yield-выражения (NULL в эйгерной
// модели) и вложенные yield from с повторяющимися ключами.
func TestGeneratorYieldResultVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g(){ $x = yield 1; $y = yield 2 => 3; return $x; }
$g = g();
foreach ($g as $k => $v) { echo "$k=>$v\n"; }
var_dump($g->getReturn());
function arr(){ yield from [1, 2]; yield from [3, 4]; }
var_dump(iterator_to_array(arr()));
function nested(){ yield from arr(); yield 'z'; }
foreach (nested() as $k => $v) { echo "$k=$v,"; }
echo "\n";
`)
}

// TestGeneratorForeachReuseErrorVsPHP — повторный foreach по исчерпанному
// генератору. Эйгерная модель даёт формулировку «Cannot rewind...», а Zend —
// «Cannot traverse an already closed generator», поэтому проверяем только
// факт исключения (класс Exception).
func TestGeneratorForeachReuseErrorVsPHP(t *testing.T) {
	got := runGeneratorPHP(t, `<?php
function g(){ yield 1; yield 2; }
$g = g();
foreach ($g as $v) { echo "$v\n"; }
try { foreach ($g as $v) { echo "again\n"; } }
catch (Exception $e) { echo "Exception\n"; }
`)
	if got != "1\n2\nException\n" {
		t.Errorf("unexpected: %q", got)
	}
}

// TestGeneratorRecursiveVsPHP — рекурсивный генератор через yield from.
func TestGeneratorRecursiveVsPHP(t *testing.T) {
	runGeneratorPHP(t, `<?php
function g($n){ if ($n > 0) { yield $n; yield from g($n - 1); } }
foreach (g(3) as $k => $v) { echo "$k=$v,"; }
echo "\n";
`)
}
