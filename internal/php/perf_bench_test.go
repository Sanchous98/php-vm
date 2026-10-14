package php

import (
	"io"
	"testing"

	"phpvm/internal/vm"
)

// Бенчмарки на «простой математике» — для сравнения с php и отслеживания
// регрессий производительности VM. Запуск: go test -run='^$' -bench=. ./internal/php/

func benchPHP(b *testing.B, src string) {
	b.Helper()
	c, err := Compile(src)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var m vm.VM
		m.Init(c.Main, c.Funcs, c.Classes)
		m.SetStepLimit(0)
		m.SetOutput(io.Discard)
		if err := m.Run(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMathSum10M(b *testing.B) {
	benchPHP(b, `<?php $s=0; for($i=0;$i<10000000;$i++){ $s=$s+$i; } echo $s;`)
}

func BenchmarkMathFib30(b *testing.B) {
	benchPHP(b, `<?php function fib($n){ if($n<2){return $n;} return fib($n-1)+fib($n-2); } echo fib(30);`)
}

func BenchmarkMathFloat3M(b *testing.B) {
	benchPHP(b, `<?php $s=0.0; for($i=0;$i<3000000;$i++){ $s=$s+1.5; } echo $s;`)
}

// Бенчмарки диспетчеризации методов/функций.

func BenchmarkMethodCall1M(b *testing.B) {
	benchPHP(b, `<?php class C { function m(){ $x=1; } } $o=new C(); for($i=0;$i<1000000;$i++){ $o->m(); } echo "ok";`)
}

// Пустое тело метода: благодаря skip-call вызов не устанавливает кадр.
func BenchmarkMethodEmptyCall1M(b *testing.B) {
	benchPHP(b, `<?php class C { function m(){} } $o=new C(); for($i=0;$i<1000000;$i++){ $o->m(); } echo "ok";`)
}

func BenchmarkStaticEmptyCall1M(b *testing.B) {
	benchPHP(b, `<?php class C { static function m(){} } for($i=0;$i<1000000;$i++){ C::m(); } echo "ok";`)
}

// Переопределение пустого метода непустым: skip не применяется, вызывается тело.
func BenchmarkMethodOverrideNonEmpty1M(b *testing.B) {
	benchPHP(b, `<?php class A { function m(){} } class B extends A { function m(){ $x=1; } } $o=new B(); for($i=0;$i<1000000;$i++){ $o->m(); } echo "ok";`)
}

func BenchmarkMethodGetter2M(b *testing.B) {
	benchPHP(b, `<?php class C { private $v=2; function get(){ return $this->v; } } $o=new C(); $s=0; for($i=0;$i<2000000;$i++){ $s=$s+$o->get(); } echo $s;`)
}

func BenchmarkStaticCall1M(b *testing.B) {
	benchPHP(b, `<?php class C { static function m(){ return 1; } } $s=0; for($i=0;$i<1000000;$i++){ $s=$s+C::m(); } echo $s;`)
}

func BenchmarkMethodCallDyn1M(b *testing.B) {
	benchPHP(b, `<?php class C { function m(){ return 1; } } $o=new C(); $n="m"; $s=0; for($i=0;$i<1000000;$i++){ $s=$s+$o->$n(); } echo $s;`)
}

func BenchmarkCallDynFunc1M(b *testing.B) {
	benchPHP(b, `<?php function f(){ return 1; } $n="f"; $s=0; for($i=0;$i<1000000;$i++){ $s=$s+$n(); } echo $s;`)
}
