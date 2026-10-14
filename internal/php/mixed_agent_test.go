package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMixedPHP записывает исходник во временный .php и сравнивает вывод
// интерпретатора с живым php 8.3.
func runMixedPHP(t *testing.T, src string) string {
	t.Helper()
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "case.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, f).Output()
	if werr != nil {
		t.Fatalf("php %q: %v", src, werr)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm %q: run: %v", src, err)
	}
	if got != string(want) {
		t.Errorf("mixed %q:\n vm  = %q\n php = %q", src, got, string(want))
	}
	return got
}

// TestMixedModeVsPHP — смешанный PHP/HTML: литеральный HTML, несколько
// блоков, `<?=`, `?>` в строках/комментариях, heredoc.
func TestMixedModeVsPHP(t *testing.T) {
	cases := []string{
		`text<?php echo "A"; ?>text2`,
		`<?php echo "A"; echo "B"; ?>`,
		`<?= 1+2 ?>`,
		`<?= 1+2 ?>tail`,
		`<?php $x = "?>"; echo $x; ?>`,
		`A<?php echo 1; ?>B<?php echo 2; ?>C`,
		`<?php echo "x"; // comment ?>tail`,
		"<?php echo 1; # comment ?>tail",
		`<?php echo 1; /* block ?> still comment */ echo 2;`,
		"<html>\n<?php echo \"a\"; ?>\nmore\n",
		"<?php echo 1; ?>\n<?php echo 2; ?>",
		"<?php if (false) { ?>no<?php } else { ?>yes<?php } ?>",
		"<?php for ($i=0;$i<3;$i++){ ?>x<?php } ?>",
		"<?php\n$s = <<<EOT\nhas ?> inside\nEOT;\necho $s;\n?>",
		"<?php echo \"h\"; __halt_compiler(); data ?> not php",
		"plain html only, no php at all",
		"<?php echo \"one\"; ?>\n<?= \"two\" ?>\n<?php echo \"three\"; ?>",
		"<?PHP echo \"upper\"; ?>",
		"#!php\n<?php\necho __LINE__;\n?>",
		"#!/usr/bin/php\nplain text\n<?php echo \"z\"; ?>",
		"<?php echo 1 ?><?php echo 2 ?><?= 3 ?>",
		"<?php $a = 1 ?><?php $b = 2 ?><?= $a+$b ?>",
	}
	for _, src := range cases {
		runMixedPHP(t, src)
	}
}

// TestMixedIncludeHTML — include файла со смешанным PHP/HTML (023-подобный).
func TestMixedIncludeHTML(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	dir := t.TempDir()
	inc := "<b>head</b>\n<?php $a = 1; ?>\nmid<?= $a ?>\n<?php echo \"tail\"; ?>\n"
	main := "<?php include(\"inc.php\"); ?>\nafter\n"
	if err := os.WriteFile(filepath.Join(dir, "inc.php"), []byte(inc), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.php")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, mainPath).Output()
	if werr != nil {
		t.Fatalf("php: %v", werr)
	}
	got, err := runFileAt(main, mainPath)
	if err != nil {
		t.Fatalf("vm run: %v", err)
	}
	if got != string(want) {
		t.Errorf("include mixed:\n vm  = %q\n php = %q", got, string(want))
	}
}

// TestHaltCompiler — всё после __halt_compiler() игнорируется.
func TestHaltCompiler(t *testing.T) {
	src := "<?php echo \"before\"; __halt_compiler(); <?php echo \"after\"; ?> data"
	got, err := runFileAt(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "before" {
		t.Errorf("got %q want %q", got, "before")
	}
	// без данных и без закрывающего тега
	got, err = runFileAt("<?php echo \"x\";__halt_compiler();", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "x" {
		t.Errorf("got %q want %q", got, "x")
	}
}

// TestMultipleBlocks — несколько блоков и текст до/после.
func TestMultipleBlocks(t *testing.T) {
	src := "one<?php echo 1; ?>two<?php echo 2; ?>three<?= 3 ?>"
	got, err := runFileAt(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "one1two2three3" {
		t.Errorf("got %q want %q", got, "one1two2three3")
	}
}

// TestRequirePlainText — require файла без PHP-тегов: всё содержимое — HTML
// (аналог Zend/tests/bug60771).
func TestRequirePlainText(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	dir := t.TempDir()
	txt := strings.Repeat("passed, ", 1024)
	if err := os.WriteFile(filepath.Join(dir, "data.php"), []byte(txt), 0o644); err != nil {
		t.Fatal(err)
	}
	main := "<?php\nrequire('data.php');\n"
	mainPath := filepath.Join(dir, "main.php")
	if err := os.WriteFile(mainPath, []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, mainPath).Output()
	if werr != nil {
		t.Fatalf("php: %v", werr)
	}
	got, err := runFileAt(main, mainPath)
	if err != nil {
		t.Fatalf("vm run: %v", err)
	}
	if got != string(want) {
		t.Errorf("plain-text require:\n vm  = %q\n php = %q", clip(got), clip(string(want)))
	}
}
