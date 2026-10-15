package php

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestForeachValDiffVsPHP — дифференциальные кейсы foreach ПО ЗНАЧЕНИЮ по
// массивам (позиционный режим F3): снимок при модификации в теле, ссылки в
// элементах, tombstone-ы после unset, вложенность, current/key/reset/next/end,
// packed/mixed-хранилища. Сравнение с живым php 8.3.
func TestForeachValDiffVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// База: assoc со строковыми ключами, int-ключи вразнобой.
		`<?php $a=['x'=>1,'y'=>2,'z'=>3]; foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		`<?php $a=[5=>'a',2=>'b',7=>'c']; foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		// Только значения (без ключа).
		`<?php $a=['x'=>1,'y'=>2]; foreach($a as $v){ echo $v; }`,
		// Модификация в теле: снимок (append/присваивание/unset).
		`<?php $a=[1,2,3]; foreach($a as $v){ $a[]=$v*10; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=['a'=>1,'b'=>2,'c'=>3]; foreach($a as $k=>$v){ $a['z'.$k]=$v; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3,4,5]; foreach($a as $k=>$v){ if($k==1){ unset($a[3],$a[2]); } if($k==3){ unset($a[4]); } echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ $a[$k]=$v*100; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $v){ $a=[7,8]; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $v){ unset($a); echo $v; } echo "|"; var_dump($a);`,
		// Ссылка-алиас на источник: снимок из-за SEPARATE.
		`<?php $a=[1,2,3]; $r=&$a; foreach($a as $v){ $a[]=$v; echo $v; } echo "|"; var_dump($a,$r);`,
		`<?php $a=[1,2,3]; $r=&$a; foreach($a as $v){ $r[]=$v; echo $v; } echo "|"; var_dump($a,$r);`,
		// Ссылочные элементы: значения разыменовываются, запись через $v не пишет в массив.
		`<?php $x=1; $a=[&$x,2]; foreach($a as $v){ $v=9; } var_dump($a,$x);`,
		`<?php $x=1; $a=[&$x,2,3]; foreach($a as $k=>$v){ echo $k,":",$v,";"; if($k==0){ $x=5; } } echo "|"; var_dump($a);`,
		// unset до цикла (tombstones в снимке).
		`<?php $a=[0,1,2,3,4]; unset($a[1],$a[3]); foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		`<?php $a=['a'=>1,'b'=>2,'c'=>3]; unset($a['b']); foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		// unset в теле по всем элементам (03-паттерн).
		`<?php $m=[]; for($i=0;$i<20;$i++){ $m['k'.$i]=$i; } $s=0; foreach($m as $k=>$v){ if($v%3===0){ unset($m[$k]); } $s+=$v; } echo $s,' ',count($m),"|"; var_dump(array_keys($m));`,
		// Вложенные foreach по значению.
		`<?php $a=[1,2,3]; foreach($a as $x){ foreach($a as $y){ echo "$x-$y;"; } } `,
		`<?php $a=[1,2,3]; foreach($a as $x){ foreach($a as $y){ if($y==2) unset($a[$y]); echo "$x-$y;"; } } echo "|"; var_dump($a);`,
		// by-value снаружи, by-ref внутри (и наоборот).
		`<?php $a=[1,2,3]; foreach($a as $x){ foreach($a as &$y){ $y+=10; } echo $x; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as &$x){ foreach($a as $y){ echo "$x-$y;"; } } echo "|"; var_dump($a);`,
		// current/key/reset/next/end внутри и после цикла (внутренний указатель).
		`<?php $a=['a'=>1,'b'=>2,'c'=>3]; next($a); foreach($a as $k=>$v){ echo "$k=$v;"; } echo "|",key($a),"=",current($a);`,
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ echo "$k:$v:"; echo key($a),'=',current($a),';'; next($a); } echo "|",key($a);`,
		`<?php $a=[1,2,3]; foreach($a as $v){ echo current($a); reset($a); echo current($a); end($a); echo current($a); } echo "|",key($a);`,
		// reset/end до цикла; foreach не трогает указатель.
		`<?php $a=[1,2,3]; end($a); foreach($a as $v){ echo $v; } echo "|",current($a);`,
		// Вложенные массивы и модификация копии значения.
		`<?php $a=[[1,2],[3,4]]; foreach($a as $v){ $v[]=9; echo count($v); } echo "|"; var_dump($a);`,
		`<?php $a=[['x'=>1],['x'=>2]]; foreach($a as $k=>$v){ $v['x']=99; echo $v['x']; } echo "|"; var_dump($a);`,
		// Изменение вложенного массива источника через элемент.
		`<?php $a=[['x'=>1],['x'=>2]]; foreach($a as $k=>$v){ $a[$k]['x']=99; echo $v['x']; } echo "|"; var_dump($a);`,
		// packed int/float/str/generic.
		`<?php $a=range(0,5); foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		`<?php $a=[1.5,2.5,3.5]; foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		`<?php $a=['a','b','c']; foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		`<?php $a=[1,'b',2.5,true,null,[3]]; foreach($a as $k=>$v){ echo $k,':',gettype($v),';'; }`,
		// Модификация в теле меняет тип хранилища только у копии.
		`<?php $a=[1,2,3]; foreach($a as $v){ $a['k']='s'; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=['k'=>1]; foreach($a as $v){ $a[]=2; echo $v; } echo "|"; var_dump($a);`,
		// Свойство объекта как источник.
		`<?php $o=new stdClass; $o->a=['p'=>1,'q'=>2]; foreach($o->a as $k=>$v){ $o->a[]=$v; echo "$k:$v;"; } echo "|"; var_dump($o->a);`,
		`<?php $o=new stdClass; $o->a=[1,2,3]; foreach($o->a as $k=>$v){ unset($o->a[$k]); echo $v; } echo "|"; var_dump($o->a);`,
		// Элемент массива как источник.
		`<?php $a=[['x'=>1,'y'=>2]]; foreach($a[0] as $k=>$v){ $a[0]['z']=$v; echo "$k:$v;"; } echo "|"; var_dump($a);`,
		// Временное выражение как источник.
		`<?php foreach(array_merge([1],[2,3]) as $k=>$v){ echo "$k:$v;"; }`,
		`<?php foreach([10,20,30] as $k=>$v){ echo "$k:$v;"; }`,
		`<?php function f(){ return ['a'=>1,'b'=>2]; } foreach(f() as $k=>$v){ echo "$k:$v;"; }`,
		// Скаляр/пустой: warning и нет цикла.
		`<?php foreach(42 as $v){ echo $v; } echo "|";`,
		`<?php $x=null; foreach($x as $v){ echo $v; } echo "|";`,
		`<?php $a=[]; foreach($a as $v){ echo $v; } echo "|";`,
		// break/continue, continue 2.
		`<?php $a=[1,2,3,4,5]; foreach($a as $k=>$v){ if($v==3) continue; if($v==5) break; echo $v; } echo "|";`,
		`<?php $a=[1,2,3]; foreach($a as $x){ foreach($a as $y){ if($y==2) continue 2; echo "$x-$y;"; } }`,
		// list() по значению и с ключами.
		`<?php $a=[[1,2],[3,4]]; foreach($a as [$x,$y]){ echo "$x-$y;"; }`,
		`<?php $a=[[1,2],[3,4]]; foreach($a as $k=>[$x,$y]){ echo "$k:$x-$y;"; }`,
		// Функция в теле обходит тот же массив (вложенный стек кадров).
		`<?php function g($arr){ $s=0; foreach($arr as $v){ $s+=$v; } return $s; } $a=[1,2,3]; foreach($a as $k=>$v){ echo $v,':',g($a),';'; }`,
		// Модификация через глобал из функции в теле.
		`<?php $a=[1,2,3]; function mod(){ global $a; $a[]=99; } foreach($a as $v){ mod(); echo $v; } echo "|"; var_dump($a);`,
		// Цикл foreach по тому же массиву с unset всех — затем повторный обход.
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ unset($a[$k]); } foreach($a as $v){ echo $v; } echo "|",count($a);`,
		// Элемент-массив со ссылкой внутри.
		`<?php $a=[1,2]; $b=[&$a[0]]; foreach($b as $v){ $v='x'; } var_dump($b,$a);`,
		// Ключи int/string канонизация.
		`<?php $a=['1'=>'a','01'=>'b',1.5=>'c',true=>'d']; foreach($a as $k=>$v){ var_dump($k,$v); }`,
		// Модификация в теле с сохранением ссылки на элемент (снимок значений).
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ $r=&$a[1]; $r=99; echo $v; } echo "|"; var_dump($a);`,
		// unset следующего элемента и добавление с ключом, равным следующему.
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ echo "$k:$v;"; if($k==0){ unset($a[1]); $a[1]=20; } } echo "|"; var_dump($a);`,
		// Строковые ключи с большим числом элементов.
		`<?php $m=[]; for($i=0;$i<50;$i++){ $m['k'.$i]=$i*2; } $s=0; foreach($m as $k=>$v){ if($i=($v%4==0)){ $s+=$v; } } echo $s,"|",count($m);`,
		// by-ref builtins в теле по значению: снимок сохраняется.
		`<?php $a=[1,2,3]; foreach($a as $v){ array_push($a,$v*10); echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3,4]; foreach($a as $v){ array_pop($a); echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3,4]; foreach($a as $v){ array_shift($a); echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3,4]; foreach($a as $k=>$v){ if($k==1){ array_splice($a,1,2,['x','y','z']); } echo "$k:$v;"; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $v){ array_unshift($a,9); echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[3,1,2]; foreach($a as $v){ if($v==3){ rsort($a); } echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[3,1,2]; foreach($a as $k=>$v){ sort($a); echo "$k:$v;"; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $v){ array_walk($a,function(&$x){ $x+=10; }); echo $v; } echo "|"; var_dump($a);`,
		// Запись через ссылку-алиас и через элемент.
		`<?php $a=[1,2,3]; $r=&$a; foreach($a as $k=>$v){ unset($r[1]); echo "$k:$v;"; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ $a[$k]+=1; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; $b=[9]; foreach($a as $v){ $a=&$b; echo $v; } echo "|"; var_dump($a,$b);`,
		`<?php $a=[1,2,3]; foreach($a as $v){ if($v==2){ $a[5]=50; } echo $v; } echo "|"; var_dump($a);`,
		// Ссылки внутри элементов на другие элементы того же массива.
		`<?php $a=[1,2,3]; $a[0]=&$a[2]; foreach($a as $k=>$v){ echo "$k:$v;"; if($k==1){ $a[2]=77; } } echo "|"; var_dump($a);`,
		// Обход после unset последнего и повторное использование массива.
		`<?php $a=[1,2,3]; unset($a[2]); foreach($a as $k=>$v){ echo "$k:$v;"; } $a[]=4; foreach($a as $k=>$v){ echo "$k:$v;"; }`,
		// Много элементов: mixed с unset в начале и в конце.
		`<?php $m=[]; for($i=0;$i<200;$i++){ $m['k'.$i]=$i; } unset($m['k0'],$m['k199'],$m['k100']); $s=0; foreach($m as $k=>$v){ $s+=$v; } echo $s,"|",count($m),"|",array_key_first($m),"|",array_key_last($m);`,
		// $GLOBALS: запись/удаление через суперглобал во время обхода.
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ $GLOBALS['a'][]=99; echo $v; } echo "|"; var_dump($a);`,
		`<?php $a=[1,2,3]; foreach($a as $k=>$v){ unset($GLOBALS['a']); echo "$k:$v;"; } echo "|"; var_dump(isset($a));`,
		`<?php $a=1; $n=0; foreach($GLOBALS as $k=>$v){ $n++; if($n==1){ $b=2; } if($n>50){ break; } } echo $n>0 ? "many" : "none";`,
	}
	for _, src0 := range cases {
		src := src0
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, _ := exec.Command(phppath, "-r", code).CombinedOutput()
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q: run error: %v", src, err)
			continue
		}
		if normDiag(got) != normDiag(string(want)) {
			t.Errorf("diff %q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
		_ = src0
	}
}

var diagRE = regexp.MustCompile(`(PHP )?(Warning|Deprecated|Notice|Fatal error):  ?[^\n]*`)

// normDiag убирает диагностики (Warning/Deprecated/Notice) — тексты и каналы у
// php CLI и VM различаются, для диффа важна семантика вывода.
func normDiag(s string) string {
	s = diagRE.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, ln := range lines {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
