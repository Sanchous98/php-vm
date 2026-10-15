package vm

import (
	"bytes"
	"testing"
)

// ООП: new, свойства, методы, наследование, override, $this, instanceof, get_class.
func TestRegOOP(t *testing.T) {
	// class Base { public $x; function who() { return "Base"; } }
	base := NewClass("Base", nil)
	{
		c := NewChunk()
		c.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(c.AddConst(StrConst("Base")))})
		c.Emit(Instruction{Op: OpRet, A: 0})
		base.Method("who", mkFunc("who", 0, c))
	}

	// class Child extends Base { public $y; who() -> "Child"; getY() -> $this->y }
	child := NewClass("Child", base)
	{
		c := NewChunk()
		c.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(c.AddConst(StrConst("Child")))})
		c.Emit(Instruction{Op: OpRet, A: 0})
		child.Method("who", mkFunc("who", 0, c))
	}
	{
		c := NewChunk()
		cy := int32(c.AddConst(StrConst("y")))
		c.Emit(Instruction{Op: OpThis, A: 0})                 // r0 = $this
		c.Emit(Instruction{Op: OpPropGet, A: 1, B: 0, C: cy}) // r1 = $this->y
		c.Emit(Instruction{Op: OpRet, A: 1})
		child.Method("getY", mkFunc("getY", 0, c))
	}

	classes := []ClassInfo{base, child}

	main := NewChunk()
	main.Emit(Instruction{Op: OpNew, A: 0, C: 1})                                    // r0 = new Child
	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(42)))}) // r1 = 42
	cy := int32(main.AddConst(StrConst("y")))
	main.Emit(Instruction{Op: OpPropSet, A: 0, B: 1, C: cy}) // r0->y = 42
	main.Emit(Instruction{Op: OpPropGet, A: 2, B: 0, C: cy}) // r2 = r0->y
	main.Emit(Instruction{Op: OpEcho, A: 2})
	cw := int32(main.AddConst(StrConst("who")))
	main.Emit(Instruction{Op: OpMethodCall, A: 3, B: 0, C: cw}) // r3 = r0->who()
	main.Emit(Instruction{Op: OpEcho, A: 3})
	cg := int32(main.AddConst(StrConst("getY")))
	main.Emit(Instruction{Op: OpMethodCall, A: 4, B: 0, C: cg}) // r4 = r0->getY()
	main.Emit(Instruction{Op: OpEcho, A: 4})
	main.Emit(Instruction{Op: OpInstanceOf, A: 5, B: 0, C: 1}) // Child
	main.Emit(Instruction{Op: OpEcho, A: 5})
	main.Emit(Instruction{Op: OpInstanceOf, A: 6, B: 0, C: 0}) // Base
	main.Emit(Instruction{Op: OpEcho, A: 6})
	main.Emit(Instruction{Op: OpClassOf, A: 7, B: 0}) // get_class
	main.Emit(Instruction{Op: OpEcho, A: 7})
	main.Emit(Instruction{Op: OpHalt})

	var vm VM
	vm.Init(main, nil, classes)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "42Child4211Child"; normNoDep(buf.String()) != normNoDep(want) {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}
