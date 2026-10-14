package php

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestFSVsPHP — дифференциальные кейсы файловых встроенных функций
// (fsAgent). Сравнение вывода с живым php 8.3.
func TestFSVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	base := t.TempDir()
	cases := []string{
		// --- dirname / basename ---
		`<?php var_dump(dirname("/a/foo//symlink.link"), dirname("//foo//bar//"), dirname("a//b"), dirname("//"), dirname("."));`,
		`<?php var_dump(basename("/a/foo//bar//"), basename("/"), basename("//"), basename("a/b.txt", ".txt"));`,
		// --- pathinfo ---
		`<?php var_dump(pathinfo("foo"));`,
		`<?php var_dump(pathinfo(".bashrc"));`,
		`<?php var_dump(pathinfo("foo."));`,
		`<?php var_dump(pathinfo("a/b.txt"));`,
		`<?php var_dump(pathinfo(".hidden.txt"));`,
		`<?php var_dump(pathinfo("/a/b/"));`,
		`<?php var_dump(pathinfo("foo", PATHINFO_EXTENSION), pathinfo("foo", PATHINFO_FILENAME));`,
		// --- fnmatch ---
		`<?php var_dump(fnmatch("*.txt","a.txt"), fnmatch("*.TXT","a.txt",FNM_CASEFOLD), fnmatch("a?c","abc"), fnmatch("[a-c]x","bx"), fnmatch("a/b","a/b",FNM_PATHNAME));`,
		`<?php var_dump(fnmatch(".x",".x",FNM_PERIOD), fnmatch("*",".x",FNM_PERIOD));`,
		// --- fputcsv / fgetcsv round-trip (php://temp) ---
		`<?php $f=fopen("php://temp","w+"); fputcsv($f,["a","b c","d,e","f\"g",""," x","tab\there","nl\nhere"]); rewind($f); echo stream_get_contents($f);`,
		`<?php $f=fopen("php://temp","w+"); fputcsv($f,["aaa","\"bbb\"",'x\y','z'],",","\"","\n","\\"); rewind($f); echo stream_get_contents($f);`,
		// --- fgetc / fgets / feof / fseek / ftell / rewind / fpassthru ---
		`<?php $f=fopen("php://temp","w+"); fwrite($f,"abcdef"); rewind($f); var_dump(fread($f,3)); var_dump(ftell($f)); fseek($f,0); var_dump(fgets($f,3)); fseek($f,1); var_dump(fgetc($f)); rewind($f); var_dump(fpassthru($f));`,
		// --- file_get_contents / file_put_contents ---
		`<?php $p="@TMP@/fsagent_a.txt"; file_put_contents($p,"hello\nworld"); var_dump(file_get_contents($p)); var_dump(file_get_contents($p,false,null,6)); var_dump(file_get_contents($p,false,null,-5)); var_dump(filesize($p)); var_dump(file($p)); var_dump(file($p, FILE_IGNORE_NEW_LINES)); unlink($p);`,
		`<?php $p="@TMP@/fsagent_b.txt"; file_put_contents($p,"abc"); file_put_contents($p,"def",FILE_APPEND); var_dump(file_get_contents($p)); var_dump(file_exists($p)); var_dump(is_file($p)); unlink($p);`,
		// --- stat / filetype / fileperms ---
		`<?php $p="@TMP@/fsagent_c.txt"; file_put_contents($p,"xyz"); $s=stat($p); var_dump($s[0]===$s["dev"], $s[7], $s["size"], filetype($p), filesize($p)); var_dump(filetype("@TMP@")); unlink($p);`,
		// --- mkdir / rmdir / rename / copy / touch / realpath / symlink / readlink ---
		`<?php $d="@TMP@/fsagent_dir"; @rmdir($d); var_dump(mkdir($d)); var_dump(is_dir($d)); var_dump(realpath($d)!=false); rmdir($d); var_dump(is_dir($d));`,
		`<?php $a="@TMP@/fsagent_lnk_a"; $b="@TMP@/fsagent_lnk_b"; @unlink($a); @unlink($b); file_put_contents($b,"q"); symlink($b,$a); var_dump(is_link($a), readlink($a)); var_dump(copy($b,$b)); unlink($a); unlink($b);`,
		`<?php $a="@TMP@/fsagent_mv_a"; $b="@TMP@/fsagent_mv_b"; @unlink($a); @unlink($b); file_put_contents($a,"m"); var_dump(rename($a,$b), file_exists($a), file_exists($b)); unlink($b);`,
		// --- sys_get_temp_dir ---
		`<?php var_dump(sys_get_temp_dir()!=="");`,
		// --- glob ---
		`<?php $d="@TMP@/fsagent_glob"; @mkdir($d); file_put_contents("$d/a.txt",""); file_put_contents("$d/b.php",""); $r=glob("$d/*.t?t"); var_dump($r); unlink("$d/a.txt"); unlink("$d/b.php"); rmdir($d);`,
	}
	for _, src0 := range cases {
		src := strings.ReplaceAll(src0, "@TMP@", base)
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Logf("php error for %q: %v", src, werr)
			continue
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q:\n  run error: %v", src, err)
			continue
		}
		if got != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}

// TestFSPhptDebug — ручной прогон одного .phpt (задаётся FS_PHPT) с полным
// сравнением вывода VM и живого php. Нужен для отладки, по умолчанию скипается.
func TestFSPhptDebug(t *testing.T) {
	p := os.Getenv("FS_PHPT")
	if p == "" {
		t.Skip("FS_PHPT не задан")
	}
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parsePhpt(string(data))
	if parsed.File == "" {
		t.Skip("нет FILE")
	}
	dir := strings.TrimSuffix(p, "/"+filepathBase(p))
	debugPHP := dir + "/_debug_run.php"
	if err := os.WriteFile(debugPHP, []byte(parsed.File), 0644); err != nil {
		t.Fatal(err)
	}
	want, _ := exec.Command(phppath, debugPHP).CombinedOutput()
	got, gerr := runFileAt(parsed.File, p)
	if gerr != nil {
		t.Logf("vm run error: %v", gerr)
	}
	t.Logf("=== VM ===\n%s\n=== PHP ===\n%s", got, string(want))
	if got != string(want) {
		t.Errorf("mismatch")
	}
}

func filepathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// TestFGetCSVVsPHP — отдельно fgetcsv на фиксированных данных.
func TestFGetCSVVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	data := "\"One\",\"\\\"Two\\\"\",\"Three\\\"\",\"Four\",\"\\\\\",\"x\"\n" +
		"6,7,8,\"line2\nline3\"\n" +
		"aaa,\"bbb\"\n"
	base := t.TempDir()
	p := base + "/fsagent_csv.txt"
	if err := os.WriteFile(p, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	src := `<?php $f=fopen("` + p + `","r"); while(($r=fgetcsv($f,1024))!==false){ var_dump($r); } fclose($f);`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, "-r", strings.TrimPrefix(src, "<?php ")).Output()
	if werr != nil {
		t.Skipf("php error: %v", werr)
	}
	if got != string(want) {
		t.Errorf("fgetcsv:\n vm  = %q\n php = %q", got, string(want))
	}
}
