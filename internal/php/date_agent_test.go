package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDateAgentVsPHP — дифференциальные кейсы даты/времени, JSON, URL и
// прочих мелких встроенных функций (dateAgent). Сравнение с живым php 8.3.
func TestDateAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- date / gmdate ---
		`<?php date_default_timezone_set("UTC"); echo date("Y-m-d H:i:s", 1700000000);`,
		`<?php echo gmdate("Y-m-d H:i:s", 1700000000);`,
		`<?php date_default_timezone_set("UTC"); echo date("d D j l N S w z W F m M n t L o Y y a A g G h H i s u v I O P T Z c r U B", 1700000000);`,
		`<?php date_default_timezone_set("UTC"); echo date("Y-m-d H:i:s T O P e", 1700000000);`,
		`<?php date_default_timezone_set("Europe/Moscow"); echo date("Y-m-d H:i:s T O P e I Z", 1700000000);`,
		`<?php date_default_timezone_set("Europe/Moscow"); echo date("Y-m-d H:i:s", 1688000000);`,
		`<?php date_default_timezone_set("UTC"); echo date("L t", 1609459200), "|", date("L t", 1583020800);`,
		`<?php date_default_timezone_set("UTC"); echo date("Y-m-d\TH:i:sP", 1000000000);`,
		`<?php date_default_timezone_set("UTC"); echo date("Y-m-d", 0), "|", date("Y-m-d", -1);`,

		// --- idate ---
		`<?php date_default_timezone_set("UTC"); echo idate("Y", 1700000000), "|", idate("z", 1700000000), "|", idate("W", 1700000000), "|", idate("B", 1700000000), "|", idate("N", 1700000000), "|", idate("w", 1700000000);`,
		`<?php date_default_timezone_set("UTC"); echo idate("h", 1700000000), "|", idate("G", 1700000000), "|", idate("I", 1700000000), "|", idate("Z", 1700000000), "|", idate("U", 1700000000), "|", idate("L", 1609459200);`,
		`<?php date_default_timezone_set("UTC"); var_dump(idate("Ymd", 1700000000));`,

		// --- mktime / gmmktime ---
		`<?php date_default_timezone_set("UTC"); echo mktime(0,0,0,1,1,2000), "|", mktime(25,70,70,13,40,2000);`,
		`<?php echo gmmktime(0,0,0,1,1,2000), "|", gmmktime(12,30,0,6,15,2020);`,
		`<?php date_default_timezone_set("Europe/Moscow"); echo mktime(0,0,0,1,1,2000);`,
		`<?php date_default_timezone_set("UTC"); echo mktime(0,0,0,1,1,1970);`,

		// --- checkdate ---
		`<?php var_dump(checkdate(2,29,2000), checkdate(2,29,1900), checkdate(2,30,2000), checkdate(0,1,2000), checkdate(1,1,0), checkdate(1,1,32768), checkdate(12,31,2020));`,

		// --- getdate / localtime ---
		`<?php date_default_timezone_set("UTC"); print_r(getdate(1700000000));`,
		`<?php date_default_timezone_set("Europe/Moscow"); print_r(getdate(1700000000));`,
		`<?php date_default_timezone_set("UTC"); print_r(localtime(1700000000));`,
		`<?php date_default_timezone_set("UTC"); print_r(localtime(1700000000, true));`,
		`<?php date_default_timezone_set("Europe/Moscow"); print_r(localtime(1700000000));`,

		// --- timezone ---
		`<?php date_default_timezone_set("UTC"); echo date_default_timezone_get();`,
		`<?php var_dump(date_default_timezone_set("Europe/Moscow")); echo date_default_timezone_get();`,
		`<?php date_default_timezone_set("UTC"); var_dump(date_default_timezone_set("Bad/Zone"));`,

		// --- strtotime ---
		`<?php date_default_timezone_set("UTC"); echo strtotime("2020-01-02 03:04:05"), "|", strtotime("2020-01-02"), "|", strtotime("@1700000000");`,
		`<?php date_default_timezone_set("UTC"); echo strtotime("+1 day", 1700000000), "|", strtotime("-2 hours", 1700000000), "|", strtotime("+1 week", 1700000000), "|", strtotime("+3 months", 1700000000);`,
		`<?php date_default_timezone_set("UTC"); echo strtotime("tomorrow", 1700000000), "|", strtotime("yesterday", 1700000000), "|", strtotime("midnight", 1700000000), "|", strtotime("noon", 1700000000), "|", strtotime("now", 1700000000);`,
		`<?php date_default_timezone_set("UTC"); echo strtotime("2020/01/02 10:00:00"), "|", strtotime("2 Jan 2020");`,
		`<?php date_default_timezone_set("UTC"); var_dump(strtotime("not a date"));`,

		// --- date_parse ---
		`<?php date_default_timezone_set("UTC"); print_r(date_parse("2020-01-02 03:04:05.123"));`,
		`<?php date_default_timezone_set("UTC"); print_r(date_parse("2020-01-02"));`,

		// --- json_encode ---
		`<?php echo json_encode([1,2,3]), "|", json_encode(["a"=>1,"b"=>[2,3]]), "|", json_encode("a/b<c>");`,
		`<?php echo json_encode("héllo"), "|", json_encode("héllo", JSON_UNESCAPED_UNICODE), "|", json_encode("a/b", JSON_UNESCAPED_SLASHES);`,
		`<?php echo json_encode([1,2], JSON_PRETTY_PRINT), "\n", json_encode(["a"=>[1,2],"b"=>[]], JSON_PRETTY_PRINT);`,
		`<?php echo json_encode(1.0), "|", json_encode(1.0, JSON_PRESERVE_ZERO_FRACTION), "|", json_encode(0.1), "|", json_encode(1e20), "|", json_encode(-0.0);`,
		`<?php echo json_encode([]), "|", json_encode(["x"=>1], JSON_FORCE_OBJECT), "|", json_encode([1=>2]), "|", json_encode([0,2]);`,
		`<?php $s = "<a>&".chr(39).chr(34); $f = 1+2+4+8; echo json_encode($s), "|", json_encode($s, $f);`,
		`<?php $s = "a".chr(0xE2).chr(0x80).chr(0xA8)."b"; echo json_encode(chr(1).chr(31)), "|", json_encode($s), "|", json_encode($s, JSON_UNESCAPED_UNICODE);`,
		`<?php var_dump(json_encode(NAN), json_last_error(), json_last_error_msg());`,

		// --- json_decode ---
		`<?php var_dump(json_decode("[1,2,3]"), json_decode("{\"a\":1}", true));`,
		`<?php var_dump(json_decode("1e3"), json_decode("123"), json_decode("true"), json_decode("null"), json_decode("\"x\""));`,
		`<?php $o = json_decode("{\"a\":1,\"b\":[2,3]}"); var_dump(is_object($o), get_class($o), $o->a, $o->b[1]);`,
		`<?php echo json_encode(json_decode("{\"b\":1,\"a\":2}")), "|", json_encode(json_decode("[1,[2,{\"x\":3}]]"));`,
		`<?php var_dump(json_decode("{bad"), json_last_error(), json_last_error_msg());`,
		`<?php var_dump(json_decode("9999999999999999999999"));`,

		// --- parse_url ---
		`<?php var_export(parse_url("http://user:pass@example.com:8080/path/to?x=1&y=2#frag"));`,
		`<?php var_export(parse_url("//example.com/path")); var_export(parse_url("https://example.com"));`,
		`<?php var_export(parse_url("/just/path")); var_export(parse_url("mailto:a@b.com")); var_export(parse_url("user@host:80"));`,
		`<?php var_export(parse_url("http://user:@host")); var_export(parse_url("http://[::1]:8080/p")); var_dump(parse_url("http://:80"));`,
		`<?php var_dump(parse_url("http://user:pass@example.com:8080/p", PHP_URL_HOST), parse_url("/p", PHP_URL_PATH), parse_url("http://h", PHP_URL_PORT));`,
		`<?php var_export(parse_url("example.com:8080")); var_export(parse_url("1abc:x")); var_export(parse_url("http://host:-1"));`,

		// --- http_build_query ---
		`<?php echo http_build_query(["a"=>1,"b"=>"x y","c"=>["d"=>2,"e"=>[1,2]],"f"=>null,"g"=>true]);`,
		`<?php echo http_build_query([1,2,3], "n_"), "|", http_build_query([0=>"x",3=>"y"]), "|", http_build_query([5=>"x"],"p_");`,
		`<?php echo http_build_query(["x"=>["y"=>["z"=>1]]]), "|", http_build_query(["a b"=>"c d"], "", "&", PHP_QUERY_RFC3986);`,
		`<?php var_dump(http_build_query([]));`,

		// --- константы ---
		`<?php echo defined("DATE_ATOM"), defined("JSON_PRETTY_PRINT"), defined("PHP_URL_HOST"), defined("CRYPT_MD5"), defined("JSON_ERROR_SYNTAX"), "|", DATE_ATOM, "|", JSON_UNESCAPED_UNICODE, "|", PHP_URL_FRAGMENT;`,

		// --- serialize (базовые) ---
		`<?php echo serialize([1,"a"=>2.5,true,null,"s"]);`,
		`<?php var_dump(unserialize(serialize(["x"=>[1,2],"y"=>"z"])));`,
		`<?php echo serialize(["x"=>[1,2],"y"=>"z"]);`,
		`<?php var_dump(unserialize('a:2:{s:1:"x";a:2:{i:0;i:1;i:1;i:2;}s:1:"y";s:1:"z";}'));`,

		// --- json-объекты: доступ к свойствам и вложенность ---
		`<?php $o = json_decode("{\"a\":{\"b\":5},\"c\":\"s\"}"); var_dump($o->a->b, $o->c, count((array)$o));`,
		`<?php $o = json_decode("{\"n\":1,\"arr\":[1,2,3]}"); echo json_encode($o->arr), "|", json_encode($o->n);`,
		`<?php var_dump(array_keys((array)json_decode("{\"x\":1}")));`,

		// --- json_encode пользовательского объекта ---
		`<?php class C { public $a = 1; } var_dump(json_encode(new C));`,
		`<?php $o = new stdClass; $o->k = "v"; echo json_encode($o), "|", http_build_query($o);`,

		// --- parse_url: крайние случаи ---
		`<?php var_export(parse_url("http://host?")); var_export(parse_url("http://host?#")); var_export(parse_url("?x=1")); var_export(parse_url("#f"));`,
		`<?php var_export(parse_url("///p")); var_dump(parse_url("")); var_export(parse_url("http:/x"));`,

		// --- прочие мелкие ---
		`<?php var_dump(gettype(1), gettype(1.5), gettype("x"), gettype([1]), gettype(null), gettype(true), gettype(new stdClass));`,
		`<?php echo json_encode([10,20]), "|", json_encode(["k"=>null]), "|", json_encode(true), "|", json_encode(null);`,
		`<?php echo json_encode(["1","1.5","abc","1e2","0x1"]), "|", json_encode(["1","1.5","abc","1e2","0x1"], JSON_NUMERIC_CHECK), "|", json_encode("123", JSON_NUMERIC_CHECK);`,
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
