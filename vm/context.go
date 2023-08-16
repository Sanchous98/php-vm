package vm

import (
	"context"
	"unsafe"
)

type Callable interface {
	NumArgs() int
	Invoke(Context) Value
}

type Context interface {
	context.Context

	Parent() Context
	Global() *GlobalContext
	GetFunction(int) Callable
	Throw(error)

	Init()
	Offset(int) Value
	Pop() Value
	Push(Value)
	Put(int, Value)
	TopIndex() int
	Slice(int, int) []Value
	Sp(int)
	MovePointer(int)
}

type GlobalContext struct {
	context.Context
	Stack

	Functions []Callable
}

func (g *GlobalContext) Parent() Context        { return nil }
func (g *GlobalContext) Global() *GlobalContext { return g }
func (g *GlobalContext) Child() FunctionContext {
	return NewFunctionContext(g)
}
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) Throw(error)                    {}

type FunctionContext struct {
	Context

	global         *GlobalContext
	vars, args     []Value
	constants      []Value
	rx, ry, pc, fp int // Registers
	returned       bool
}

func NewFunctionContext(parent Context) (c FunctionContext) {
	c.global = parent.Global()
	c.pc, c.rx, c.ry = 0, 0, 0
	c.returned = false
	c.Context = parent
	return
}

func (ctx *FunctionContext) Arg(num int) Value { return ctx.args[num] }
func (ctx *FunctionContext) Parent() Context   { return ctx.Context }
func (ctx *FunctionContext) Child() FunctionContext {
	return NewFunctionContext(ctx)
}
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.global }
func (ctx *FunctionContext) Offset(o int) Value     { return ctx.global.Offset(o) }
func (ctx *FunctionContext) Pop() Value             { return ctx.global.Pop() }
func (ctx *FunctionContext) Push(v Value)           { ctx.global.Push(v) }
func (ctx *FunctionContext) Put(index int, v Value) { ctx.global.Put(index, v) }
func (ctx *FunctionContext) Slice(x, y int) []Value { return ctx.global.Slice(x, y) }
func (ctx *FunctionContext) Sp(pointer int)         { ctx.global.Sp(pointer) }
func (ctx *FunctionContext) MovePointer(offset int) { ctx.global.MovePointer(offset) }
func (ctx *FunctionContext) TopIndex() int          { return ctx.global.TopIndex() }

type BuiltInFunction[R Value] struct {
	Args int
	Fn   func(...Value) R
}

func (f BuiltInFunction[R]) NumArgs() int { return f.Args }
func (f BuiltInFunction[R]) Invoke(ctx Context) Value {
	args := ctx.Slice(-f.Args, 0)
	return f.Fn(args...)
}

type CompiledFunction struct {
	Instructions Bytecode
	Constants    []Value
	Args         int
	Vars         int
}

func (f CompiledFunction) NumArgs() int { return f.Args }
func (f CompiledFunction) Invoke(parent Context) Value {
	ctx := NewFunctionContext(parent)
	ctx.constants = f.Constants
	ctx.vars = ctx.Slice(-f.Args, f.Vars)
	ctx.args = ctx.Slice(-f.Args, 0)
	ctx.fp = ctx.TopIndex() - f.Args
	ctx.MovePointer(f.Vars + f.Args)

	for ctx.pc = 0; !ctx.returned && ctx.pc < len(f.Instructions); ctx.pc++ {
		switch f.Instructions.ReadOperation(noescape(&ctx)) {
		case OpPop:
			Pop(noescape(&ctx))
		case OpAssertType:
			AssertType(noescape(&ctx))
		case OpAdd:
			Add(noescape(&ctx))
		case OpAddInt:
			AddInt(noescape(&ctx))
		case OpAddFloat:
			AddFloat(noescape(&ctx))
		case OpAddArray:
			AddArray(noescape(&ctx))
		case OpAddBool:
			AddBool(noescape(&ctx))
		case OpSub:
			Sub(noescape(&ctx))
		case OpSubInt:
			SubInt(noescape(&ctx))
		case OpSubFloat:
			SubFloat(noescape(&ctx))
		case OpSubBool:
			SubBool(noescape(&ctx))
		case OpMul:
			Mul(noescape(&ctx))
		case OpMulInt:
			MulInt(noescape(&ctx))
		case OpMulFloat:
			MulFloat(noescape(&ctx))
		case OpMulBool:
			MulBool(noescape(&ctx))
		case OpDiv:
			Div(noescape(&ctx))
		case OpDivInt:
			DivInt(noescape(&ctx))
		case OpDivFloat:
			DivFloat(noescape(&ctx))
		case OpDivBool:
			DivBool(noescape(&ctx))
		case OpMod:
			Mod(noescape(&ctx))
		case OpModInt:
			ModInt(noescape(&ctx))
		case OpModFloat:
			ModFloat(noescape(&ctx))
		case OpModBool:
			ModBool(noescape(&ctx))
		case OpPreIncrement:
			PreIncrement(noescape(&ctx))
		case OpPostIncrement:
			PostIncrement(noescape(&ctx))
		case OpPreDecrement:
			PreDecrement(noescape(&ctx))
		case OpPostDecrement:
			PostDecrement(noescape(&ctx))
		case OpIdentical:
			Identical(noescape(&ctx))
		case OpNotIdentical:
			NotIdentical(noescape(&ctx))
		case OpLoad:
			Load(noescape(&ctx))
		case OpConst:
			Const(noescape(&ctx))
		case OpConcat:
			Concat(noescape(&ctx))
		case OpJump:
			Jump(noescape(&ctx))
		case OpJumpZ:
			JumpZ(noescape(&ctx))
		case OpJumpNZ:
			JumpNZ(noescape(&ctx))
		case OpCall:
			Call(noescape(&ctx))
		case OpReturn:
			Return(noescape(&ctx))
		case OpAssign:
			Assign(noescape(&ctx))
		case OpAssignAdd:
			AssignAdd(noescape(&ctx))
		case OpAssignSub:
			AssignSub(noescape(&ctx))
		case OpAssignMul:
			AssignMul(noescape(&ctx))
		case OpAssignDiv:
			AssignDiv(noescape(&ctx))
		case OpAssignMod:
			AssignMod(noescape(&ctx))
		case OpArrayFetch:
			ArrayFetch(noescape(&ctx))
		case OpArrayPut:
			ArrayPut(noescape(&ctx))
		case OpArrayPush:
			ArrayPush(noescape(&ctx))
		case OpCast:
			Cast(noescape(&ctx))
		case OpEqual:
			Equal(noescape(&ctx))
		case OpNotEqual:
			NotEqual(noescape(&ctx))
		case OpGreater:
			Greater(noescape(&ctx))
		case OpLess:
			Less(noescape(&ctx))
		case OpGreaterOrEqual:
			GreaterOrEqual(noescape(&ctx))
		case OpLessOrEqual:
			LessOrEqual(noescape(&ctx))
		case OpCompare:
			Compare(noescape(&ctx))
		}
	}

	v := ctx.Offset(0)
	ctx.Sp(ctx.fp)

	return v
}

//go:nolint
func noescape[T any](pointer *T) *T {
	v := uintptr(unsafe.Pointer(pointer)) ^ 0
	return (*T)(unsafe.Pointer(v))
}
