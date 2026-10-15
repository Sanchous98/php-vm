package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestForeachRefAgentVsPHP — дифференциальные кейсы позиционного обхода
// foreach по ссылке (живая хеш-позиция, элемент как настоящая Ref-ячейка),
// живого обхода свойств объекта, WeakMap и свежих хендлов клона.
// Сравнение с живым php 8.3.
func TestForeachRefAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// Изменение массива из тела цикла видно через цель-ссылку.
		`<?php $a=[1,2,3]; foreach($a as $k=>&$v){ $a[$k]=$v*100; } var_dump($a);`,
		// Добавление элементов: итератор идёт по живой позиции.
		`<?php $a=[1,2,3]; foreach($a as &$v){ array_push($a,"n$v"); if(count($a)>6) break; } var_dump($a);`,
		// Удаление текущего/последующих элементов.
		`<?php $a=[1,2,3,4]; foreach($a as &$v){ echo "$v,"; array_pop($a); } echo "|"; var_dump($a);`,
		// unset элементов во время обхода.
		`<?php $a=[0,1,2,3,4]; foreach($a as $k=>&$v){ if($k==1){ unset($a[3],$a[2]); } echo $v; } echo "|"; var_dump($a);`,
		// Ссылкой в массиве остаётся только последний обойдённый элемент.
		`<?php $a=[1,2,3]; foreach($a as &$v){} var_dump($a);`,
		// Вложенные foreach по ссылке по одному массиву.
		`<?php $a=[1,2,3]; foreach($a as &$x){ foreach($a as &$y){ echo "$x-$y;"; $y++; } } echo "|"; var_dump($a);`,
		// rsort и array_walk сохраняют позицию итератора.
		`<?php $a=[1,2,3,4,5,0]; foreach($a as &$v){ echo $v; if($v==3){ rsort($a); } } echo "|";`,
		`<?php $a=[1,2,3,4,5]; foreach($a as &$v){ echo $v; if($v==3){ array_walk($a,function(&$x){$x+=10;}); } } echo "|";`,
		// by-ref foreach делает переменную ссылкой и пишет через внешний alias.
		`<?php $a=[1,2,3]; $r=&$a; foreach($a as &$v){ $v+=1; } var_dump($a,$r);`,
		// Копия до цикла не затронута (SEPARATE_ARRAY).
		`<?php $a=[1,2,3]; $b=$a; foreach($a as &$v){ $v*=10; } var_dump($a,$b);`,
		// foreach по значению по-прежнему идёт по снимку.
		`<?php $a=[1,2,3]; foreach($a as $v){ array_pop($a); echo $v; } echo "|"; var_dump($a);`,
		// foreach по ссылке на элемент массива.
		`<?php $a=[[1,2]]; foreach($a[0] as &$v){ $v*=10; } var_dump($a);`,
		// foreach по ссылке на свойство/элемент: массив отделяется от COW-копии
		// в глобальной переменной (непромотированный main, opGlobalSet).
		`<?php $a = new stdClass; $b = $a->b = array('original'); foreach($a->b as $k=>&$v) { $v = 'changed'; } var_dump($b); unset($a, $b);`,
		`<?php $b = $a[0] = array('original'); foreach($a[0] as $k=>&$v) { $v = 'changed'; } var_dump($b); unset($a, $b);`,
		`<?php $b = $a[0][0] = array('original'); foreach($a[0][0] as $k=>&$v) { $v = 'changed'; } var_dump($b); unset($a, $b);`,

		// Живой обход свойств объекта: добавленные свойства видны.
		`<?php class C{ public $a=1; public $b=2; } $o=new C; $i=0; foreach($o as $v){ if($i++<2){ $o->{"n$i"}=$v; } echo $v; } echo "|"; var_dump($o);`,
		// Удаление свойств во время обхода: снятые пропускаются.
		`<?php class C{ public $a=1; public $b=2; public $c=3; public $d=4; } $o=new C; foreach($o as $v){ if($v==1){ unset($o->b); } if($v==2){ unset($o->a); } echo $v; } echo "|"; var_dump($o);`,
		// Неинициализированные типизированные свойства в foreach пропускаются.
		`<?php class C{ public int $a=1; public int $b; public $c; } foreach(new C as $k=>$v){ echo "$k="; var_dump($v); }`,
		// Приватные свойства видны только из своей области (Bug #33171).
		`<?php class A{ private $c="A"; } class B extends A{ private $c="B"; function go(){ foreach($this as $k=>$v){ echo "$k=$v;"; } } } (new B)->go();`,

		// WeakMap: пары key=>value и by-ref запись значений.
		`<?php $m=new WeakMap; $o1=new stdClass; $o2=new stdClass; $m[$o1]=1; $m[$o2]=2; foreach($m as $k=>$v){ echo ($k===$o1?"o1":($k===$o2?"o2":"?")), "=$v;"; } echo "|";`,
		`<?php $m=new WeakMap; $o1=new stdClass; $o2=new stdClass; $m[$o1]=1; $m[$o2]=2; foreach($m as $k=>&$v){ $v++; } var_dump($m[$o1],$m[$o2]);`,
		`<?php $m=new WeakMap; $o=new stdClass; $m[$o]=null; var_dump(isset($m[$o]), !empty($m[$o]));`,
		`<?php $m=new WeakMap; $o=new stdClass; $m[$o]=[]; $m[$o][]=42; $m[$o]=[]; $m[$o][]=7; var_dump($m[$o]);`,

		// Клон получает собственный хендл (не переиспользует хендл источника).
		`<?php $a=new stdClass; $b=clone $a; var_dump($a,$b);`,
		`<?php $a=new WeakMap; $o=new stdClass; $a[$o]=1; $b=clone $a; var_dump($a,$b);`,
	}

	for _, src0 := range cases {
		src := src0
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-d", "error_reporting=E_ALL", "-r", code).CombinedOutput()
		if werr != nil {
			// Фатальные ошибки PHP тоже сравнимы (текст печатается в stdout/stderr).
			t.Logf("php exit for %q: %v", src, werr)
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q: run error: %v", src, err)
			continue
		}
		// Нормализация: формат депрекаций у `php -r` отличается от нашего
		// сниппет-раннера ("PHP Deprecated … Command line code" vs "Deprecated …
		// php"), а тест проверяет семантику foreach — сравниваем без
		// депрекаций и без пробелов.
		if normNoDep(got) != normNoDep(string(want)) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}
