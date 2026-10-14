package vm

import "testing"

func TestArrayOpcodeStrings(t *testing.T) {
	cases := map[Opcode]string{
		OpArray: "ARRAY", OpIndex: "INDEX", OpIndexSet: "INDEX_SET",
		OpAppend: "APPEND", OpLen: "LEN", OpUnset: "UNSET",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", op, got, want)
		}
	}
}

func TestOOPOpcodeStrings(t *testing.T) {
	cases := map[Opcode]string{
		OpNew: "NEW", OpPropGet: "PROP_GET", OpPropSet: "PROP_SET",
		OpMethodCall: "METHOD_CALL", OpThis: "THIS",
		OpInstanceOf: "INSTANCEOF", OpClassOf: "CLASS_OF",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", op, got, want)
		}
	}
}

func TestNewOOPOpcodeStrings(t *testing.T) {
	cases := map[Opcode]string{
		OpClone: "CLONE", OpStaticGet: "STATIC_GET",
		OpStaticSet: "STATIC_SET", OpConstGet: "CONST_GET",
		OpStaticCall: "STATIC_CALL",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", op, got, want)
		}
	}
}
