package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCallableAgentVsPHP — дифференциальные кейсы вызова callable из
// встроенных функций (call_user_func, array_map/filter/reduce, сортировки,
// array_walk). Сравнение с живым php 8.3.
func TestCallableAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- call_user_func / call_user_func_array ---
		`<?php function add($a,$b){return $a+$b;} echo call_user_func("add",2,3);`,
		`<?php function add($a,$b){return $a+$b;} echo call_user_func_array("add",[5,7]);`,
		`<?php $f=fn($x)=>$x*$x; echo call_user_func($f,6);`,
		`<?php function greet($n){return "hi $n";} echo call_user_func_array("greet",["bob"]);`,

		// --- callable-формы: методы, static, __invoke, "C::m" ---
		`<?php class C { function m($x){return $x*2;} static function s($x){return $x+1;} function __invoke($x){return $x-1;} } $o=new C; echo call_user_func([$o,"m"],5),"|",call_user_func(["C","s"],5),"|",call_user_func($o,5),"|",call_user_func("C::s",9);`,
		`<?php class C { function m($x){return $x*2;} } $o=new C; var_dump(is_callable([$o,"m"]), is_callable("strlen"), is_callable("nope"));`,
		`<?php class C { function __invoke($a,$b){return $a.$b;} } $o=new C; echo call_user_func($o,"x","y");`,

		// --- array_map ---
		`<?php $f=fn($x)=>$x*$x; print_r(array_map($f,["a"=>1,"b"=>2,"c"=>3]));`,
		`<?php print_r(array_map(null,[1,2,3]));`,
		`<?php print_r(array_map(null,[1,2,3],["x","y"]));`,
		`<?php print_r(array_map(fn($a,$b)=>$a+$b,[1,2],[10,20,30]));`,
		`<?php print_r(array_map("strtoupper",["a","b"]));`,
		`<?php print_r(array_map(fn($x)=>$x+1,[5=>10,9=>20]));`,

		// --- array_filter ---
		`<?php print_r(array_filter([1,0,2,null,3,"",4]));`,
		`<?php print_r(array_filter(["a"=>1,"b"=>0,"c"=>3], fn($v)=>$v>1));`,
		`<?php print_r(array_filter(["a"=>1,"b"=>0,"c"=>3], fn($k)=>$k==="b", ARRAY_FILTER_USE_KEY));`,
		`<?php print_r(array_filter(["a"=>1,"b"=>0], fn($v,$k)=>$k==="a", ARRAY_FILTER_USE_BOTH));`,

		// --- array_reduce ---
		`<?php var_dump(array_reduce([1,2,3,4], fn($c,$v)=>$c+$v, 10));`,
		`<?php var_dump(array_reduce([1,2,3], fn($c,$v)=>$c.$v));`,
		`<?php var_dump(array_reduce([], fn($c,$v)=>$c+$v, 42));`,

		// --- usort / uasort / uksort ---
		`<?php $a=[3,1,2]; usort($a, fn($x,$y)=>$x-$y); print_r($a);`,
		`<?php $a=[3,1,2]; var_dump(usort($a, fn($x,$y)=>$y-$x)); print_r($a);`,
		`<?php $b=["b"=>2,"a"=>3,"c"=>1]; uasort($b, fn($x,$y)=>$x-$y); print_r($b);`,
		`<?php $c=["b"=>2,"a"=>3,"c"=>1]; uksort($c, fn($x,$y)=>strcmp($x,$y)); print_r($c);`,
		`<?php $a=["b","a","c"]; usort($a, fn($x,$y)=>strcmp($x,$y)); print_r($a);`,

		// --- sort / rsort / asort / ksort без callback ---
		`<?php $f=[3,1,2]; sort($f); print_r($f);`,
		`<?php $f=[3,1,2]; rsort($f); print_r($f);`,
		`<?php $g=["b"=>2,"a"=>3]; ksort($g); print_r($g);`,
		`<?php $g=["b"=>2,"a"=>3]; krsort($g); print_r($g);`,
		`<?php $g=["b"=>2,"a"=>3]; asort($g); print_r($g);`,
		`<?php $g=["b"=>2,"a"=>3]; arsort($g); print_r($g);`,
		`<?php $a=["10","9","2"]; sort($a, SORT_STRING); print_r($a);`,
		`<?php $a=["10","9","2"]; sort($a, SORT_NUMERIC); print_r($a);`,
		`<?php $a=["B","a","C"]; sort($a, 10); print_r($a);`,
		`<?php $c=[3=>"c",1=>"a",2=>"b"]; ksort($c); print_r($c);`,
		`<?php $c=[3=>"c",1=>"a",2=>"b"]; uksort($c, fn($x,$y)=>$x-$y); print_r($c);`,
		`<?php $a=[]; var_dump(sort($a)); print_r($a);`,

		// --- array_walk / array_walk_recursive ---
		`<?php $d=[1,2,3]; array_walk($d, function(&$v,$k){$v=$v*10+$k;}); print_r($d);`,
		`<?php $a=[1,2,3]; $r=[]; array_walk($a, function($v,$k) use (&$r){ $r[]=$v*$k; }); print_r($r);`,
		`<?php $d=["a"=>1,"b"=>2]; array_walk($d, function(&$v,$k){ $v="$k:$v"; }); print_r($d);`,
		`<?php $e=[1,[2,3],[4,[5]]]; array_walk_recursive($e, function(&$v,$k){$v++;}); print_r($e);`,
		`<?php $a=[1,[2,[3]]]; $r=[]; array_walk_recursive($a, function($v,$k) use (&$r){ $r[]=$v; }); print_r($r);`,
		`<?php $d=[1]; array_walk($d, function($v,$k,$x){ echo "$v-$k-$x"; }, "z");`,
	}

	for _, src0 := range cases {
		src := src0
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Logf("php error for %q: %v", src, werr)
			continue
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q: run error: %v", src, err)
			continue
		}
		if got != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}
