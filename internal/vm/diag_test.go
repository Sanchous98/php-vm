package vm

import "testing"

func TestArgTypeErrorFormat(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "with param and given",
			got:  ArgTypeError("strlen", 1, "string", "string", "array", "", 0),
			want: "strlen(): Argument #1 ($string) must be of type string, array given",
		},
		{
			name: "without param (variadic)",
			got:  ArgTypeError("array_merge", 2, "", "array", "string", "", 0),
			want: "array_merge(): Argument #2 must be of type array, string given",
		},
		{
			name: "without given",
			got:  ArgTypeError("ReflectionClass::isInstance", 1, "object", "object", "", "", 0),
			want: "ReflectionClass::isInstance(): Argument #1 ($object) must be of type object",
		},
		{
			name: "with called in",
			got:  ArgTypeError("foo", 1, "x", "int", "string", "/a.php", 7),
			want: "foo(): Argument #1 ($x) must be of type int, string given, called in /a.php on line 7",
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, c.got, c.want)
		}
	}
}

func TestReturnAndPropTypeErrorFormat(t *testing.T) {
	var vm VM
	if got, want := vm.returnTypeError("foo", "int", "none").Message, "foo(): Return value must be of type int, none returned"; got != want {
		t.Errorf("returnTypeError = %q, want %q", got, want)
	}
	if got, want := vm.propTypeError("string", "C", "p", "int", false).Message, "Cannot assign string to property C::$p of type int"; got != want {
		t.Errorf("propTypeError = %q, want %q", got, want)
	}
	if got, want := vm.propTypeError("string", "C", "p", "int", true).Message, "Cannot assign string to reference held by property C::$p of type int"; got != want {
		t.Errorf("propTypeError(byRef) = %q, want %q", got, want)
	}
}
