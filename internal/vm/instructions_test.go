package vm

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func BenchmarkEqual(b *testing.B) {
	b.ReportAllocs()

	g := &GlobalContext{}
	ctx := &FunctionContext{Context: g, global: g}
	ctx.Init()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Push(Int(1))
		ctx.Push(Bool(true))
		Equal(ctx)
		ctx.Pop()
	}
}

func BenchmarkIdentical(b *testing.B) {
	b.ReportAllocs()

	g := &GlobalContext{}
	ctx := &FunctionContext{Context: g, global: g}
	ctx.Init()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Push(Int(1))
		ctx.Push(Bool(true))
		Identical(ctx)
		ctx.Pop()
	}
}

func BenchmarkAdd(b *testing.B) {
	b.ReportAllocs()

	g := GlobalContext{}
	ctx := FunctionContext{Context: &g, global: &g}
	ctx.Init()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Push(Int(1))
		ctx.Push(Int(2))
		Add(noescape(&ctx))
		ctx.Pop()
	}
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

	g := &GlobalContext{}
	ctx := &FunctionContext{Context: g, global: g}
	ctx.Init()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Equal(ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

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

	g := &GlobalContext{}
	ctx := &FunctionContext{Context: g, global: g}
	ctx.Init()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Add(ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
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

	g := &GlobalContext{}
	ctx := &FunctionContext{Context: g, global: g}
	ctx.Init()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx.Push(tt.left)
			ctx.Push(tt.right)
			Compare(ctx)
			assert.Equal(t, tt.result, ctx.Pop())
		})
	}
}

func BenchmarkPostIncrement(b *testing.B) {
	b.ReportAllocs()

	g := GlobalContext{}
	ctx := FunctionContext{Context: &g, global: &g}
	ctx.Init()
	ctx.vars = append(ctx.vars, Int(0))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		PostIncrement(noescape(&ctx))
		ctx.vars[0] = ctx.vars[0].(Int) - 1
		Pop(noescape(&ctx))
	}
}

func BenchmarkLoad(b *testing.B) {
	b.ReportAllocs()

	g := GlobalContext{}
	ctx := FunctionContext{Context: &g, global: &g}
	ctx.Init()
	ctx.vars = append(ctx.vars, Int(0))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		Load(noescape(&ctx))
		ctx.Pop()
	}
}
