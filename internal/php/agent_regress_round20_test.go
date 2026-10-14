package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// agent_regress_round20_test.go — регрессии раунда 20: unserialize
// (R:-приёмники, пометка -1 у Serializable, изменение видимости свойств,
// верхнеуровневый R:1), диагностики eval, Directory/scandir/chdir, сетевые
// тексты и UDP, forward_static_call (late static binding), by-ref warning
// call_user_func*, final __PHP_Incomplete_Class и имена анонимных классов.
// Сравнение с живым php 8.3 — байт в байт.

func r20RunLive(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", "-d", "zend.exception_ignore_args=0", f).CombinedOutput()
	res := strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
	res = strings.ReplaceAll(res, dir, "")
	return res
}

func r20Check(t *testing.T, name, src string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil && !vm.IsExitSignal(err) {
		t.Fatalf("%s: run error: %v", name, err)
	}
	want := r20RunLive(t, src)
	if got != want {
		t.Errorf("%s:\n--- src ---\n%s\n--- got ---\n%q\n--- want ---\n%q", name, src, got, want)
	}
}

func TestRegress20VsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"scandir_empty", `<?php
try { scandir(""); } catch (ValueError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
`},
		{"chdir_errno", `<?php
echo "start\n";
var_dump(chdir(__DIR__ . "/no_such_dir"));
`},
		{"directory_read_closed", `<?php
$d = dir(__DIR__);
$d->read();
$d->rewind();
$d->read();
$d->close();
try { $d->read(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(is_resource($d->handle));
`},
		{"unserialize_ref_dest_alias", `<?php
var_dump(@unserialize('a:2:{i:0;i:1;i:0;R:2;}'));
var_dump(@unserialize('a:2:{i:0;i:4;i:1;i:5;i:2;R:4;}'));
var_dump(unserialize('a:3:{i:0;i:4;i:1;i:2;i:2;R:3;}'));
`},
		{"unserialize_ref_incomplete_offset", `<?php
var_dump(@unserialize('a:7:{i:0;i:04;s:1:"a";i:2;i:9617006;i:4;s:1:"a";i:4;s:1:"a";R:5;s:1:"7";R:3;s:1:"a";R:5;;s:18;}}'));
`},
		{"unserialize_top_ref_upgrade", `<?php
class obj { var $ryat; function __wakeup() { $this->ryat = 0x1122334455; } }
var_dump(unserialize('O:8:"stdClass":1:{i:0;O:3:"obj":1:{s:4:"ryat";R:1;}}'));
`},
		{"serialize_failed_serializable_ref", `<?php
class UnSerializable implements Serializable {
  public function serialize() {}
  public function unserialize($serialized) {}
}
$unser = new UnSerializable();
$arr = [$unser];
$arr[1] = &$arr[0];
$arr[2] = 'endcap';
$arr[3] = &$arr[2];
$data = serialize($arr);
echo $data, "\n";
var_export(unserialize($data));
echo "\n";
`},
		{"unserialize_visibility_change", `<?php
$serialized = 'TzozOiJGb28iOjQ6e3M6MTI6IgBGb28AcHJpdmF0ZSI7aToxO3M6MTI6IgAqAHByb3RlY3RlZCI7aToyO3M6NjoicHVibGljIjtpOjM7czo4OiJub3RUaGVyZSI7czozOiJvbGQiO30';
class Foo { private $public = null; private $protected = null; private $private = null; }
var_dump(unserialize(base64_decode($serialized)));
`},
		{"eval_serializable_deprecation", `<?php
function f($s) { eval($s); }
class Base implements Serializable {
  public function serialize() { return ""; }
  public function unserialize($s) {}
}
f('class Child extends Base {}');
var_dump(class_exists('Child'));
`},
		{"fsc_chain_related", `<?php
class A { const NAME='A'; static function test(){ echo static::NAME, "\n"; } }
class B extends A { const NAME='B'; static function test(){ echo self::NAME, "\n"; forward_static_call(array('parent','test')); }
  static function t2(){ echo self::NAME, "\n"; forward_static_call(array('self','test')); } }
class C extends B { const NAME='C'; static function test(){ echo self::NAME, "\n"; forward_static_call(array('A','test')); } }
B::test(); echo "-\n"; B::t2(); echo "-\n"; C::test(); echo "-\n"; C::t2();
`},
		{"fsc_chain_unrelated", `<?php
class A { const NAME='A'; static function test(){ echo static::NAME, "\n"; } }
class B extends A { const NAME='B'; static function test(){ echo self::NAME, "\n"; forward_static_call(array('parent','test')); } }
class D { const NAME='D'; static function test(){ echo self::NAME, "\n"; forward_static_call(array('B','test')); } }
D::test();
`},
		{"call_user_func_byref_warning", `<?php
$a = array(4,3,2);
var_dump(call_user_func_array("sort", array($a)));
var_dump(call_user_func("sort", $a));
var_dump($f = "sort" ? "ok" : "bad");
`},
		{"call_user_func_named_no_byref_warning", `<?php
try { call_user_func_array("array_multisort", ["" => 1]); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
`},
		{"anon_class_vardump_iface", `<?php
class Test { public $prop; }
$o = new class implements JsonSerializable {
    public function jsonSerialize(): mixed { return null; }
};
var_dump($o);
`},
		{"incomplete_class_final", `<?php
$c = new class('bar') extends __PHP_Incomplete_Class {
};
`},
		{"fsockopen_errors", `<?php
var_dump(fsockopen("127.0.0.1", 31337, $e1, $s1, 2));
echo $e1, ": ", $s1, "\n";
var_dump(fsockopen("invalid://127.0.0.1:31337", -1, $e2, $s2, 2));
echo $e2, ": ", $s2, "\n";
var_dump(fsockopen("localhost\0.example.com", 31337, $e3, $s3, 2));
echo $e3, ": ", $s3, "\n";
`},
		{"stream_socket_client_dns", `<?php
$fp = stream_socket_client("tcp://host.invalid:80", $e, $s, 2);
var_dump($fp);
echo $e, ": ", $s, "\n";
`},
		{"socket_client_port_suffix", `<?php
$ln = stream_socket_server("tcp://127.0.0.1:0", $e, $s);
$name = stream_socket_get_name($ln, false);
$port = substr(strrchr($name, ":"), 1);
$c = stream_socket_client("tcp://127.0.0.1:" . $port . "/client0", $e2, $s2, 2);
var_dump(is_resource($c));
fclose($c);
fclose($ln);
`},
		{"udp_streams", `<?php
$srv = null;
for ($port = 31339; $port < 31500; ++$port) {
    $uri = "udp://127.0.0.1:$port";
    $srv = @stream_socket_server($uri, $errno, $errstr, STREAM_SERVER_BIND);
    if ($srv) break;
}
if (!$srv) { die("no server\n"); }
$cli = fsockopen("udp://127.0.0.1", $port);
var_dump(is_resource($cli));
fwrite($cli, "0123456789");
var_dump(fread($srv, 10));
fclose($cli);
fclose($srv);
`},
		{"udp_read_failure", `<?php
$fp = fsockopen("udp://127.0.0.1", 39999, $errno, $errstr, 1);
$x = fwrite($fp, "\n");
var_dump($x);
var_dump(fread($fp, 40));
fclose($fp);
`},
		{"closure_method_scope_name", `<?php
class foo {
    public function bar() : callable {
        return function() : array { return null; };
    }
}
$baz = new foo();
try { $baz->bar()(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; echo $e->getTraceAsString(), "\n"; }
`},
		{"unknown_wrapper_fallback", `<?php
file_put_contents(__DIR__ . "/r20_data.txt", "test data");
chdir(__DIR__);
var_dump(file_exists("foobar://google.com/../../r20_data.txt"));
var_dump(file_get_contents("foobar://google.com/../../r20_data.txt"));
var_dump(file_exists("foobar://no_way/../../nope.txt"));
@unlink(__DIR__ . "/r20_data.txt");
`},
		{"dynamic_call_no_byref_warning", `<?php
$a = array(4,3,2);
$f = "sort";
var_dump($f($a));
`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { r20Check(t, c.name, c.src) })
	}
}
