package vm

import (
	"github.com/stretchr/testify/assert"
	"math"
	"strings"
	"testing"
)

func TestAdd(t *testing.T) {
	tests := [...]struct {
		name        string
		left, right Value
		result      Value
	}{
		{"int + int = int", Int(1), Int(2), Int(3)},
		{"int + float = float", Int(1), Float(2), Float(3)},
		{"float + float = float", Float(1), Float(2), Float(3)},
		{"bool + bool = int", Bool(false), Bool(false), Int(0)},
		{"bool + int = int", Bool(false), Int(1), Int(1)},
		{"bool + float = float", Bool(false), Float(1), Float(1)},
		{"[0] + [1, 2] = [0, 2]", NewArray(map[Value]Value{Int(0): Int(0)}, 1), NewArray(map[Value]Value{Int(0): Int(1), Int(1): Int(2)}, 2), NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(2)}, 2)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Add(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestArrayAccessPush(t *testing.T) {
	tests := [...]struct {
		name   string
		left   *Array
		result *Array
	}{
		{
			"$x[] = 1",
			NewArray(nil),
			&Array{next: Int(1), hash: HashTable{keys: map[Value]int{Int(0): 0}, values: []Value{Null{}}}},
		},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ArrayAccessPush(&ctx)
			ref := ctx.Pop().(Ref)
			arr := ctx.Pop().(*Array)
			assert.Equal(t, tt.result, arr)
			assert.Equal(t, &arr.hash.values[0], ref.ref)
		})
	}
}

func TestArrayAccessRead(t *testing.T) {
	tests := [...]struct {
		name   string
		key    Value
		arr    *Array
		result Value
	}{
		{"$a[1]", Int(1), NewArray(map[Value]Value{Int(1): Int(2)}), Int(2)},
		{"$a['test']", String("test"), NewArray(map[Value]Value{String("test"): Int(2)}), Int(2)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.arr)
			ctx.Push(tt.key)
			ArrayAccessRead(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestArrayAccessWrite(t *testing.T) {
	tests := [...]struct {
		name   string
		left   *Array
		key    Value
		result *Array
	}{
		{
			"$x[1] = 2",
			NewArray(nil), Int(1),
			&Array{next: Int(2), hash: HashTable{keys: map[Value]int{Int(1): 0}, values: []Value{Null{}}}},
		},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.key)
			ArrayAccessWrite(&ctx)
			ref := ctx.Pop().(Ref)
			arr := ctx.Pop().(*Array)
			assert.Equal(t, tt.result, arr)
			assert.Equal(t, &arr.hash.values[0], ref.ref)
		})
	}
}

func TestArrayNew(t *testing.T) {
	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	ArrayNew(&ctx)
	assert.Equal(t, NewArray(nil), ctx.Pop())
}

func TestArrayUnset(t *testing.T) {
	tests := [...]struct {
		name string
		arr  *Array
		key  Value
		res  *Array
	}{
		{"unset($a[1])", NewArray(map[Value]Value{Int(1): Int(2)}), Int(1), NewArray(map[Value]Value{})},
		{"unset($a['test'])", NewArray(map[Value]Value{String("test"): Int(2)}), String("test"), NewArray(map[Value]Value{})},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.arr)
			ctx.Push(tt.key)
			ArrayUnset(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestAssign(t *testing.T) {
	ref := func(value Value) *Value {
		return &value
	}

	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 0", 0, Null{}, Int(0), Int(0)},
		{"&$a = 0", 0, NewRef(ref(Null{})), Int(0), Ref{ref(Int(0))}},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			Assign(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignAdd(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 1; $a += 2", 0, Int(1), Int(2), Int(1 + 2)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignAdd(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignBwAnd(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 1; $a &= 2", 0, Int(1), Int(2), Int(1 & 2)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignBwAnd(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignBwOr(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 1; $a |= 2", 0, Int(1), Int(2), Int(1 | 2)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignBwOr(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignBwXor(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 1; $a ^= 2", 0, Int(1), Int(2), Int(1 ^ 2)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignBwXor(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignCoalesce(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = null; $a ??= 2", 0, Null{}, Int(2), Int(2)},
		{"$a = 1; $a ??= 2", 0, Int(1), Int(2), Int(1)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignCoalesce(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignConcat(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = '1'; $a .= '2'", 0, String("1"), String("2"), String("12")},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignConcat(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignDiv(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 1; $a /= 2", 0, Int(1), Int(2), Float(0.5)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignDiv(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignMod(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 1; $a %= 2", 0, Int(1), Int(2), Int(math.Mod(1, 2))},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignMod(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignMul(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 2; $a *= 3", 0, Int(2), Int(3), Int(2 * 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignMul(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignPow(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 2; $a **= 3", 0, Int(2), Int(3), Int(math.Pow(2, 3))},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignPow(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignRef(t *testing.T) {
	ref := func(v Value) *Value { return &v }
	tests := [...]struct {
		name  string
		left  Ref
		right Value
		res   Ref
	}{
		{"&$a = 0", NewRef(nil), Int(0), NewRef(ref(Int(0)))},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			AssignRef(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestAssignShiftLeft(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 8; $a <<= 3", 0, Int(8), Int(3), Int(8 << 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignShiftLeft(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignShiftRight(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 8; $a >>= 3", 0, Int(8), Int(3), Int(8 >> 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignShiftRight(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestAssignSub(t *testing.T) {
	tests := [...]struct {
		name       string
		r1         uint32
		defaultVal Value
		val        Value
		res        Value
	}{
		{"$a = 8; $a -= 3", 0, Int(8), Int(3), Int(8 - 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g, vars: g.Slice(0, 1)}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.sp = int(ctx.r1)
			ctx.r1 = tt.r1
			ctx.vars[ctx.r1] = tt.defaultVal
			ctx.Push(tt.val)
			AssignSub(&ctx)
			assert.Equal(t, tt.res, ctx.vars[ctx.r1])
		})
	}
}

func TestBwAnd(t *testing.T) {
	tests := [...]struct {
		name             string
		left, right, res Value
	}{
		{"8 & 3", Int(8), Int(3), Int(8 & 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			BwAnd(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestBwNot(t *testing.T) {
	tests := [...]struct {
		name      string
		left, res Value
	}{
		{"~8", Int(8), Int(^8)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			BwNot(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestBwOr(t *testing.T) {
	tests := [...]struct {
		name             string
		left, right, res Value
	}{
		{"8 | 3", Int(8), Int(3), Int(8 | 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			BwOr(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestBwXor(t *testing.T) {
	tests := [...]struct {
		name             string
		left, right, res Value
	}{
		{"8 ^ 3", Int(8), Int(3), Int(8 ^ 3)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			BwXor(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestCall(t *testing.T) {
	//g := GlobalContext{}
	//g.Init()
	//ctx := FunctionContext{GlobalContext: &g, parent: &g}
	//
	//ctx.r1 = 1
	//ctx.Push(BuiltInFunction[Int]{Fn: func(ctx *FunctionContext) Int {
	//	assert.Equal(t, Int(1), ctx.vars[0])
	//	return Int(0)
	//}})
	//ctx.Push(Int(1))
	//Call(&ctx)
	//assert.Equal(t, Int(0), ctx.Pop())
}

func TestCast(t *testing.T) {
	t.Skip()
}

func TestCoalesce(t *testing.T) {
	tests := [...]struct {
		name             string
		left, right, res Value
	}{
		{"null ?? 1", Null{}, Int(1), Int(1)},
		{"2 ?? 1", Int(2), Int(1), Int(2)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Coalesce(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestCompare(t *testing.T) {
	tests := [...]struct {
		name        string
		left, right Value
		result      Int
	}{
		{"0 <=> 1", Int(0), Int(1), -1},
		{"1 <=> 0", Int(1), Int(0), 1},
		{"1 <=> 1", Int(1), Int(1), 0},
		{"0 <=> 1.0", Int(0), Float(1), -1},
		{"1 <=> 0.0", Int(1), Float(0), 1},
		{"1 <=> 1.0", Int(1), Float(1), 0},
		{"1 <=> false", Int(1), Bool(false), 1},
		{"1 <=> true", Int(1), Bool(true), 0},
		{"2 <=> true", Int(2), Bool(true), 0},
		{"1 <=> \"1\"", Int(1), String("1"), 0},
		{"2 <=> \"1\"", Int(2), String("1"), 1},
		{"2 <=> \"2\"", Int(2), String("2"), 0},
		{"1 <=> \"2\"", Int(1), String("2"), -1},
		{"1 <=> []", Int(1), NewArray(nil), -1},
		{"0 <=> []", Int(0), NewArray(nil), -1},

		{"0.0 <=> 1.0", Float(0), Float(1), -1},
		{"1.0 <=> 0.0", Float(1), Float(0), 1},
		{"1.0 <=> 1.0", Float(1), Float(1), 0},
		{"1.0 <=> false", Float(1), Bool(false), 1},
		{"1.0 <=> true", Float(1), Bool(true), 0},
		{"2.0 <=> true", Float(2), Bool(true), 0},
		{"1.0 <=> \"1\"", Float(1), String("1"), 0},
		{"2.0 <=> \"1\"", Float(2), String("1"), 1},
		{"2.0 <=> \"2\"", Float(2), String("2"), 0},
		{"1.0 <=> \"2\"", Float(1), String("2"), -1},
		{"1.0 <=> []", Float(1), NewArray(nil), -1},
		{"0.0 <=> []", Float(0), NewArray(nil), -1},

		{"true <=> false", Bool(true), Bool(false), 1},
		{"false <=> false", Bool(false), Bool(false), 0},
		{"false <=> true", Bool(false), Bool(true), -1},
		{"true <=> \"0\"", Bool(true), String("0"), 1},
		{"false <=> \"0\"", Bool(false), String("0"), 0},
		{"true <=> \"1\"", Bool(true), String("1"), 0},
		{"false <=> \"1\"", Bool(false), String("1"), -1},
		{"true <=> \"2\"", Bool(true), String("2"), 0},
		{"false <=> \"2\"", Bool(false), String("2"), -1},
		{"true <=> []", Bool(true), NewArray(nil), -1},
		{"false <=> []", Bool(false), NewArray(nil), -1},

		{"\"1\" <=> \"0\"", String("1"), String("0"), 1},
		{"\"0\" <=> \"0\"", String("0"), String("0"), 0},
		{"\"1\" <=> \"1\"", String("1"), String("1"), 0},
		{"\"1\" <=> \"2\"", String("1"), String("2"), -1},
		{"\"0\" <=> []", String("0"), NewArray(nil), -1},
		{"\"1\" <=> []", String("1"), NewArray(nil), -1},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Compare(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestConcat(t *testing.T) {
	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	ctx.Push(String("test1"))
	ctx.Push(String("test2"))
	Concat(&ctx)
	assert.Equal(t, String("test1test2"), ctx.Pop())
}

func TestConst(t *testing.T) {
	g := GlobalContext{Constants: []Value{String("test")}}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.r1 = 0

	Const(&ctx)
	assert.Equal(t, String("test"), ctx.Pop())
}

func TestDiv(t *testing.T) {
	tests := [...]struct {
		name             string
		left, right, res Value
	}{
		{"8 / 4", Int(8), Int(4), Int(2)},
		{"8 / 5", Int(8), Int(5), Float(8) / Float(5)},
	}

	g := GlobalContext{}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Div(&ctx)
			assert.Equal(t, tt.res, ctx.Pop())
		})
	}
}

func TestEcho(t *testing.T) {
	var str strings.Builder

	g := GlobalContext{out: &str}
	g.Init()
	ctx := FunctionContext{GlobalContext: &g, parent: &g}

	ctx.Push(String("test1"))
	ctx.Push(String("test2"))
	ctx.r1 = 2
	Echo(&ctx)
	assert.Equal(t, "test1test2", str.String())
}

func TestEqual(t *testing.T) {
	tests := [...]struct {
		name        string
		left, right Value
		result      Value
	}{
		{"true == true", Bool(true), Bool(true), Bool(true)},
		{"true == false", Bool(true), Bool(false), Bool(false)},
		{"true == 1", Bool(true), Int(1), Bool(true)},
		{"true == 0", Bool(true), Int(0), Bool(false)},
		{"true == -1", Bool(true), Int(-1), Bool(true)},
		{"true == \"1\"", Bool(true), String("1"), Bool(true)},
		{"true == \"0\"", Bool(true), String("0"), Bool(false)},
		{"true == \"-1\"", Bool(true), String("-1"), Bool(true)},
		{"true == null", Bool(true), Null{}, Bool(false)},
		{"true == []", Bool(true), NewArray(nil), Bool(false)},
		{"true == \"\"", Bool(true), String(""), Bool(false)},
		{"true == \"php\"", Bool(true), String("php"), Bool(true)},

		{"false == false", Bool(false), Bool(false), Bool(true)},
		{"false == 1", Bool(false), Int(1), Bool(false)},
		{"false == 0", Bool(false), Int(0), Bool(true)},
		{"false == -1", Bool(false), Int(-1), Bool(false)},
		{"false == \"1\"", Bool(false), String("1"), Bool(false)},
		{"false == \"0\"", Bool(false), String("0"), Bool(true)},
		{"false == \"-1\"", Bool(false), String("-1"), Bool(false)},
		{"false == null", Bool(false), Null{}, Bool(true)},
		{"false == []", Bool(false), NewArray(nil), Bool(true)},
		{"false == \"\"", Bool(false), String(""), Bool(true)},
		{"false == \"php\"", Bool(false), String("php"), Bool(false)},

		{"1 == 1", Int(1), Int(1), Bool(true)},
		{"1 == 0", Int(1), Int(0), Bool(false)},
		{"1 == -1", Int(1), Int(-1), Bool(false)},
		{"1 == \"1\"", Int(1), String("1"), Bool(true)},
		{"1 == \"0\"", Int(1), String("0"), Bool(false)},
		{"1 == \"-1\"", Int(1), String("-1"), Bool(false)},
		{"1 == null", Int(1), Null{}, Bool(false)},
		{"1 == []", Int(1), NewArray(nil), Bool(false)},
		{"1 == \"\"", Int(1), String(""), Bool(false)},
		{"1 == \"php\"", Int(1), String("php"), Bool(false)},

		{"0 == 0", Int(0), Int(0), Bool(true)},
		{"0 == -1", Int(0), Int(-1), Bool(false)},
		{"0 == \"1\"", Int(0), String("1"), Bool(false)},
		{"0 == \"0\"", Int(0), String("0"), Bool(true)},
		{"0 == \"-1\"", Int(0), String("-1"), Bool(false)},
		{"0 == null", Int(0), Null{}, Bool(true)},
		{"0 == []", Int(0), NewArray(nil), Bool(false)},
		{"0 == \"\"", Int(0), String(""), Bool(false)},
		{"0 == \"php\"", Int(0), String("php"), Bool(false)},

		{"-1 == -1", Int(-1), Int(-1), Bool(true)},
		{"-1 == \"1\"", Int(-1), String("1"), Bool(false)},
		{"-1 == \"0\"", Int(-1), String("0"), Bool(false)},
		{"-1 == \"-1\"", Int(-1), String("-1"), Bool(true)},
		{"-1 == null", Int(-1), Null{}, Bool(false)},
		{"-1 == []", Int(-1), NewArray(nil), Bool(false)},
		{"-1 == \"\"", Int(-1), String(""), Bool(false)},
		{"-1 == \"php\"", Int(-1), String("php"), Bool(false)},

		{"\"1\" == \"1\"", String("1"), String("1"), Bool(true)},
		{"\"1\" == \"0\"", String("1"), String("0"), Bool(false)},
		{"\"1\" == \"-1\"", String("1"), String("-1"), Bool(false)},
		{"\"1\" == null", String("1"), Null{}, Bool(false)},
		{"\"1\" == []", String("1"), NewArray(nil), Bool(false)},
		{"\"1\" == \"\"", String("1"), String(""), Bool(false)},
		{"\"1\" == \"php\"", String("1"), String("php"), Bool(false)},

		{"\"0\" == \"0\"", String("0"), String("0"), Bool(true)},
		{"\"0\" == \"-1\"", String("0"), String("-1"), Bool(false)},
		{"\"0\" == null", String("0"), Null{}, Bool(false)},
		{"\"0\" == []]", String("0"), NewArray(nil), Bool(false)},
		{"\"0\" == \"\"", String("0"), String(""), Bool(false)},
		{"\"0\" == \"php\"", String("0"), String("php"), Bool(false)},

		{"\"-1\" == \"-1\"", String("-1"), String("-1"), Bool(true)},
		{"\"-1\" == null", String("-1"), Null{}, Bool(false)},
		{"\"-1\" == []", String("-1"), NewArray(nil), Bool(false)},
		{"\"-1\" == \"\"", String("-1"), String(""), Bool(false)},
		{"\"-1\" == \"php\"", String("-1"), String("php"), Bool(false)},

		{"null == null", Null{}, Null{}, Bool(true)},
		{"null == []", Null{}, NewArray(nil), Bool(true)},
		{"null == \"\"", Null{}, String(""), Bool(true)},
		{"null == \"php\"", Null{}, String("php"), Bool(false)},

		{"\"\" == []", String(""), NewArray(nil), Bool(false)},
		{"\"\" == \"\"", String(""), String(""), Bool(true)},
		{"\"\" == \"php\"", String(""), String("php"), Bool(false)},

		{"\"php\" == []", String("php"), NewArray(nil), Bool(false)},
		{"\"php\" == \"php\"", String("php"), String("php"), Bool(true)},

		{"[] == []", NewArray(nil), NewArray(nil), Bool(true)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Equal(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestForEachInit(t *testing.T) {
}

func TestForEachKey(t *testing.T) {
}

func TestForEachNext(t *testing.T) {
}

func TestForEachValid(t *testing.T) {
}

func TestForEachValue(t *testing.T) {
}

func TestForEachValueRef(t *testing.T) {
}

func TestGreater(t *testing.T) {
}

func TestGreaterOrEqual(t *testing.T) {
}

func TestIdentical(t *testing.T) {
	tests := [...]struct {
		name        string
		left, right Value
		result      Value
	}{
		{"[] === []", NewArray(nil), NewArray(nil), Bool(true)},
		{"[0] === [0]", NewArray(map[Value]Value{Int(0): Int(0)}), NewArray(map[Value]Value{Int(0): Int(0)}), Bool(true)},
		{"[0 => 0, 1 => 1] === [1 => 1, 0 => 0]", NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(1)}), NewArray(map[Value]Value{Int(1): Int(1), Int(0): Int(0)}), Bool(false)},
		{"[0 => 0, 1 => 1] === [0 => 0, 1 => 1]", NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(1)}), NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(1)}), Bool(true)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Identical(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestInitCall(t *testing.T) {
}

func TestInitCallVar(t *testing.T) {
}

func TestIsSet(t *testing.T) {
}

func TestJump(t *testing.T) {
}

func TestJumpFalse(t *testing.T) {
}

func TestJumpTrue(t *testing.T) {
}

func TestLess(t *testing.T) {
}

func TestLessOrEqual(t *testing.T) {
}

func TestLoad(t *testing.T) {
}

func TestLoadRef(t *testing.T) {
}

func TestMod(t *testing.T) {
}

func TestMul(t *testing.T) {
}

func TestNot(t *testing.T) {
	tests := [...]struct {
		name   string
		left   Value
		result Value
	}{
		{"!int(0)", Int(0), Bool(true)},
		{"!int(1)", Int(1), Bool(false)},
		{"!int(-1)", Int(-1), Bool(false)},

		{"!float(0)", Float(0), Bool(true)},
		{"!float(1)", Float(1), Bool(false)},
		{"!float(-1)", Float(-1), Bool(false)},

		{"!bool(false)", Bool(false), Bool(true)},
		{"!bool(true)", Bool(true), Bool(false)},

		{"!string('')", String(""), Bool(true)},
		{"!string('test')", String("test"), Bool(false)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			Not(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestNotEqual(t *testing.T) {
	tests := [...]struct {
		name        string
		left, right Value
		result      Value
	}{
		{"true == true", Bool(true), Bool(true), Bool(false)},
		{"true == false", Bool(true), Bool(false), Bool(true)},
		{"true == 1", Bool(true), Int(1), Bool(false)},
		{"true == 0", Bool(true), Int(0), Bool(true)},
		{"true == -1", Bool(true), Int(-1), Bool(false)},
		{"true == \"1\"", Bool(true), String("1"), Bool(false)},
		{"true == \"0\"", Bool(true), String("0"), Bool(true)},
		{"true == \"-1\"", Bool(true), String("-1"), Bool(false)},
		{"true == null", Bool(true), Null{}, Bool(true)},
		{"true == []", Bool(true), NewArray(nil), Bool(true)},
		{"true == \"\"", Bool(true), String(""), Bool(true)},
		{"true == \"php\"", Bool(true), String("php"), Bool(false)},

		{"false == false", Bool(false), Bool(false), Bool(false)},
		{"false == 1", Bool(false), Int(1), Bool(true)},
		{"false == 0", Bool(false), Int(0), Bool(false)},
		{"false == -1", Bool(false), Int(-1), Bool(true)},
		{"false == \"1\"", Bool(false), String("1"), Bool(true)},
		{"false == \"0\"", Bool(false), String("0"), Bool(false)},
		{"false == \"-1\"", Bool(false), String("-1"), Bool(true)},
		{"false == null", Bool(false), Null{}, Bool(false)},
		{"false == []", Bool(false), NewArray(nil), Bool(false)},
		{"false == \"\"", Bool(false), String(""), Bool(false)},
		{"false == \"php\"", Bool(false), String("php"), Bool(true)},

		{"1 == 1", Int(1), Int(1), Bool(false)},
		{"1 == 0", Int(1), Int(0), Bool(true)},
		{"1 == -1", Int(1), Int(-1), Bool(true)},
		{"1 == \"1\"", Int(1), String("1"), Bool(false)},
		{"1 == \"0\"", Int(1), String("0"), Bool(true)},
		{"1 == \"-1\"", Int(1), String("-1"), Bool(true)},
		{"1 == null", Int(1), Null{}, Bool(true)},
		{"1 == []", Int(1), NewArray(nil), Bool(true)},
		{"1 == \"\"", Int(1), String(""), Bool(true)},
		{"1 == \"php\"", Int(1), String("php"), Bool(true)},

		{"0 == 0", Int(0), Int(0), Bool(false)},
		{"0 == -1", Int(0), Int(-1), Bool(true)},
		{"0 == \"1\"", Int(0), String("1"), Bool(true)},
		{"0 == \"0\"", Int(0), String("0"), Bool(false)},
		{"0 == \"-1\"", Int(0), String("-1"), Bool(true)},
		{"0 == null", Int(0), Null{}, Bool(false)},
		{"0 == []", Int(0), NewArray(nil), Bool(true)},
		{"0 == \"\"", Int(0), String(""), Bool(true)},
		{"0 == \"php\"", Int(0), String("php"), Bool(true)},

		{"-1 == -1", Int(-1), Int(-1), Bool(false)},
		{"-1 == \"1\"", Int(-1), String("1"), Bool(true)},
		{"-1 == \"0\"", Int(-1), String("0"), Bool(true)},
		{"-1 == \"-1\"", Int(-1), String("-1"), Bool(false)},
		{"-1 == null", Int(-1), Null{}, Bool(true)},
		{"-1 == []", Int(-1), NewArray(nil), Bool(true)},
		{"-1 == \"\"", Int(-1), String(""), Bool(true)},
		{"-1 == \"php\"", Int(-1), String("php"), Bool(true)},

		{"\"1\" == \"1\"", String("1"), String("1"), Bool(false)},
		{"\"1\" == \"0\"", String("1"), String("0"), Bool(true)},
		{"\"1\" == \"-1\"", String("1"), String("-1"), Bool(true)},
		{"\"1\" == null", String("1"), Null{}, Bool(true)},
		{"\"1\" == []", String("1"), NewArray(nil), Bool(true)},
		{"\"1\" == \"\"", String("1"), String(""), Bool(true)},
		{"\"1\" == \"php\"", String("1"), String("php"), Bool(true)},

		{"\"0\" == \"0\"", String("0"), String("0"), Bool(false)},
		{"\"0\" == \"-1\"", String("0"), String("-1"), Bool(true)},
		{"\"0\" == null", String("0"), Null{}, Bool(true)},
		{"\"0\" == []]", String("0"), NewArray(nil), Bool(true)},
		{"\"0\" == \"\"", String("0"), String(""), Bool(true)},
		{"\"0\" == \"php\"", String("0"), String("php"), Bool(true)},

		{"\"-1\" == \"-1\"", String("-1"), String("-1"), Bool(false)},
		{"\"-1\" == null", String("-1"), Null{}, Bool(true)},
		{"\"-1\" == []", String("-1"), NewArray(nil), Bool(true)},
		{"\"-1\" == \"\"", String("-1"), String(""), Bool(true)},
		{"\"-1\" == \"php\"", String("-1"), String("php"), Bool(true)},

		{"null == null", Null{}, Null{}, Bool(false)},
		{"null == []", Null{}, NewArray(nil), Bool(false)},
		{"null == \"\"", Null{}, String(""), Bool(false)},
		{"null == \"php\"", Null{}, String("php"), Bool(true)},

		{"\"\" == []", String(""), NewArray(nil), Bool(true)},
		{"\"\" == \"\"", String(""), String(""), Bool(false)},
		{"\"\" == \"php\"", String(""), String("php"), Bool(true)},

		{"\"php\" == []", String("php"), NewArray(nil), Bool(true)},
		{"\"php\" == \"php\"", String("php"), String("php"), Bool(false)},

		{"[] == []", NewArray(nil), NewArray(nil), Bool(false)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			NotEqual(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestNotIdentical(t *testing.T) {
	tests := [...]struct {
		name        string
		left, right Value
		result      Value
	}{
		{"[] === []", NewArray(nil), NewArray(nil), Bool(false)},
		{"[0] === [0]", NewArray(map[Value]Value{Int(0): Int(0)}), NewArray(map[Value]Value{Int(0): Int(0)}), Bool(false)},
		{"[0 => 0, 1 => 1] === [1 => 1, 0 => 0]", NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(1)}), NewArray(map[Value]Value{Int(1): Int(1), Int(0): Int(0)}), Bool(true)},
		{"[0 => 0, 1 => 1] === [0 => 0, 1 => 1]", NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(1)}), NewArray(map[Value]Value{Int(0): Int(0), Int(1): Int(1)}), Bool(false)},
	}

	g := GlobalContext{}
	ctx := FunctionContext{GlobalContext: &g, parent: &g}
	ctx.Init()

	for _, tt := range &tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			NotIdentical(&ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestPop(t *testing.T) {
}

func TestPop2(t *testing.T) {
}

func TestPostDecrement(t *testing.T) {
}

func TestPostIncrement(t *testing.T) {
}

func TestPow(t *testing.T) {
}

func TestPreDecrement(t *testing.T) {
}

func TestPreIncrement(t *testing.T) {
}

func TestReturn(t *testing.T) {
}

func TestReturnValue(t *testing.T) {
}

func TestShiftLeft(t *testing.T) {
}

func TestShiftRight(t *testing.T) {
}

func TestSub(t *testing.T) {
}

func TestThrow(t *testing.T) {
}

func TestUnset(t *testing.T) {

}
