package php

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile — вспомогательная запись файла в тестовом каталоге.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestIncludeLiteral проверяет include литерального файла и возврат значения.
func TestIncludeLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "echo.inc"), "<?php\necho \"Hello\";\n")
	writeFile(t, filepath.Join(dir, "ret.inc"), "<?php\nreturn 42;\n")
	writeFile(t, filepath.Join(dir, "val.inc"), "<?php\nreturn [1, 2, 3];\n")

	main := filepath.Join(dir, "main.php")

	// include как оператор
	got, err := runFileAt("<?php\ninclude \"echo.inc\";\n", main)
	if err != nil {
		t.Fatalf("include literal: %v", err)
	}
	if got != "Hello" {
		t.Errorf("include literal: got %q want %q", got, "Hello")
	}

	// include как выражение со скобками и через __DIR__
	got, err = runFileAt("<?php\ninclude(__DIR__ . \"/echo.inc\");\n", main)
	if err != nil {
		t.Fatalf("include __DIR__: %v", err)
	}
	if got != "Hello" {
		t.Errorf("include __DIR__: got %q want %q", got, "Hello")
	}

	// $x = include "..." — возврат значения из файла
	got, err = runFileAt("<?php\n$x = include \"ret.inc\";\necho $x;\n", main)
	if err != nil {
		t.Fatalf("$x = include: %v", err)
	}
	if got != "42" {
		t.Errorf("$x = include: got %q want %q", got, "42")
	}

	// значение include по умолчанию — 1
	got, err = runFileAt("<?php\n$x = include \"echo.inc\";\necho $x;\n", main)
	if err != nil {
		t.Fatalf("include default: %v", err)
	}
	if got != "Hello1" {
		t.Errorf("include default: got %q want %q", got, "Hello1")
	}

	// массив как значение include
	got, err = runFileAt("<?php\n$x = include \"val.inc\";\necho count($x);\n", main)
	if err != nil {
		t.Fatalf("include array: %v", err)
	}
	if got != "3" {
		t.Errorf("include array: got %q want %q", got, "3")
	}
}

// TestIncludeOnce проверяет, что include_once/require_once подключает файл один раз.
func TestIncludeOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "once.inc"), "<?php\necho \"O\";\n")
	main := filepath.Join(dir, "main.php")

	// include (без _once) подключает повторно
	got, err := runFileAt("<?php\ninclude \"once.inc\";\ninclude \"once.inc\";\n", main)
	if err != nil {
		t.Fatalf("include twice: %v", err)
	}
	if got != "OO" {
		t.Errorf("include twice: got %q want %q", got, "OO")
	}

	// include_once — ровно один раз
	got, err = runFileAt("<?php\ninclude_once \"once.inc\";\ninclude_once \"once.inc\";\n", main)
	if err != nil {
		t.Fatalf("include_once: %v", err)
	}
	if got != "O" {
		t.Errorf("include_once: got %q want %q", got, "O")
	}

	// require_once — ровно один раз
	got, err = runFileAt("<?php\nrequire_once \"once.inc\";\nrequire_once \"once.inc\";\n", main)
	if err != nil {
		t.Fatalf("require_once: %v", err)
	}
	if got != "O" {
		t.Errorf("require_once: got %q want %q", got, "O")
	}
}

// TestIncludeFunctionInIncludedFile проверяет hoisting функций из include.
func TestIncludeFunctionInIncludedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "func.inc"), "<?php\nfunction myIncludedFunc($a) { echo $a; }\n")
	main := filepath.Join(dir, "main.php")
	got, err := runFileAt("<?php\ninclude \"func.inc\";\nmyIncludedFunc(\"Hi\");\n", main)
	if err != nil {
		t.Fatalf("func in include: %v", err)
	}
	if got != "Hi" {
		t.Errorf("func in include: got %q want %q", got, "Hi")
	}
}

// TestIncludeDynamic проверяет динамический путь include (рантайм).
func TestIncludeDynamic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dyn.inc"), "<?php\necho \"D\";\n")
	main := filepath.Join(dir, "main.php")
	got, err := runFileAt("<?php\n$f = \"dyn.inc\";\ninclude $f;\n", main)
	if err != nil {
		t.Fatalf("dynamic include: %v", err)
	}
	if got != "D" {
		t.Errorf("dynamic include: got %q want %q", got, "D")
	}
}

// TestIncludeMissingFile проверяет, что include отсутствующего файла даёт false.
func TestIncludeMissingFile(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.php")
	got, err := runFileAt("<?php\n$x = @include \"nope.php\";\nvar_dump($x);\n", main)
	if err != nil {
		t.Fatalf("missing include: %v", err)
	}
	if got != "bool(false)\n" {
		t.Errorf("missing include: got %q want %q", got, "bool(false)\n")
	}
}

// TestEvalLiteral проверяет eval с литеральной строкой.
func TestEvalLiteral(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`<?php echo eval("return 1+2;");`, "3"},
		{`<?php echo eval('return "x" . "y";');`, "xy"},
		{`<?php eval('echo "E";');`, "E"},
		// eval без return возвращает null
		{`<?php var_dump(eval('1+1;'));`, "NULL\n"},
	}
	for _, c := range cases {
		got, err := runFileAt(c.src, "")
		if err != nil {
			t.Fatalf("eval %q: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("eval %q: got %q want %q", c.src, got, c.want)
		}
	}
}

// TestEvalDynamic проверяет eval с динамической строкой (рантайм).
func TestEvalDynamic(t *testing.T) {
	got, err := runFileAt("<?php\n$c = 'return 6*7;';\necho eval($c);\n", "")
	if err != nil {
		t.Fatalf("dynamic eval: %v", err)
	}
	if got != "42" {
		t.Errorf("dynamic eval: got %q want %q", got, "42")
	}
}

// TestEvalParseError проверяет, что ошибка разбора в eval бросается как
// перехватываемый ParseError.
func TestEvalParseError(t *testing.T) {
	src := "<?php\ntry {\n    eval('this is not valid php (');\n} catch (ParseError $e) {\n    echo \"caught\";\n}\n"
	got, err := runFileAt(src, "")
	if err != nil {
		t.Fatalf("eval ParseError: %v", err)
	}
	if got != "caught" {
		t.Errorf("eval ParseError: got %q want %q", got, "caught")
	}
}

// TestRequireMissing проверяет фатал require для отсутствующего файла,
// перехватываемый как Error. Zend печатает Warning о потоке до throw
// (сверено с php 8.3).
func TestRequireMissing(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.php")
	src := "<?php\ntry {\n    require 'nope-required.php';\n} catch (Error $e) {\n    echo \"caught\";\n}\n"
	got, err := runFileAt(src, main)
	if err != nil {
		t.Fatalf("require missing: %v", err)
	}
	want := "\nWarning: require(nope-required.php): Failed to open stream: No such file or directory in " +
		main + " on line 3\ncaught"
	if got != want {
		t.Errorf("require missing: got %q want %q", got, want)
	}
}
