package php

import (
	"bytes"
	"os/exec"
	"testing"
)

// phpRunMBExec выполняет PHP-программу живым php 8.3.
func phpRunMBExec(t *testing.T, src string) []byte {
	t.Helper()
	cmd := exec.Command("php", "-r", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("php -r упал: %v\nstderr=%s\nsrc=%s", err, errb.String(), src)
	}
	return out.Bytes()
}

// phpRunMBVM выполняет ту же программу в интерпретаторе.
func phpRunMBVM(t *testing.T, src string) []byte {
	t.Helper()
	out, err := runFile("<?php\n" + src + "\n?>")
	if err != nil {
		t.Fatalf("phpvm: %v\nsrc=%s", err, src)
	}
	return []byte(out)
}

// TestMBAgentVsPHP — дифференциальные кейсы mb_* против живого php 8.3.
func TestMBAgentVsPHP(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// внутренняя кодировка / список / алиасы
		`var_dump(mb_internal_encoding()); var_dump(mb_internal_encoding('ISO-8859-1')); var_dump(mb_internal_encoding()); mb_internal_encoding('UTF-8');`,
		`echo implode(",", mb_list_encodings());`,
		`var_dump(mb_encoding_aliases('UTF-8'));`,
		`var_dump(mb_encoding_aliases('ASCII'));`,
		`var_dump(mb_encoding_aliases('ISO-8859-1'));`,
		`var_dump(mb_encoding_aliases('CP1251'));`,
		`var_dump(mb_encoding_aliases('UTF-16'));`,

		// check/detect
		`var_dump(mb_check_encoding("Привет","UTF-8"), mb_check_encoding("Hello","ASCII"), mb_check_encoding("Привет","ASCII"));`,
		`$x = mb_convert_encoding("ÿ","ISO-8859-1","UTF-8"); var_dump(bin2hex($x), mb_check_encoding($x,"UTF-8"));`,
		`var_dump(mb_detect_encoding("Привет"));`,
		`var_dump(mb_detect_encoding("Hello"));`,
		`var_dump(mb_detect_encoding("Hello",["UTF-8","ASCII"]));`,
		`$x = mb_convert_encoding("ÿ","ISO-8859-1","UTF-8"); var_dump(mb_detect_encoding($x,["UTF-8","ISO-8859-1"]));`,

		// convert_encoding
		`var_dump(bin2hex(mb_convert_encoding("Привет","ISO-8859-1","UTF-8")));`,
		`var_dump(bin2hex(mb_convert_encoding("ÄÖÜ","ISO-8859-1","UTF-8")));`,
		`var_dump(bin2hex(mb_convert_encoding("Привет","CP1251","UTF-8")));`,
		`var_dump(bin2hex(mb_convert_encoding("Hello","UTF-16BE","UTF-8")));`,
		`var_dump(bin2hex(mb_convert_encoding("Hello","UTF-16LE","UTF-8")));`,
		`var_dump(bin2hex(mb_convert_encoding("Hello","UTF-32BE","UTF-8")));`,
		`var_dump(bin2hex(mb_convert_encoding("Hello","UTF-32LE","UTF-8")));`,
		`var_dump(mb_convert_encoding("Hello","ASCII","UTF-8"));`,
		`var_dump(mb_convert_encoding("Привет","UTF-8","UTF-8"));`,
		`var_dump(mb_convert_encoding(mb_convert_encoding("Привет","CP1251","UTF-8"),"UTF-8","CP1251"));`,
		`var_dump(mb_convert_encoding(mb_convert_encoding("ÄÖÜ","ISO-8859-1","UTF-8"),"UTF-8","ISO-8859-1"));`,
		`var_dump(bin2hex(mb_convert_encoding("Hello","UTF-16","UTF-8")));`,
		`$bom = mb_convert_encoding("ÿþ","ISO-8859-1","UTF-8"); $h = mb_convert_encoding("Hi","UTF-16LE","UTF-8"); var_dump(mb_convert_encoding($bom.$h,"UTF-8","UTF-16"));`,
		`$bom = mb_convert_encoding("þÿ","ISO-8859-1","UTF-8"); $h = mb_convert_encoding("Hi","UTF-16BE","UTF-8"); var_dump(mb_convert_encoding($bom.$h,"UTF-8","UTF-16"));`,
		`var_dump(mb_convert_encoding(["a"=>"Привет","b"=>"ok"],"CP1251","UTF-8"));`,

		// strlen / substr
		`var_dump(mb_strlen("Привет"), mb_strlen("a😀b"), mb_strlen("Hello"));`,
		`var_dump(mb_substr("Привет",1,3), mb_substr("Привет",-2), mb_substr("Привет",-4,2));`,
		`var_dump(mb_substr("Привет",0,-1), mb_substr("Привет",2), mb_substr("Привет",10));`,
		`var_dump(mb_substr("Привет",2,100));`,

		// позиции
		`var_dump(mb_strpos("Привет мир","мир"));`,
		`var_dump(mb_strrpos("Привет мир мир","мир"));`,
		`var_dump(mb_strpos("Привет","x"));`,
		`var_dump(mb_stripos("Привет Мир","мир"));`,
		`var_dump(mb_strripos("Привет МИР Мир","мир"));`,
		`var_dump(mb_strpos("Привет мир","мир",2), mb_strpos("Привет мир","мир",8));`,

		// регистр
		`var_dump(mb_strtolower("ПРИВЕТ ÄÖÜ"), mb_strtoupper("привет äöü"));`,
		`var_dump(mb_convert_case("привет мир",0));`,
		`var_dump(mb_convert_case("ПРИВЕТ МИР",1));`,
		`var_dump(mb_convert_case("привет мир",2));`,
		`var_dump(mb_convert_case("foo-bar baz",2));`,
		`var_dump(mb_convert_case("o'neill o'brien",2));`,

		// split
		`var_dump(mb_str_split("Привет",2));`,
		`var_dump(mb_str_split("Привет"));`,
		`var_dump(mb_str_split("abc"));`,

		// ord / chr
		`var_dump(mb_ord("П"), mb_ord("😀"), mb_chr(1055), mb_chr(128512));`,

		// substr_count
		`var_dump(mb_substr_count("Привет мир мир","мир"), mb_substr_count("aaa","a"));`,

		// strstr / stristr / strrchr
		`var_dump(mb_strstr("Привет мир","ир"));`,
		`var_dump(mb_strstr("Привет мир","ир",true));`,
		`var_dump(mb_strstr("Привет","x"));`,
		`var_dump(mb_stristr("Привет МИР","мир"));`,
		`var_dump(mb_strrchr("Привет/мир/тест","/"));`,

		// ширина
		`var_dump(mb_strwidth("Hello"), mb_strwidth("Привет"), mb_strwidth("日本語"), mb_strwidth("a😀b"), mb_strwidth("Ａ"), mb_strwidth("ｱ"));`,
		`var_dump(mb_strimwidth("Hello World",0,5));`,
		`var_dump(mb_strimwidth("Hello World",0,5,"..."));`,
		`var_dump(mb_strimwidth("Привет мир",0,6));`,
		`var_dump(mb_strimwidth("Привет мир",0,6,".."));`,
		`var_dump(mb_strimwidth("日本語テスト",0,6,".."));`,
		`var_dump(mb_strimwidth("Привет мир",-1,6,".."));`,

		// convert_variables
		`$s="Привет"; $r=mb_convert_variables("ISO-8859-1","UTF-8",$s); var_dump($r, bin2hex($s));`,
		`$a=["k"=>"Привет","arr"=>["внутри"=>"мир"],5=>"тест"]; $r=mb_convert_variables("CP1251","UTF-8",$a); var_dump($r, bin2hex($a["k"]), bin2hex($a["arr"]["внутри"]));`,
	}
	for _, src := range cases {
		want := phpRunMBExec(t, src)
		got := phpRunMBVM(t, src)
		if !bytes.Equal(got, want) {
			t.Errorf("mb mismatch:\nsrc  = %s\n vm  = %q\n php = %q", src, got, want)
		}
	}
}
