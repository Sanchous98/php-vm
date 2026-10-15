package vm

import (
	"bytes"
	"testing"
)

// Программа, собранная опкодами (аналог):
//
//	class Counter {
//	    public static $total = 0;
//	    const STEP = 10;
//	    public $value;
//	    function __construct($v) { $this->value = $v; self::$total = self::$total + $v; }
//	    function bump() { $this->value = $this->value + self::STEP; return $this->value; }
//	    function total() { return self::$total; }
//	}
//	$c = new Counter(5); echo $c->bump();       // 15
//	$c2 = clone $c;      echo $c2->bump();      // 25
//	echo $c->value;                            // 15
//	echo $c->total();                          // 5
//
// Ожидаемый вывод сверен с php: "1525155".
func TestOOPProgram(t *testing.T) {
	counter := NewClass("Counter", nil)
	counter.DefineStatic("total", MakeInt(0))
	counter.DefineConst("STEP", MakeInt(10))

	// __construct($v): $this->value = $v; self::$total = self::$total + $v
	{
		c := NewChunk()
		csVal := int32(c.AddConst(StrConst("value")))
		csTot := int32(c.AddConst(StrConst("total")))
		c.Emit(Instruction{Op: OpThis, A: 1})                    // r1 = $this
		c.Emit(Instruction{Op: OpPropSet, A: 1, B: 0, C: csVal}) // $this->value = $v (r0)
		c.Emit(Instruction{Op: OpStaticGet, A: 2, B: 0, C: csTot})
		c.Emit(Instruction{Op: OpAdd, A: 3, B: 2, C: 0}) // r3 = total + $v
		c.Emit(Instruction{Op: OpStaticSet, A: 3, B: 0, C: csTot})
		c.Emit(Instruction{Op: OpRet, A: 1})
		counter.Constructor(mkFunc("__construct", 1, c))
	}
	// bump(): $this->value += self::STEP; return $this->value
	{
		c := NewChunk()
		csStep := int32(c.AddConst(StrConst("STEP")))
		csVal := int32(c.AddConst(StrConst("value")))
		c.Emit(Instruction{Op: OpThis, A: 0})
		c.Emit(Instruction{Op: OpConstGet, A: 1, B: 0, C: csStep})
		c.Emit(Instruction{Op: OpPropGet, A: 2, B: 0, C: csVal})
		c.Emit(Instruction{Op: OpAdd, A: 3, B: 2, C: 1})
		c.Emit(Instruction{Op: OpPropSet, A: 0, B: 3, C: csVal})
		c.Emit(Instruction{Op: OpRet, A: 3})
		counter.Method("bump", mkFunc("bump", 0, c))
	}
	// total(): return self::$total
	{
		c := NewChunk()
		csTot := int32(c.AddConst(StrConst("total")))
		c.Emit(Instruction{Op: OpStaticGet, A: 0, B: 0, C: csTot})
		c.Emit(Instruction{Op: OpRet, A: 0})
		counter.Method("total", mkFunc("total", 0, c))
	}

	classes := []ClassInfo{counter}

	main := NewChunk()
	cBump := int32(main.AddConst(StrConst("bump")))
	cValue := int32(main.AddConst(StrConst("value")))
	cTotal := int32(main.AddConst(StrConst("total")))

	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(5)))}) // arg = 5
	main.Emit(Instruction{Op: OpNew, A: 0, B: 1, C: 0})                             // r0 = new Counter(5)
	main.Emit(Instruction{Op: OpMethodCall, A: 2, B: 0, C: cBump})                  // r2 = r0->bump()
	main.Emit(Instruction{Op: OpEcho, A: 2})
	main.Emit(Instruction{Op: OpClone, A: 3, B: 0})                // r3 = clone r0
	main.Emit(Instruction{Op: OpMethodCall, A: 4, B: 3, C: cBump}) // r4 = r3->bump()
	main.Emit(Instruction{Op: OpEcho, A: 4})
	main.Emit(Instruction{Op: OpPropGet, A: 5, B: 0, C: cValue}) // r5 = r0->value
	main.Emit(Instruction{Op: OpEcho, A: 5})
	main.Emit(Instruction{Op: OpMethodCall, A: 6, B: 0, C: cTotal}) // r6 = r0->total()
	main.Emit(Instruction{Op: OpEcho, A: 6})
	main.Emit(Instruction{Op: OpHalt})

	var vm VM
	vm.Init(main, nil, classes)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "1525155"; normNoDep(buf.String()) != want {
		t.Fatalf("got %q, want %q (php)", buf.String(), want)
	}
}
