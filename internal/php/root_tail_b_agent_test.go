package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// root_tail_b_agent_test.go — регрессии по «длинному хвосту» корневых
// Zend-тестов (вторая половина) и пропущенным Parse error:
//   - namespace-relative / fully qualified имена в объявлении namespace;
//   - групповой `use` (элементы, ключевые слова, хвостовая запятая, ошибки);
//   - `implements\A` / `extends\A` — соседние `\` превращают текст в одно имя;
//   - self/parent/static в позициях родителя/интерфейса/трейта;
//   - запрет непарентезированного вложения тернарников;
//   - динамические вызовы scope-introspection функций и диагностики callable;
//   - правила размещения объявлений namespace;
//   - TypeError конструктора Exception/Error, не-final ErrorException;
//   - `const NULL` и коллизии use-алиасов с объявлениями классов;
//   - `function f();` без тела в неабстрактном классе;
//   - модификаторы/атрибуты анонимных классов.
// Ожидания сверены с живым php 8.3 (checkDiffFatal — полное сравнение вывода).

func TestRootTailBAgentVsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"ns_relative_decl", "<?php\n\nnamespace NAMEspace\\xyz;\n"},
		{"ns_relative_decl_ci", "<?php\nnamespace Namespace\\x;\n"},
		{"ns_fq_decl", "<?php\n\nnamespace \\Foo;\n"},
		{"ns_kw_name", "<?php\nnamespace namespace;\n"},
		{"ns_kw_name_block", "<?php\nnamespace namespace { }\n"},
		{"ns_odd_name", "<?php\nnamespace 42;\n"},
		{"ns_space_semicolon", "<?php\nnamespace ;\n"},
		{"ns_valid_two", "<?php\nnamespace A\\B;\necho __NAMESPACE__, \"\\n\";\n"},
		{"group_use_fqn", "<?php\n\nuse Foo\\Bar\\{\\Baz};\n"},
		{"group_use_fqn_after_comma", "<?php\nuse Foo\\Bar\\{A, \\Baz};\n"},
		{"group_use_const_in_const", "<?php\n\nuse const Foo\\Bar\\{\n    A,\n    const B,\n    function C\n};\n"},
		{"group_use_fn_in_fn", "<?php\nuse function Foo\\Bar\\{A, function B};\n"},
		{"group_use_empty", "<?php\nuse Baz\\{};\n"},
		{"group_use_comma", "<?php\nuse Baz\\{,};\n"},
		{"group_use_double_comma", "<?php\nuse Baz\\{Foo,,};\n"},
		{"group_use_kw_name", "<?php\nuse Foo\\{list};\n"},
		{"group_use_relative", "<?php\nuse Foo\\Bar\\{namespace\\B};\n"},
		{"group_use_trailing_comma", "<?php\nnamespace N;\nuse Foo\\Bar\\{A, B,};\necho A::class, \" \", B::class, \"\\n\";\n"},
		{"group_use_trailing_const", "<?php\nnamespace N;\nuse const Foo\\{C,};\necho C, \"\\n\";\n"},
		{"implements_adjacent", "<?php\n\nclass B implements\\A {}\n"},
		{"extends_adjacent", "<?php\nclass B extends\\A {}\n"},
		{"iface_extends_adjacent", "<?php\ninterface I extends\\A {}\n"},
		{"grammar_regression_010", "<?php\n\ninterface A{}\n\nclass B implements\\A {}\n\necho \"Done\", PHP_EOL;\n"},
		{"extends_self", "<?php\n\nclass test extends self {\n}\n"},
		{"extends_static", "<?php\nclass Foo extends static {\n}\n"},
		{"implements_parent", "<?php\nclass A implements parent {}\n"},
		{"iface_extends_static", "<?php\ninterface Foo extends static {}\n"},
		{"trait_use_static", "<?php\nclass C { use static; }\n"},
		{"trait_insteadof_static_ref", "<?php\ntrait SomeTrait { public function foobar() {} }\nclass Test {\n    use SomeTrait {\n        static::foobar insteadof SomeTrait;\n    }\n}\n"},
		{"trait_insteadof_static_list", "<?php\ntrait SomeTrait { public function foobar() {} }\nclass Test {\n    use SomeTrait {\n        SomeTrait::foobar insteadof static;\n    }\n}\n"},
		{"extends_qualified_self_ok", "<?php\nnamespace N;\nclass A extends Foo\\self {}\necho \"ok\\n\";\n"},
		{"ternary_1", "<?php\n\n1 ? 2 : 3 ? 4 : 5;\n"},
		{"ternary_2", "<?php\n\n1 ?: 2 ? 3 : 4;\n"},
		{"ternary_3", "<?php\n\n1 ? 2 : 3 ?: 4;\n"},
		{"ternary_ok", "<?php\nvar_dump(1 ? 2 : (3 ? 4 : 5));\nvar_dump((1 ? 2 : 3) ? 4 : 5);\nvar_dump(1 ?: 2 ?: 3);\nvar_dump(1 ? 2 ? 3 : 4 : 5);\n"},
		{"dyn_extract_var", "<?php\nfunction t($f) { try { $f(['a'=>1]); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt('extract');\n"},
		{"dyn_extract_upper", "<?php\nfunction t() { $f='EXTRACT'; try { $f(['a'=>1]); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_compact", "<?php\nfunction t() { $f='compact'; try { $f(['a']); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_get_defined_vars", "<?php\nfunction t() { $f='get_defined_vars'; try { $f(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_func_get_args", "<?php\nfunction t() { $f='func_get_args'; try { $f(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_func_get_arg", "<?php\nfunction t() { $f='func_get_arg'; try { $f(1); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_func_num_args", "<?php\nfunction t() { $f='func_num_args'; try { $f(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_extract_call_user_func_var", "<?php\nfunction t($f) { try { call_user_func($f, ['a'=>1]); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt('extract');\n"},
		{"dyn_extract_array_map", "<?php\ntry { array_map('extract', [['a'=>1]]); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_extract_cast_str", "<?php\nfunction t() { try { ((string) 'extract')(['a' => 42]); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } }\nt();\n"},
		{"dyn_array_callable_size1", "<?php\ntry { (['foo'])(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_array_callable_size3", "<?php\ntry { (['foo','bar','baz'])(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_obj_not_callable", "<?php\n$o = new stdClass; try { $o(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_int_not_callable", "<?php\n$x = 1; try { $x(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_null_not_callable", "<?php\n$x = null; try { $x(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_str_nonstatic", "<?php\nclass Foo { function bar(){} } try { $m='Foo::bar'; $m(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_arr_nonstatic", "<?php\nclass Foo { function bar(){} function __call($n,$a){} } class T { function r() { $m=['Foo','bar']; try { $m(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } } }\n(new T)->r();\n"},
		{"dyn_missing_with_call_in_obj_ctx", "<?php\nclass Foo { function __call($n,$a){} function t() { $m=['Foo','bar']; try { $m(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; } } }\n(new Foo)->t();\n"},
		{"dyn_missing_global", "<?php\nclass Foo { function __call($n,$a){} } try { $m=['Foo','bar']; $m(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_method_not_string", "<?php\n$a = new stdClass; $b = 1; try { $a::$b(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"dyn_class_not_valid", "<?php\ntry { $a::$b(); } catch (\\Error $e) { echo $e->getMessage(), \"\\n\"; }\n"},
		{"ns_not_first", "<?php\necho \"x\\n\";\nnamespace foo;\necho __NAMESPACE__, \"\\n\";\n"},
		{"ns_not_first_block", "<?php\n$a = 'oops';\necho $a;\nnamespace foo {\n}\n"},
		{"ns_nested", "<?php\nnamespace foo {\n namespace oops {\n }\n}\n"},
		{"ns_mix_bracket_then_plain", "<?php\nnamespace foo {\n}\nnamespace oops;\n"},
		{"ns_mix_plain_then_bracket", "<?php\nnamespace foo;\nnamespace oops {\n}\n"},
		{"ns_code_outside", "<?php\nnamespace foo {\n}\n$a = 'oops';\n"},
		{"ns_declare_first_ok", "<?php\ndeclare(strict_types=1);\nnamespace foo;\necho __NAMESPACE__, \"\\n\";\n"},
		{"ns_two_plain_ok", "<?php\nnamespace a;\nnamespace b;\necho __NAMESPACE__, \"\\n\";\n"},
		{"exception_msg_null_ok", "<?php\n$e = new Exception();\nvar_dump($e->getMessage());\n"},
		{"exception_msg_int_ok", "<?php\n$e = new Exception(123);\necho $e->getMessage(), \"\\n\";\n"},
		{"exception_msg_bool_ok", "<?php\n$e = new Exception(true);\necho $e->getMessage(), \"\\n\";\n"},
		{"const_null", "<?php\nnamespace foo;\nconst NULL = 1;\n"},
		{"const_true", "<?php\nconst true = 1;\n"},
		{"const_normal_ok", "<?php\nconst Foo = 1;\necho Foo, \"\\n\";\n"},
		{"use_then_class", "<?php\nuse A\\B as Foo;\n\nclass Foo {\n}\n"},
		{"class_then_use", "<?php\nclass Foo {\n}\n\nuse A\\B as Foo;\n"},
		{"use_late_binding_conflict", "<?php\n\n/* Reverse declaration order disables early-binding */\nclass B extends A {}\nclass A {}\nuse Foo\\B;\n"},
		{"method_no_body", "<?php\n\nabstract class test {\n}\n\nclass Impl extends Test {\n    function Foo();\n}\n"},
		{"method_no_body_trait", "<?php\ntrait T { function f(); }\n"},
		{"method_abstract_ok", "<?php\nabstract class A { abstract function f(); }\n"},
		{"method_sig_iface_ok", "<?php\ninterface I { function f(); }\necho \"ok\\n\";\n"},
		{"anon_readonly", "<?php\n$o = new readonly class {\n public int $f;\n function __construct() { $this->f = 2; }\n};\nvar_dump($o->f);\n"},
		{"anon_abstract", "<?php\n$x = new abstract class {};\n"},
		{"anon_final", "<?php\n$x = new final class {};\n"},
		{"anon_readonly_readonly", "<?php\n$x = new readonly readonly class {};\n"},
		{"anon_attr_readonly", "<?php\n$x = new #[AllowDynamicProperties] readonly class {};\n"},
		{"anon_plain_ok", "<?php\n$x = new class { public $v = 1; };\nvar_dump($x->v);\n"},
		{"this_reassign", "<?php\nfunction foo() {\n    $a = \"this\";\n    $$a = 0;\n    var_dump($$a);\n}\nfoo();\n"},
		{"this_in_unset", "<?php\nunset($this);\n"},
		{"this_as_static", "<?php\nfunction foo() {\n    static $this;\n    var_dump($this);\n}\nfoo();\n"},
		{"this_as_global", "<?php\nfunction foo() {\n    global $this;\n    var_dump($this);\n}\nfoo();\n"},
		{"this_as_lexical", "<?php\n\nclass Foo {\n    public function f() {\n        return function() use ($this) {};\n    }\n}\n"},
		{"this_in_catch", "<?php\nclass C {\n    function foo() {\n        try {\n            throw new Exception();\n        } catch (Exception $this) {\n        }\n        var_dump($this);\n    }\n}\n$obj = new C;\n$obj->foo();\n"},
		{"unset_cast_removed", "<?php\n\n$x = 1;\nvar_dump((unset) $x);\nvar_dump($x);\n"},
		{"throwable_class", "<?php\nclass Failure implements Throwable {}\n"},
		{"throwable_iface_ok", "<?php\ninterface I extends Throwable {}\necho \"ok\\n\";\n"},
		{"throwable_enum", "<?php\nenum E implements Throwable {}\n"},
		{"fcc_nullsafe_simple", "<?php\n\n$foo?->bar(...);\n"},
		{"fcc_nullsafe_nested", "<?php\n\n$foo?->foo->bar(...);\n"},
		{"listref_nullsafe", "<?php\n[&$y]=$y->y?->y;\n"},
		{"attr_fcc", "<?php\n#[Attribute(...)]\nclass Foo {\n\n}\n"},
		{"attr_unpack", "<?php\n#[Attribute(...$a)]\nclass Foo {}\n"},
	}
	for _, c := range cases {
		checkDiffFatal(t, c.name, c.src)
	}
}

// runLivePHPTraceArgs — живой php с zend.exception_ignore_args=0 (как в
// PHPT-прогоне): трейсы содержат аргументы кадров.
func runLivePHPTraceArgs(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", "-d", "zend.exception_ignore_args=0", f).CombinedOutput()
	return strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
}

func checkDiffFatalTrace(t *testing.T, name, src string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil && !vm.IsExitSignal(err) {
		t.Fatalf("%s: run error: %v", name, err)
	}
	want := runLivePHPTraceArgs(t, src)
	if got != want {
		t.Errorf("%s:\n--- src ---\n%s\n--- got ---\n%q\n--- want ---\n%q", name, src, got, want)
	}
}

// TestRootTailBExceptionTraceVsPHP — фаталы конструктора Exception с полным
// трейсом (аргументы кадров видны при zend.exception_ignore_args=0).
func TestRootTailBExceptionTraceVsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"exception_msg_type", "<?php\n\nthrow new Exception(new stdClass);\n"},
		{"exception_subclass_msg_type", "<?php\n\nclass Hello extends Exception {}\nthrow new Hello(new stdClass);\n"},
		{"error_msg_type", "<?php\n\nthrow new Error(new stdClass);\n"},
		{"errorexception_extend", "<?php\nclass MyErrorException extends ErrorException{}\nthrow new MyErrorException(new stdClass);\n"},
		{"exception_msg_array", "<?php\nthrow new Exception([]);\n"},
	}
	for _, c := range cases {
		checkDiffFatalTrace(t, c.name, c.src)
	}
}
