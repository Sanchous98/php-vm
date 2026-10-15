package php

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

func p1run(t *testing.T, src string) string {
	t.Helper()
	c, err := CompileFile(src, "t.php")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(0)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	if err := m.Run(); err != nil {
		if throwableOf(err) == nil {
			t.Fatalf("run: %v", err)
		}
	}
	return buf.String()
}

func p1php(t *testing.T, src string) string {
	t.Helper()
	cmd := exec.Command("php", "-d", "error_reporting=E_ALL", "-d", "display_errors=1")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("php: %v\n%s", err, out)
	}
	return string(out)
}

// TestP1OrderDiff — порядок обхода/ключей после unset+insert обязан совпадать
// с живым php во всех сценариях задачи.
func TestP1OrderDiff(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not found")
	}
	scen := []struct{ name, src string }{
		{"unset_mid", `<?php $a=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; unset($a["b"]); $a["e"]=5; $a["c"]=30; echo implode(",",array_keys($a)),"|",implode(",",$a),"\n"; var_dump($a);`},
		{"unset_begin", `<?php $a=["x"=>1,"y"=>2,"z"=>3]; unset($a["x"]); $a["w"]=4; echo implode(",",array_keys($a)),"\n";`},
		{"unset_end", `<?php $a=["x"=>1,"y"=>2,"z"=>3]; unset($a["z"]); $a["w"]=4; unset($a["w"]); $a["v"]=5; echo implode(",",array_keys($a)),"\n";`},
		{"unset_reinsert_same", `<?php $a=["a"=>1,"b"=>2,"c"=>3]; unset($a["a"]); $a["a"]=9; echo implode(",",array_keys($a)),"\n"; unset($a["b"]); unset($a["c"]); $a["b"]=8; $a["c"]=7; echo implode(",",array_keys($a)),"\n";`},
		{"int_keys", `<?php $a=[10=>1,20=>2,30=>3]; unset($a[20]); $a[40]=4; $a[]=5; echo implode(",",array_keys($a)),"|",$a[41],"\n";`},
		{"foreach_unset_val", `<?php $a=["a"=>1,"b"=>2,"c"=>3,"d"=>4,"e"=>5]; foreach($a as $k=>$v){ echo "$k=$v;"; if($v%2==0) unset($a[$k]); } echo "|",implode(",",array_keys($a)),"\n";`},
		{"foreach_append", `<?php $a=[1,2,3]; $i=0; foreach($a as $k=>$v){ echo "$k=$v;"; if($i++<2){ $a[]=10+$i; } } echo "|",implode(",",$a),"\n";`},
		{"foreach_modify_val", `<?php $a=["a"=>1,"b"=>2,"c"=>3]; foreach($a as $k=>$v){ $a["b"]=20; unset($a["c"]); echo "$k=$v;"; } echo "|",implode(",",$a),"\n";`},
		{"foreach_byref", `<?php $a=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; foreach($a as $k=>&$v){ $v*=10; if($k==="b"){ unset($a["c"]); $a["e"]=5; } } echo implode(",",array_keys($a)),"|",implode(",",$a),"\n";`},
		{"nested_iter", `<?php $a=["a"=>1,"b"=>2,"c"=>3]; $out=""; foreach($a as $k=>$v){ foreach($a as $k2=>$v2){ $out.="$k$k2"; if($k2==="b" && $k==="a"){ unset($a["c"]); } } } echo $out,"|",implode(",",array_keys($a)),"\n";`},
		{"current_key", `<?php $a=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; unset($a["b"]); reset($a); echo key($a),"=",current($a),";"; next($a); echo key($a),"=",current($a),";"; end($a); echo key($a),"=",current($a),";"; prev($a); echo key($a),";"; ` + "\n"},
		{"serialize_order", `<?php $a=["a"=>1,"b"=>2,"c"=>3]; unset($a["a"]); unset($a["c"]); $a["d"]=4; $a["a"]=5; echo serialize($a),"\n",json_encode($a),"\n"; print_r(array_values($a));`},
		{"churn_compact", `<?php $a=[]; for($i=0;$i<200;$i++) $a["k$i"]=$i; for($i=0;$i<200;$i+=2){ unset($a["k$i"]); } for($i=0;$i<200;$i+=2){ $a["n$i"]=$i; } for($i=100;$i<200;$i+=4){ unset($a["k$i"]); $a["m$i"]=$i; } echo count($a),"|",implode(",",array_slice(array_keys($a),0,12)),"|",implode(",",array_slice(array_keys($a),-8)),"\n"; echo serialize($a),"\n";`},
		{"walk_mutate", `<?php $a=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; array_walk($a, function(&$v,$k){ $v*=2; }); echo implode(",",$a),"\n"; $b=["a"=>1,"b"=>2,"c"=>3,"d"=>4]; array_walk($b, function(&$v,$k) use (&$b){ if($v%2===0) unset($b[$k]); }); echo implode(",",array_keys($b)),"\n";`},
		{"tail_reuse", `<?php $a=[1,2,3,4,5]; unset($a[4]); unset($a[3]); $a[]=6; $a[]=7; $a[]=8; echo implode(",",$a),"\n"; unset($a[7]); $a[99]=1; echo implode(",",array_keys($a)),"\n";`},
		{"dense_churn", `<?php $a=[]; for($i=0;$i<50;$i++) $a["k$i"]=$i; for($j=0;$j<8;$j++){ for($i=0;$i<50;$i+=2) unset($a["k$i"]); for($i=0;$i<50;$i+=2) $a["k$i"]=$i+$j; } echo count($a),"|",implode(",",array_keys($a)),"\n";`},
		{"keys_values_fns", `<?php $a=["z"=>1,"a"=>2,"m"=>3]; unset($a["a"]); $a["b"]=4; echo implode(",",array_keys($a)),"|",implode(",",array_values($a)),"|",implode(",",$a),"\n"; $c=array_merge($a,["q"=>9]); echo implode(",",array_keys($c)),"\n"; echo implode(",",array_keys(array_reverse($a))),"\n";`},
	}
	for _, s := range scen {
		t.Run(s.name, func(t *testing.T) {
			got := p1run(t, s.src)
			want := p1php(t, s.src)
			if got != want {
				t.Errorf("MISMATCH %s\n--- vm ---\n%s\n--- php ---\n%s", s.name, got, want)
			}
		})
	}
}
