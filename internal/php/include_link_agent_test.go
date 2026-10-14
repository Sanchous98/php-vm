package php

import (
	"path/filepath"
	"testing"
)

func TestIncludeLinkedIncludeClassesGlobals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.inc"),
		"<?php\n"+
			"const MY_CONST = 7;\n"+
			"$GLOB = 'gv';\n"+
			"function lib_fn() { return 'fn'; }\n"+
			"class LibClass { public $v = 1; public function m() { return 'm'; } public static function s() { return 's'; } }\n")
	main := filepath.Join(dir, "main.php")
	src := "<?php\n" +
		"$p = __DIR__ . '/lib.inc';\n" +
		"include $p;\n" +
		"echo lib_fn();\n" + // functions visible
		"$o = new LibClass();\n" + // class visible via new
		"echo $o->m();\n" +
		"echo LibClass::s();\n" + // static call
		"var_dump($o instanceof LibClass);\n" + // instanceof
		"echo $GLOB;\n" + // global var visible
		"echo MY_CONST;\n"
	got, err := runFileAt(src, main)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "fnmsbool(true)\ngv7"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestIncludeLinkedIncludeClosureAndNested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.inc"),
		"<?php\nfunction a_fn() { return 'A'; }\n")
	writeFile(t, filepath.Join(dir, "b.inc"),
		"<?php\nfunction b_fn() { return a_fn() . 'B'; }\nrequire(__DIR__.'/a.inc');\n")
	main := filepath.Join(dir, "main.php")
	src := "<?php\nrequire(__DIR__.'/b.inc');\necho b_fn();\n"
	got, err := runFileAt(src, main)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "AB" {
		t.Fatalf("got %q", got)
	}
}
