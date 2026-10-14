package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestArrayFnAgentVsPHP — дифференциальные кейсы array-функций, добавленных
// агентом: внутренний указатель (key/current/next/prev/reset/end), мутирующие
// (array_shift/pop/push/unshift/splice), естественная сортировка
// (natsort/natcasesort), callable-версии diff/intersect, array_multisort.
// Сравнение с живым php 8.3.
func TestArrayFnAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- внутренний указатель ---
		`<?php $a=[10,20,30,"x"=>40]; var_dump(reset($a),current($a),key($a),next($a),key($a),prev($a),key($a),end($a),key($a));`,
		`<?php $a=[10,20]; var_dump(current($a)); next($a); next($a); var_dump(current($a),key($a)); reset($a); var_dump(current($a));`,
		`<?php $a=[]; var_dump(current($a),key($a),reset($a),end($a),next($a),prev($a));`,
		`<?php $a=["x"=>1,"y"=>2,"z"=>3]; while(($k=key($a))!==null){ echo "$k="; var_dump(current($a)); next($a); }`,
		`<?php $a=[3=>"c",1=>"a",2=>"b"]; echo key($a); var_dump(current($a)); end($a); echo key($a); reset($a); echo key($a);`,

		// --- array_shift ---
		`<?php $a=[10,20,30,"x"=>40]; var_dump(array_shift($a)); print_r($a);`,
		`<?php $a=["x"=>1,"y"=>2]; var_dump(array_shift($a)); print_r($a);`,
		`<?php $a=[]; var_dump(array_shift($a)); print_r($a);`,
		`<?php $a=[3=>"a",5=>"b",7=>"c"]; var_dump(array_shift($a)); print_r($a);`,
		`<?php $a=[0=>"a","x"=>"b",5=>"c"]; var_dump(array_shift($a)); print_r($a);`,

		// --- array_pop ---
		`<?php $a=["x"=>1,"y"=>2]; var_dump(array_pop($a)); print_r($a);`,
		`<?php $a=[3=>"a",5=>"b",7=>"c"]; var_dump(array_pop($a)); print_r($a);`,
		`<?php $a=[]; var_dump(array_pop($a)); print_r($a);`,

		// --- array_push ---
		`<?php $a=[1,2]; var_dump(array_push($a,3,4)); print_r($a);`,
		`<?php $a=["x"=>1]; var_dump(array_push($a,9)); print_r($a);`,
		`<?php $a=[1,2]; var_dump(array_push($a)); print_r($a);`,

		// --- array_unshift ---
		`<?php $a=[2,3]; var_dump(array_unshift($a,0,1)); print_r($a);`,
		`<?php $a=["x"=>1,"y"=>2]; var_dump(array_unshift($a,"a")); print_r($a);`,
		`<?php $a=[3=>"a",5=>"b",7=>"c"]; var_dump(array_unshift($a,"z")); print_r($a);`,

		// --- array_splice ---
		`<?php $a=[1,2,3,4,5]; var_dump(array_splice($a,1,2)); print_r($a);`,
		`<?php $a=[1,2,3,4,5]; var_dump(array_splice($a,1,2,["a","b","c"])); print_r($a);`,
		`<?php $a=["a"=>1,"b"=>2,"c"=>3]; var_dump(array_splice($a,1,1)); print_r($a);`,
		`<?php $a=[1,2,3]; var_dump(array_splice($a,-1)); print_r($a);`,
		`<?php $a=[1,2,3]; var_dump(array_splice($a,10,2,["z"])); print_r($a);`,
		`<?php $a=[1,2,3,4]; var_dump(array_splice($a,1,-1)); print_r($a);`,
		`<?php $a=[1,2,3,4]; var_dump(array_splice($a,1,0,"X")); print_r($a);`,
		`<?php $a=[0=>"a","x"=>"b",1=>"c","y"=>"d"]; var_dump(array_splice($a,1,2)); print_r($a);`,
		`<?php $a=[1,2,3]; var_dump(array_splice($a,1,1,["x"=>"X","y"=>"Y"])); print_r($a);`,
		`<?php $a=["a"=>1,"b"=>2,"c"=>3]; var_dump(array_splice($a,0,2,[10,20])); print_r($a);`,

		// --- natsort / natcasesort ---
		`<?php $e=["img12.png","img10.png","img2.png","img1.png"]; var_dump(natsort($e)); print_r($e);`,
		`<?php $f=["IMG2","img1","Img3"]; var_dump(natcasesort($f)); print_r($f);`,
		`<?php $g=[10,2,1,"1a",20]; var_dump(natsort($g)); print_r($g);`,
		`<?php $h=["b"=>"10","a"=>"9","c"=>"2"]; natcasesort($h); print_r($h);`,

		// --- callable diff/intersect ---
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; print_r(array_udiff($a1,$a2,$cfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; print_r(array_udiff_assoc($a1,$a2,$cfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; $kfn=fn($x,$y)=>strcmp($x,$y); print_r(array_udiff_uassoc($a1,$a2,$cfn,$kfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $kfn=fn($x,$y)=>strcmp($x,$y); print_r(array_diff_uassoc($a1,$a2,$kfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $kfn=fn($x,$y)=>strcmp($x,$y); print_r(array_diff_ukey($a1,$a2,$kfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; print_r(array_uintersect($a1,$a2,$cfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; print_r(array_uintersect_assoc($a1,$a2,$cfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; $kfn=fn($x,$y)=>strcmp($x,$y); print_r(array_uintersect_uassoc($a1,$a2,$cfn,$kfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $kfn=fn($x,$y)=>strcmp($x,$y); print_r(array_intersect_uassoc($a1,$a2,$kfn));`,
		`<?php $a1=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; $a2=["a"=>1,"b"=>5,"e"=>2,"f"=>6]; $kfn=fn($x,$y)=>strcmp($x,$y); print_r(array_intersect_ukey($a1,$a2,$kfn));`,
		`<?php $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; print_r(array_udiff([1,2,3,4],[2,3],[4],$cfn));`,
		`<?php $cfn=function($x,$y){return $x==$y?0:($x<$y?-1:1);}; print_r(array_uintersect(["x"=>1,"y"=>2,"z"=>3],[2,9],$cfn));`,
		`<?php print_r(array_intersect_key([0=>"b","x"=>1],[0=>"a","x"=>2])); print_r(array_diff_key([0=>"b","x"=>1],[0=>"a"]));`,

		// --- array_multisort ---
		`<?php $a=[3,1,2];$b=["c","a","b"]; var_dump(array_multisort($a,$b)); print_r($a); print_r($b);`,
		`<?php $c=[3,1,2]; var_dump(array_multisort($c,SORT_DESC)); print_r($c);`,
		`<?php $e=[3,1,2];$f=["c","a","b"]; var_dump(array_multisort($e,SORT_DESC,$f)); print_r($e); print_r($f);`,
		`<?php $g=[3,1,2,1];$h=["x","a","b","c"]; var_dump(array_multisort($g,SORT_ASC,SORT_NUMERIC,$h,SORT_ASC,SORT_STRING)); print_r($g); print_r($h);`,
		`<?php $i=["x"=>3,"y"=>1,"z"=>2]; var_dump(array_multisort($i)); print_r($i);`,
		`<?php $j=["10","9","2"]; var_dump(array_multisort($j)); print_r($j);`,

		// --- array_column / array_combine ---
		`<?php $records=[["id"=>1,"n"=>"a"],["id"=>2,"n"=>"b"]]; print_r(array_column($records,"n","id"));`,
		`<?php print_r(array_combine(["a","b"],[1,2])); print_r(array_combine([1,2],[1,2]));`,
		`<?php print_r(array_column([["a"=>1],["a"=>2]],"a"));`,
		`<?php $r=[["a"=>1,"i"=>"k"],["a"=>2]]; print_r(array_column($r,"a","i"));`,
		`<?php print_r(array_column([["a"=>1]],null));`,
		`<?php var_dump(array_combine([],[]));`,

		// --- форматы сообщений об ошибках (как PHP 8.3) ---
		`<?php try { array_diff_uassoc(1,[1],"strcmp"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_diff_uassoc([1],1,"strcmp"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_diff_ukey([1],[1],"unknown_function"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_udiff([1],[1],"nope_xyz"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_combine([1,2],[1]); } catch (ValueError $e) { echo $e->getMessage(); }`,
		`<?php try { array_diff_key([1],"x"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_intersect_key([1],"x"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_column("x","a"); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_column([["a"]], []); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php print_r(array_column([["php"=>7,"foo"],["php"=>8,"bar"]], "php", false));`,
		`<?php print_r(array_column([["php"=>7,"foo"],["php"=>8,"bar"]], "php", true));`,

		// --- натуральное сравнение (ведущие нули, как в PHP) ---
		`<?php var_dump(strnatcmp("a1","a01"), strnatcmp("01","1"), strnatcmp("a1x","a01x"), strnatcasecmp("A1","a01"));`,
		`<?php $e=["A01","a1","b10","a01","b01"]; natcasesort($e); print_r($e);`,

		// --- границы указателя prev/next/end ---
		`<?php $a=[1,2,3]; reset($a); var_dump(prev($a),current($a),key($a));`,
		`<?php $a=[1,2,3]; end($a); var_dump(prev($a),prev($a),prev($a),prev($a));`,
		`<?php $a=["one",2=>"help",3,false,"sk2"=>"val2","sk1"=>"val1"]; end($a); for($i=count($a);$i>0;$i--){var_dump(prev($a));}`,

		// --- array_multisort: ошибки формата PHP ---
		`<?php try { array_multisort([1,2], SORT_REGULAR, SORT_NUMERIC); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php try { array_multisort([1,2], 12345); } catch (ValueError $e) { echo $e->getMessage(); }`,
		`<?php try { array_multisort(1.5); } catch (TypeError $e) { echo $e->getMessage(); }`,
		`<?php $a=[3,1]; $b=[2,1]; var_dump(array_multisort($a,SORT_DESC,$b,SORT_ASC)); print_r($a); print_r($b);`,
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
