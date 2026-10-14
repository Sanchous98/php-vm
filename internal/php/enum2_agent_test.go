package php

import (
	"strings"
	"testing"
)

// TestEnum2Features — регрессионные тесты по перечислениям (Zend/tests/enum):
// var_dump/print_r/var_export-представление, сериализация E-формата,
// дубликаты/несоответствие backing-типа, json_encode, сравнения, readonly-ссылки
// и отложенные ошибки кейсов.
func TestEnum2Features(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "var-dump",
			src: `<?php
enum Foo { case Bar; }
enum IntFoo: int { case Bar = 42; }
var_dump([Foo::Bar, IntFoo::Bar]);
`,
			want: "array(2) {\n  [0]=>\n  enum(Foo::Bar)\n  [1]=>\n  enum(IntFoo::Bar)\n}\n",
		},
		{
			name: "print-r",
			src: `<?php
enum IntFoo: int { case Bar = 42; }
print_r(IntFoo::Bar);
`,
			want: "IntFoo Enum:int\n(\n    [name] => Bar\n    [value] => 42\n)\n",
		},
		{
			name: "var-export",
			src: `<?php
enum Foo { case Bar; }
echo var_export(Foo::Bar, true), "\n";
`,
			want: "\\Foo::Bar\n",
		},
		{
			name: "serialize",
			src: `<?php
enum Foo { case Bar; }
echo serialize(Foo::Bar), "\n";
`,
			want: "E:7:\"Foo:Bar\";\n",
		},
		{
			name: "unserialize-roundtrip",
			src: `<?php
enum Foo { case Bar; case Quux; }
$b = unserialize('E:7:"Foo:Bar";');
var_dump($b === Foo::Bar);
`,
			want: "bool(true)\n",
		},
		{
			name: "unserialize-comparison",
			src: `<?php
enum Foo { case Bar; case Baz; }
$a = Foo::Bar; $b = Foo::Baz;
var_dump($a === $a, $a === $b, $a > $a, $a >= $a, $a > $b, $a >= $b, $a > true);
`,
			want: "bool(true)\nbool(false)\nbool(false)\nbool(true)\nbool(false)\nbool(false)\nbool(false)\n",
		},
		{
			name: "duplicate-value",
			src: `<?php
enum Foo: int { case Bar = 0; case Baz = 0; }
try { var_dump(Foo::Bar); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump(Foo::from(0)); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump(count(Foo::cases()));
`,
			want: "Duplicate value in enum Foo for cases Bar and Baz\nDuplicate value in enum Foo for cases Bar and Baz\nint(2)\n",
		},
		{
			name: "backed-mismatch",
			src: `<?php
enum Foo: int { case Bar = 'bar'; }
try { var_dump(Foo::Bar); } catch (Error $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
try { var_dump(Foo::from(42)); } catch (Error $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
try { var_dump(Foo::from('bar')); } catch (Error $e) { echo get_class($e), ': ', $e->getMessage(), "\n"; }
`,
			want: "TypeError: Enum case type string does not match enum backing type int\n" +
				"TypeError: Enum case type string does not match enum backing type int\n" +
				"TypeError: Foo::from(): Argument #1 ($value) must be of type int, string given\n",
		},
		{
			name: "no-new-catchable",
			src: `<?php
enum Foo {}
try { new Foo(); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
`,
			want: "Cannot instantiate enum Foo\n",
		},
		{
			name: "readonly-ref",
			src: `<?php
enum Foo: int { case Bar = 0; }
$bar = Foo::Bar;
try { $value = &$bar->value; $value = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump(Foo::Bar->value);
`,
			want: "Cannot modify readonly property Foo::$value\nint(0)\n",
		},
		{
			name: "json-non-backed",
			src: `<?php
enum Foo { case Bar; }
var_dump(json_encode(Foo::Bar));
echo json_last_error(), ': ', json_last_error_msg(), "\n";
`,
			want: "bool(false)\n11: Non-backed enums have no default serialization\n",
		},
		{
			name: "json-backed",
			src: `<?php
enum IntFoo: int { case Bar = 0; }
enum StringFoo: string { case Bar = 'Bar'; }
var_dump(json_encode(IntFoo::Bar), json_encode(StringFoo::Bar));
`,
			want: "string(1) \"0\"\nstring(5) \"\"Bar\"\"\n",
		},
		{
			name: "json-serializable",
			src: `<?php
enum CustomFoo implements JsonSerializable {
    case Bar;
    public function jsonSerialize(): mixed { return 'Custom ' . $this->name; }
}
var_dump(json_encode(CustomFoo::Bar));
`,
			want: "string(12) \"\"Custom Bar\"\"\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := runFile(c.src)
			if strings.TrimSpace(got) != strings.TrimSpace(c.want) {
				t.Errorf("%s:\n got=%q\nwant=%q", c.name, got, c.want)
			}
		})
	}
}
