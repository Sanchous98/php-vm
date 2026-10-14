package php

import (
	"strings"
	"testing"
)

// types9 — регрессионные тесты для кластера Zend/tests/type_declarations:
//   - ленивая выдача PHP-хендлов (#N) кейсам enum;
//   - проверка совместимости абстрактного конструктора-прототипа;
//   - отложенный early binding: классы, объявленные позже, «недоступны» при
//     проверке сигнатур (Zend class_order).

// TestTypes9EnumLazyHandle: объявление enum не занимает #N, обращение к кейсу
// занимает (Zend).
func TestTypes9EnumLazyHandle(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"declare-only", `<?php
enum E { case A; }
var_dump(new stdClass);
`, "object(stdClass)#1 (0) {\n}\n"},
		{"access-case", `<?php
enum E { case A; }
E::A;
var_dump(new stdClass);
`, "object(stdClass)#2 (0) {\n}\n"},
		{"cases-all", `<?php
enum E { case A; case B; }
E::cases();
var_dump(new stdClass);
`, "object(stdClass)#3 (0) {\n}\n"},
	}
	for _, tc := range cases {
		out, err := runFile(tc.src)
		if err != nil {
			t.Fatalf("%s: run error: %v", tc.name, err)
		}
		if out != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, out, tc.want)
		}
	}
}

// TestTypes9AbstractCtorProto: потомок сверяется с абстрактным
// конструктором-прототипом (а не с промежуточной конкретной копией).
func TestTypes9AbstractCtorProto(t *testing.T) {
	src := `<?php
class X {}
abstract class A { abstract function __construct(X $x); }
class B extends A { function __construct(object $x) {} }
class C extends B { function __construct(Y $x) {} }
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "Could not check compatibility between C::__construct(Y $x) and A::__construct(X $x)") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestTypes9ForwardClassUnavailable: класс C объявлен позже B, поэтому при
// проверке сигнатуры B::method():C Zend не может проверить совместимость.
func TestTypes9ForwardClassUnavailable(t *testing.T) {
	src := `<?php
class A { public function method(): B {} }
class B extends A { public function method(): C {} }
class C extends B {}
new C;
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "Could not check compatibility between B::method(): C and A::method(): B, because class C is not available") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestTypes9FinalCtorStillChecked: final-конструктор предка всё ещё запрещает
// переопределение (регрессия к правке про абстрактный прототип).
func TestTypes9FinalCtorStillChecked(t *testing.T) {
	src := `<?php
class Base { public final function __construct() {} }
class Extended extends Base { public function __construct() {} }
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "Cannot override final method Base::__construct()") {
		t.Errorf("unexpected output: %q", out)
	}
}
