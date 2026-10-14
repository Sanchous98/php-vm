package php

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Поведенческие тесты строкового буфера (`.=`/OpConcatApp и OpConcatN):
// алиасинг после копирования слотов, by-ref, самоприписывание, порядок
// диагностик и сравнение с живым php.

func TestStringBufAliasing(t *testing.T) {
	src := `<?php
$a = "";
for ($i = 0; $i < 1000; $i++) { $a .= "x"; }
$b = $a;
$a .= "y";
echo strlen($b), " ", ($b === str_repeat("x", 1000) ? "ok" : "bad"), " ", strlen($a), " ", substr($a, -1), "\n";

$c = "";
for ($i = 0; $i < 50; $i++) { $c .= "ab"; }
$arr = [];
$arr[] = $c;
$c .= "ZZZ";
echo $arr[0], "|", substr($c, -3), "|", strlen($arr[0]), "\n";

function f($x) { $x .= "!"; return $x; }
$d = "";
for ($i = 0; $i < 20; $i++) { $d .= "ab"; }
$r = f($d);
echo strlen($d), " ", substr($r, -1), " ", strlen($r), "\n";

function mk() { $s = ""; for ($i = 0; $i < 10; $i++) { $s .= "z"; } return $s; }
$e = mk();
$e .= "R";
echo $e, "\n";

class C { public $p = ""; }
$o = new C;
$g = "";
for ($i = 0; $i < 10; $i++) { $g .= "q"; }
$o->p = $g;
$g .= "R";
echo $o->p, "|", substr($g, -1), "\n";

$s1 = "";
for ($i = 0; $i < 10; $i++) { $s1 .= "b"; }
$s3 = "ccc";
$s2 = $s1 . $s3;
$s1 .= "X";
echo $s2, "|", substr($s1, -1), "\n";

$h = "";
for ($i = 0; $i < 30; $i++) { $h .= "x"; }
$r2 = &$h;
$h .= "y";
echo ($r2 === $h ? "same" : "diff"), " ", strlen($r2), "\n";
`
	want := "1000 ok 1001 y\n" +
		strings.Repeat("ab", 50) + "|ZZZ|100\n" +
		"40 ! 41\n" +
		"zzzzzzzzzzR\n" +
		"qqqqqqqqqq|R\n" +
		"bbbbbbbbbbccc|X\n" +
		"same 31\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("aliasing:\n got %q\nwant %q", got, want)
	}
}

func TestStringBufSelfForeachAndTargets(t *testing.T) {
	src := `<?php
$s = "ab";
for ($i = 0; $i < 5; $i++) { $s .= $s; }
echo $s, "\n";

$arr = ["a", "b", "c"];
foreach ($arr as &$v) { $v .= "X"; }
unset($v);
echo implode(",", $arr), "\n";

$m = ["k" => ""];
for ($i = 0; $i < 50; $i++) { $m["k"] .= "z"; }
class D { public $p = ""; public static $sp = ""; }
$o = new D;
for ($i = 0; $i < 50; $i++) { $o->p .= "y"; }
for ($i = 0; $i < 50; $i++) { D::$sp .= "w"; }
echo strlen($m["k"]), " ", strlen($o->p), " ", strlen(D::$sp), "\n";

$name = "v";
$$name = "";
for ($i = 0; $i < 50; $i++) { ${$name} .= "q"; }
echo strlen($$name), "\n";
`
	want := strings.Repeat("ab", 32) + "\n" +
		"aX,bX,cX\n" +
		"50 50 50\n" +
		"50\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("self/foreach:\n got %q\nwant %q", got, want)
	}
}

func TestStringBufMagicTargets(t *testing.T) {
	src := `<?php
class M { private $d = []; function __get($n) { return array_key_exists($n, $this->d) ? $this->d[$n] : ""; } function __set($n, $v) { $this->d[$n] = $v; } }
$m = new M;
for ($i = 0; $i < 50; $i++) { $m->p .= "x"; }
echo strlen($m->p), " ", $m->p[0], "\n";

class A implements ArrayAccess {
    public $d = [];
    public function offsetExists($o): bool { return isset($this->d[$o]); }
    public function offsetGet($o): mixed { return $this->d[$o] ?? null; }
    public function offsetSet($o, $v): void { $this->d[$o] = $v; }
    public function offsetUnset($o): void { unset($this->d[$o]); }
}
$a = new A;
for ($i = 0; $i < 50; $i++) { $a["k"] .= "y"; }
echo strlen($a["k"]), " ", $a["k"][0], "\n";
`
	want := "50 x\n50 y\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("magic targets:\n got %q\nwant %q", got, want)
	}
}

func TestStringBufRefs(t *testing.T) {
	src := `<?php
$a = "";
for ($i = 0; $i < 40; $i++) { $a .= "x"; }
$b = &$a;
$a .= "y";
echo ($a === $b ? "same" : "diff"), " ", strlen($b), " ", substr($b, -1), "\n";

$c = "q";
$d = &$c;
$d .= "w";
$c .= "e";
echo $c, " ", $d, "\n";

function app(&$x) { for ($i = 0; $i < 40; $i++) { $x .= "ab"; } }
$s = "";
app($s);
echo strlen($s), " ", substr($s, -2), "\n";

$e = "";
for ($i = 0; $i < 30; $i++) { $e .= "m"; }
$arr = [&$e];
$e .= "!";
echo $arr[0], " ", strlen($arr[0]), "\n";
`
	want := "same 41 y\nqwe qwe\n80 ab\n" + strings.Repeat("m", 30) + "! 31\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("refs:\n got %q\nwant %q", got, want)
	}
}

// Цепочки >= 3 частей: OpConcatN. Порядок приведения к строке должен
// совпадать с вложенными OpConcat (__toString и предупреждения слева направо).
func TestStringBufConcatNOrder(t *testing.T) {
	src := `<?php
$c = "C";
$arr = [1];
class T { public function __toString() { echo "["; return "T"; } }
$t = new T;
echo $c . $t . $c, "\n";
echo $c . $arr . $c, "\n";
echo "end\n";
`
	got := runPHPCode(t, src)
	// __toString вызывается при приведении — до печати результата.
	if !strings.Contains(got, "[CTC\n") {
		t.Fatalf("порядок __toString нарушен:\n%s", got)
	}
	if !strings.Contains(got, "CArrayC\n") {
		t.Fatalf("результат цепочки с массивом:\n%s", got)
	}
	// Предупреждение о массиве идёт перед строкой результата.
	iw := strings.Index(got, "Array to string conversion")
	ir := strings.Index(got, "CArrayC")
	if iw < 0 || ir < 0 || iw > ir {
		t.Fatalf("порядок предупреждения нарушен:\n%s", got)
	}
	if !strings.HasSuffix(got, "end\n") {
		t.Fatalf("хвост вывода:\n%s", got)
	}
}

func TestStringBufEmptyAndBig(t *testing.T) {
	src := `<?php
$a = "";
$a .= "";
echo strlen($a), "|", ($a === "" ? "empty" : "no"), "\n";
$b = null;
$b .= "";
echo ($b === "" ? "null-empty" : "bad"), "\n";
$c = null;
$c .= "x";
echo $c, "\n";

$big = str_repeat("x", 1 << 20);
$s = "";
$s .= $big;
$t = $s;
$s .= "y";
echo strlen($t), " ", strlen($s), " ", substr($s, -1), "\n";

$who = "world";
$n = 3;
echo "hello $who #$n end\n";

$u = "";
for ($i = 0; $i < 300; $i++) { $u .= $i . ","; }
echo strlen($u), " ", substr($u, -3), "\n";
`
	want := "0|empty\nnull-empty\nx\n1048576 1048577 y\nhello world #3 end\n1090 99,\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("empty/big:\n got %q\nwant %q", got, want)
	}
}

func TestStringBufMixedReset(t *testing.T) {
	src := `<?php
$parts = [];
$s = "";
for ($i = 0; $i < 2000; $i++) {
    $s .= $i . "-";
    if ($i % 100 == 0) { $parts[] = $s; $s = "reset"; }
}
echo count($parts), " ", strlen($parts[0]), " ", $parts[19], "\n";
const K = "abc";
$k = K;
$k .= "d";
echo $k, "\n";
$s2 = "1" . "2" . "3" . "4" . "5" . "6";
echo $s2, "\n";
`
	var tail strings.Builder
	for i := 1801; i <= 1900; i++ {
		tail.WriteString(strconv.Itoa(i))
		tail.WriteByte('-')
	}
	want := "20 2 reset" + tail.String() + "\nabcd\n123456\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("mixed:\n got %q\nwant %q", got, want)
	}
}

// Буферы в кадрах без SSA-аллокатора (func_get_args), в генераторах и
// замыканиях: копии слота не должны видеть чужие дописывания.
func TestStringBufFramesGeneratorsClosures(t *testing.T) {
	src := `<?php
function noSSA($pre) {
    $a = "x"; $b = "y"; $c = "z";
    $args = func_get_args();
    return $pre . $a . $b . $c . count($args);
}
echo noSSA("p"), "\n";

function gen() {
    $s = "";
    for ($i = 0; $i < 50; $i++) { $s .= "g"; }
    yield $s;
    $s .= "!";
    yield $s;
}
foreach (gen() as $v) { echo strlen($v), " "; }
echo "\n";

$s = "";
for ($i = 0; $i < 20; $i++) { $s .= "c"; }
$f = function () use ($s) { return $s; };
$s .= "Z";
echo $f(), " ", $s, "\n";

$a0 = "0"; $a1 = "1"; $a2 = "2"; $a3 = "3"; $a4 = "4"; $a5 = "5";
$a6 = "6"; $a7 = "7"; $a8 = "8"; $a9 = "9"; $a10 = "A"; $a11 = "B";
$a12 = "C"; $a13 = "D"; $a14 = "E"; $a15 = "F"; $a16 = "G"; $a17 = "H";
$chain = $a0 . $a1 . $a2 . $a3 . $a4 . $a5 . $a6 . $a7 . $a8 . $a9 . $a10 . $a11 . $a12 . $a13 . $a14 . $a15 . $a16 . $a17;
echo $chain, "\n";
`
	want := "pxyz1\n50 51 \n" + strings.Repeat("c", 20) + " " + strings.Repeat("c", 20) + "Z\n0123456789ABCDEFGH\n"
	if got := runPHPCode(t, src); got != want {
		t.Fatalf("frames/generators/closures:\n got %q\nwant %q", got, want)
	}
}

// Компилятор обязан использовать OpConcatN для чистых цепочек и OpConcatApp
// для `.=`.
func TestStringBufOpcodesEmitted(t *testing.T) {
	src := `<?php
function g($a, $b, $c) {
    $s = $a . $b . $c;
    $t = "";
    for ($i = 0; $i < 3; $i++) { $t .= $b . "-"; }
    return $s . $t;
}
g("A", "B", "C");
`
	c, err := CompileFile(src, "t.php")
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, f := range c.Funcs {
		if cf, ok := f.Executable.(interface{ Disassemble() string }); ok {
			body.WriteString(cf.Disassemble())
		}
	}
	if !strings.Contains(body.String(), "CONCAT_N") {
		t.Fatalf("OpConcatN не эмитится:\n%s", body.String())
	}
	if !strings.Contains(body.String(), "CONCAT_APP") {
		t.Fatalf("OpConcatApp не эмитится:\n%s", body.String())
	}
}

// Сравнение с живым php 8.3 (если он есть в PATH) на представительных
// скриптах. Наши предупреждения уходят в stdout, поэтому берём скрипты без
// диагностик.
func TestStringBufMatchesPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	cases := []string{
		`<?php $s=""; for($i=0;$i<5000;$i++){ $s .= $i . "-"; } echo strlen($s), " ", substr($s,-2), "\n";`,
		`<?php $s="ab"; for($i=0;$i<5;$i++){ $s .= $s; } echo $s, "\n";`,
		`<?php $a=["a","b","c"]; foreach($a as &$v){ $v .= "X"; } unset($v); echo implode(",",$a), "\n";`,
		`<?php $s=5; $s .= 1.5; $s .= null; $s .= true; $s .= false; echo $s, "\n";`,
		`<?php $i=42; $f=3.5; $who="w"; echo "i=$i f=$f $who end\n";`,
		`<?php $a="A"; $b="B"; $c="C"; $d="D"; $s=$a.$b.$c.$d; $a.="X"; echo $s, " ", $a, "\n";`,
		`<?php $s=""; $arr=[]; for($i=0;$i<100;$i++){ $s .= "ab"; if($i%10==0){ $arr[]=$s; $s=""; } } echo count($arr), " ", $arr[5], "\n";`,
	}
	for i, src := range cases {
		want, err := exec.Command(phppath, "-r", strings.TrimPrefix(src, "<?php ")).Output()
		if err != nil {
			t.Fatalf("case %d: php: %v", i, err)
		}
		got := runPHPCode(t, src)
		if got != string(want) {
			t.Errorf("case %d:\n got %q\nwant %q", i, got, string(want))
		}
	}
}
