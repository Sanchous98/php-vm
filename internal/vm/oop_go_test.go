package vm

import (
	"bytes"
	"testing"
)

// Go-класс: структура point, встраивающая Object, с методами, зарегистрированная
// как класс. Методы читают данные через ctx.This[point]().
type point struct {
	Object
	x, y int
}

func TestOOPGoClass(t *testing.T) {
	cls := RegisterClass[point]("Point", nil)

	cls.Constructor(&Function{Name: "__construct", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			p := ctx.This[point]()
			p.x, p.y = int(args[0].Int()), int(args[1].Int())
			return MakeNull(), nil
		},
	}})
	cls.Method("sum", &Function{Name: "sum", Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			p := ctx.This[point]()
			return MakeInt(int64(p.x + p.y)), nil
		},
	}})

	classes := []ClassInfo{cls}

	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(3)))}) // $a = 3
	main.Emit(Instruction{Op: OpLoadK, A: 2, C: int32(main.AddConst(IntConst(4)))}) // $b = 4
	main.Emit(Instruction{Op: OpNew, A: 0, B: 1, C: 0})                             // $o = new Point(3, 4)
	cSum := int32(main.AddConst(StrConst("sum")))
	main.Emit(Instruction{Op: OpMethodCall, A: 3, B: 0, C: cSum}) // $o->sum()
	main.Emit(Instruction{Op: OpEcho, A: 3})
	main.Emit(Instruction{Op: OpHalt})

	var vm VM
	vm.Init(main, nil, classes)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if buf.String() != "7" {
		t.Fatalf("got %q, want 7", buf.String())
	}
}

// Мост PHP-свойство <-> Go-поле через объявленные доступоры: bytecode пишет
// $p->x / $p->y, Go-метод sum() читает те же поля p.x/p.y.
func TestOOPGoClassPropsBridge(t *testing.T) {
	cls := RegisterClass[point]("Point", nil)
	cls.Declare("x",
		func(o *Object) Value { return MakeInt(int64(objectAs[point](o, nil).x)) },
		func(o *Object, v Value) { objectAs[point](o, nil).x = int(v.ToInt()) })
	cls.Declare("y",
		func(o *Object) Value { return MakeInt(int64(objectAs[point](o, nil).y)) },
		func(o *Object, v Value) { objectAs[point](o, nil).y = int(v.ToInt()) })
	cls.Method("sum", &Function{Name: "sum", Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			p := ctx.This[point]()
			return MakeInt(int64(p.x + p.y)), nil
		},
	}})

	classes := []ClassInfo{cls}
	main := NewChunk()
	cx := int32(main.AddConst(StrConst("x")))
	cy := int32(main.AddConst(StrConst("y")))
	csum := int32(main.AddConst(StrConst("sum")))
	main.Emit(Instruction{Op: OpNew, A: 0, B: 0, C: 0})
	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(10)))})
	main.Emit(Instruction{Op: OpPropSet, A: 0, B: 1, C: cx}) // $p->x = 10
	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(20)))})
	main.Emit(Instruction{Op: OpPropSet, A: 0, B: 1, C: cy}) // $p->y = 20
	main.Emit(Instruction{Op: OpPropGet, A: 2, B: 0, C: cx}) // echo $p->x
	main.Emit(Instruction{Op: OpEcho, A: 2})
	main.Emit(Instruction{Op: OpMethodCall, A: 3, B: 0, C: csum}) // $p->sum()
	main.Emit(Instruction{Op: OpEcho, A: 3})
	main.Emit(Instruction{Op: OpHalt})

	var vm VM
	vm.Init(main, nil, classes)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if buf.String() != "1030" {
		t.Fatalf("got %q, want %q", buf.String(), "1030")
	}
}
