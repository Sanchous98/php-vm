package vm

import (
	"bytes"
	"testing"
)

func runChunk(t *testing.T, main *Chunk, funcs []*Function, classes []ClassInfo) (string, error) {
	t.Helper()
	var vm VM
	vm.Init(main, funcs, classes)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	err := vm.Run()
	return buf.String(), err
}

// Интерфейсы: instanceof по интерфейсу.
func TestOOPInterface(t *testing.T) {
	iface := NewInterface("I")
	a := NewClass("A", nil)
	a.Implements(iface)
	classes := []ClassInfo{iface, a}

	main := NewChunk()
	main.Emit(Instruction{Op: OpNew, A: 0, C: 1})              // new A
	main.Emit(Instruction{Op: OpInstanceOf, A: 1, B: 0, C: 0}) // $o instanceof I
	main.Emit(Instruction{Op: OpEcho, A: 1})
	main.Emit(Instruction{Op: OpHalt})

	got, err := runChunk(t, main, nil, classes)
	if err != nil || got != "1" {
		t.Fatalf("interface: got %q err %v, want 1", got, err)
	}
}

// Трейты: методы примешиваются.
func TestOOPTrait(t *testing.T) {
	trait := NewClass("T", nil)
	trait.Method("bar", Register("bar", func(ctx *Context) Int { return 7 }))
	b := NewClass("B", nil).Use(trait)
	classes := []ClassInfo{trait, b}

	main := NewChunk()
	main.Emit(Instruction{Op: OpNew, A: 0, C: 1})
	c := int32(main.AddConst(StrConst("bar")))
	main.Emit(Instruction{Op: OpMethodCall, A: 1, B: 0, C: c})
	main.Emit(Instruction{Op: OpEcho, A: 1})
	main.Emit(Instruction{Op: OpHalt})

	got, err := runChunk(t, main, nil, classes)
	if err != nil || got != "7" {
		t.Fatalf("trait: got %q err %v, want 7", got, err)
	}
}

// Видимость: private-метод недоступен снаружи.
func TestOOPVisibility(t *testing.T) {
	c := NewClass("C", nil)
	c.Method("secret", Register("secret", func(ctx *Context) Int { return 1 }), Private)
	classes := []ClassInfo{c}

	main := NewChunk()
	main.Emit(Instruction{Op: OpNew, A: 0, C: 0})
	cs := int32(main.AddConst(StrConst("secret")))
	main.Emit(Instruction{Op: OpMethodCall, A: 1, B: 0, C: cs})
	main.Emit(Instruction{Op: OpHalt})

	if _, err := runChunk(t, main, nil, classes); err != errAccess {
		if th, ok := err.(*Throwable); !ok || th.Class != "Error" {
			t.Fatalf("visibility: got err %v, want errAccess or Error", err)
		}
	}
}
