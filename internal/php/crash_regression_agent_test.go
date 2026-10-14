package php

import "testing"

// Регрессия Bug #46381: вызов нестатического метода нативного класса
// статически (ArrayIterator::current()) из метода другого класса.
// Раньше VM передавала чужой $this → небезопасное приведение к splArrayData
// читало за пределами аллокации и роняла процесс SIGSEGV (после ~500 тестов).
// Теперь $this чужого класса не передаётся, падения быть не должно.
func TestStaticCallNonStaticNoSegfault(t *testing.T) {
	const src = `<?php
class test {
    public function method() {
        return ArrayIterator::current();
    }
}
$test = new test();
$test->method();
echo "Done\n";
`
	p := parsePhpt("--FILE--\n" + src)
	if _, err := runPhptAt(p, "/tmp/bug46381.php"); err != nil {
		// Ошибка (fatal) допустима — важно, что процесс не падает.
		t.Logf("run error (ok): %v", err)
	}
}

// Подкласс нативного класса должен выделять нативное хранилище — иначе
// ctx.This[T]() читает за пределами Object.
func TestNativeSubclassAllocatesStorage(t *testing.T) {
	const src = `<?php
class MyIter extends ArrayIterator {}
$it = new MyIter([1,2,3]);
foreach ($it as $k => $v) { echo $k, '=', $v, "\n"; }
echo "Done\n";
`
	p := parsePhpt("--FILE--\n" + src)
	got, err := runPhptAt(p, "/tmp/native_subclass.php")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatalf("no output")
	}
}

// Регрессия: сравнение циклических объектов (свойство ссылается на сам
// объект/друг на друга) не должно уходить в бесконечную рекурсию.
func TestCyclicObjectCompareNoStackOverflow(t *testing.T) {
	const src = `<?php
class A { public $x; }
$a = new A(); $a->x = $a;
$b = new A(); $b->x = $b;
var_dump($a == $b);
echo "Done\n";
`
	p := parsePhpt("--FILE--\n" + src)
	got, err := runPhptAt(p, "/tmp/cyclic_cmp.php")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatalf("no output")
	}
}
