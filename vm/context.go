package vm

import (
	"context"
	"unsafe"
)

type Callable interface {
	Call(ctx FunctionContext) Value
}

type Context interface {
	context.Context

	Parent() Context
	Global() Context
	Child(int) FunctionContext
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

func (g *GlobalContext) Parent() Context { return nil }
func (g *GlobalContext) Global() Context { return nil }
func (g *GlobalContext) Child(numArgs int) FunctionContext {
	return NewFunctionContext(g, numArgs)
}
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) Throw(error)                    {}

type FunctionContext struct {
	Context

	vars           []Value
	constants      []Value
	rx, ry, pc, fp int // Registers
	returned       bool
	numArgs        int
}

func NewFunctionContext(parent Context, numArgs int) FunctionContext {
	c := FunctionContext{}
	c.pc, c.rx, c.ry = 0, 0, 0
	c.returned = false
	c.Context = parent
	c.fp = parent.TopIndex() - numArgs
	c.numArgs = numArgs
	return c
}

func (ctx *FunctionContext) Parent() Context { return ctx.Context }
func (ctx *FunctionContext) Global() Context { return ctx.Context.Global() }
func (ctx *FunctionContext) Child(numArgs int) FunctionContext {
	return NewFunctionContext(ctx, numArgs)
}

type BuiltInFunction func(ctx FunctionContext) Value

func (f BuiltInFunction) Call(ctx FunctionContext) Value { return f(ctx) }

type CompiledFunction struct {
	Name         string
	LocalsSize   int
	Instructions Bytecode
	Constants    []Value
	NumVars      int
}

func (f CompiledFunction) Call(ctx FunctionContext) Value {
	ctx.constants = f.Constants
	ctx.vars = ctx.Slice(-ctx.numArgs, f.NumVars)
	ctx.MovePointer(f.NumVars)

	for ctx.pc = 0; !ctx.returned && ctx.pc < len(f.Instructions); ctx.pc++ {
		switch f.Instructions.ReadOperation(noescape(&ctx)) {
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
		case OpAnd:
			And(noescape(&ctx))
		case OpOr:
			Or(noescape(&ctx))
		case OpNot:
			Not(noescape(&ctx))
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
