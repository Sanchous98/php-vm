package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runIteratorPHP — дифференциальный прогон: вывод интерпретатора должен
// побайтово совпасть с живым php 8.3. Deprecation-шум PHP подавляется
// (error_reporting=0), чтобы проверять семантику, а не диагностику.
func runIteratorPHP(t *testing.T, src string) string {
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
		t.Errorf("iterator mismatch:\n vm  = %q\n php = %q", got, string(want))
	}
	return got
}

// TestArrayAccessVsPHP — offsetGet/offsetSet/offsetExists/offsetUnset.
func TestArrayAccessVsPHP(t *testing.T) {
	src := `<?php
class Bag implements ArrayAccess {
    private array $d = [];
    public function offsetExists(mixed $o): bool { return isset($this->d[$o]); }
    public function offsetGet(mixed $o): mixed { return $this->d[$o] ?? "none"; }
    public function offsetSet(mixed $o, mixed $v): void {
        if ($o === null) { $this->d[] = $v; } else { $this->d[$o] = $v; }
    }
    public function offsetUnset(mixed $o): void { unset($this->d[$o]); }
}
$b = new Bag();
$b["x"] = 10;
$b["y"] = 20;
$b[] = 30;
echo $b["x"], "\n";
echo $b["y"], "\n";
echo $b["z"], "\n";
var_dump(isset($b["x"]));
var_dump(isset($b["z"]));
unset($b["x"]);
var_dump(isset($b["x"]));
$b["n"] = 1;
$b["n"] += 5;
echo $b["n"], "\n";
`
	runIteratorPHP(t, src)
}

// TestArrayAccessNumericKeyVsPHP — числовые ключи и строка/число.
func TestArrayAccessNumericKeyVsPHP(t *testing.T) {
	src := `<?php
class V implements ArrayAccess {
    private array $d = [];
    public function offsetExists(mixed $o): bool { return array_key_exists($o, $this->d); }
    public function offsetGet(mixed $o): mixed { return $this->d[$o] ?? null; }
    public function offsetSet(mixed $o, mixed $v): void { $this->d[$o] = $v; }
    public function offsetUnset(mixed $o): void { unset($this->d[$o]); }
}
$v = new V();
$v[0] = "zero";
$v["1"] = "one";
$v[2] = "two";
var_dump(isset($v[0]));
var_dump(isset($v["0"]));
var_dump(isset($v[3]));
var_dump($v[1]);
var_dump($v[2]);
`
	runIteratorPHP(t, src)
}

// TestCountableVsPHP — count() и is_countable() для Countable.
func TestCountableVsPHP(t *testing.T) {
	src := `<?php
class Cnt implements Countable {
    private int $n = 0;
    public function __construct(int $n) { $this->n = $n; }
    public function count(): int { return $this->n; }
}
$c = new Cnt(5);
echo count($c), "\n";
echo sizeof($c), "\n";
echo count(new Cnt(0)), "\n";
var_dump(is_countable($c));
var_dump(is_countable([1, 2]));
var_dump(is_countable(5));
var_dump(is_countable("x"));
var_dump(is_countable(null));
`
	runIteratorPHP(t, src)
}

// TestCountableErrorsVsPHP — count() не-Countable объекта: TypeError.
func TestCountableErrorsVsPHP(t *testing.T) {
	src := `<?php
try { count(new stdClass()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { count(5); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
echo count([1, 2, 3]), "\n";
`
	runIteratorPHP(t, src)
}

// TestIteratorVsPHP — обход объекта, реализующего Iterator.
func TestIteratorVsPHP(t *testing.T) {
	src := `<?php
class Seq implements Iterator {
    private array $items;
    private int $i = 0;
    public function __construct(array $items) { $this->items = $items; }
    public function rewind(): void { $this->i = 0; }
    public function valid(): bool { return $this->i < count($this->items); }
    public function current(): mixed { return $this->items[$this->i]; }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
}
$s = new Seq(["a", "b", "c"]);
foreach ($s as $k => $v) { echo "$k=$v\n"; }
foreach ($s as $v) { echo $v, "\n"; }
$t = new Seq([]);
foreach ($t as $v) { echo "never\n"; }
// повторный обход должен ре-инициализироваться
foreach ($s as $k => $v) { if ($k == 1) { continue; } echo "again $k=$v\n"; }
`
	runIteratorPHP(t, src)
}

// TestIteratorAggregateVsPHP — обход объекта, реализующего IteratorAggregate.
func TestIteratorAggregateVsPHP(t *testing.T) {
	src := `<?php
class Seq implements Iterator {
    private array $items;
    private int $i = 0;
    public function __construct(array $items) { $this->items = $items; }
    public function rewind(): void { $this->i = 0; }
    public function valid(): bool { return $this->i < count($this->items); }
    public function current(): mixed { return $this->items[$this->i]; }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
}
class Agg implements IteratorAggregate {
    private array $items;
    public function __construct(array $items) { $this->items = $items; }
    public function getIterator(): Iterator { return new Seq($this->items); }
}
$a = new Agg([10, 20, 30]);
foreach ($a as $k => $v) { echo "$k=>$v\n"; }
`
	runIteratorPHP(t, src)
}

// TestObjectPropertiesForeachVsPHP — обход обычного объекта по свойствам.
func TestObjectPropertiesForeachVsPHP(t *testing.T) {
	src := `<?php
class Plain { public $a = 1; public $b = 2; public $c = 3; }
foreach (new Plain() as $k => $v) { echo "$k=$v\n"; }
$o = new stdClass();
$o->x = 7;
foreach ($o as $k => $v) { echo "$k=$v\n"; }
`
	runIteratorPHP(t, src)
}

// TestIteratorInstanceOfVsPHP — implements Iterator/ArrayAccess/Countable
// компилируется и проходит instanceof.
func TestIteratorInstanceOfVsPHP(t *testing.T) {
	src := `<?php
class S implements Iterator {
    public function rewind(): void {}
    public function valid(): bool { return false; }
    public function current(): mixed { return null; }
    public function key(): mixed { return null; }
    public function next(): void {}
}
class A implements ArrayAccess {
    public function offsetExists(mixed $o): bool { return false; }
    public function offsetGet(mixed $o): mixed { return null; }
    public function offsetSet(mixed $o, mixed $v): void {}
    public function offsetUnset(mixed $o): void {}
}
class C implements Countable {
    public function count(): int { return 3; }
}
$s = new S(); $a = new A(); $c = new C();
var_dump($s instanceof Iterator);
var_dump($s instanceof Traversable);
var_dump($a instanceof ArrayAccess);
var_dump($c instanceof Countable);
var_dump($a instanceof Iterator);
`
	runIteratorPHP(t, src)
}

// TestNestedForeachVsPHP — вложенный foreach по объектам-итераторам.
func TestNestedForeachVsPHP(t *testing.T) {
	src := `<?php
class Seq implements Iterator {
    private array $items;
    private int $i = 0;
    public function __construct(array $items) { $this->items = $items; }
    public function rewind(): void { $this->i = 0; }
    public function valid(): bool { return $this->i < count($this->items); }
    public function current(): mixed { return $this->items[$this->i]; }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
}
$outer = new Seq([1, 2]);
foreach ($outer as $x) {
    foreach (new Seq([10, 20]) as $y) {
        echo $x, ",", $y, "\n";
    }
}
`
	runIteratorPHP(t, src)
}

// TestForeachArrayStillWorksVsPHP — не сломали обычный foreach по массиву.
func TestForeachArrayStillWorksVsPHP(t *testing.T) {
	src := `<?php
$a = [1, 2, 3];
foreach ($a as $k => $v) { echo "$k:$v\n"; }
$b = ["x" => 10, "y" => 20];
foreach ($b as $k => $v) { echo "$k=$v\n"; }
`
	runIteratorPHP(t, src)
}
