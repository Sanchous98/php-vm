package vm

import (
	"context"
	"sync/atomic"
)

type Frame struct {
	ctx      FunctionContext
	bytecode Bytecode
	fp       int
}

type Callable interface {
	NumArgs() int
	Invoke(Context)
}

type Context interface {
	context.Context
	stackIface[Value]

	Parent() Context
	Global() *GlobalContext
	GetFunction(int) Callable
	Throw(error)

	ReadRX() int
	ReadRY() int
	WriteRX(int)
	WriteRY(int)

	PushFrame() *Frame
	PopFrame() *Frame
}

type GlobalContext struct {
	context.Context
	Stack[Value]

	frames   [128]Frame
	framesSp int

	Constants   []Value
	Functions   []Callable
	initialized atomic.Bool

	rx, ry int
}

func (g *GlobalContext) Init() {
	if !g.initialized.Swap(true) {
		g.Stack.Init()
	}
}
func (g *GlobalContext) Parent() Context                { return nil }
func (g *GlobalContext) Global() *GlobalContext         { return g }
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) Throw(error)                    {}
func (g *GlobalContext) currentFrame() *Frame           { return &g.frames[g.framesSp] }
func (g *GlobalContext) PushFrame() *Frame {
	g.framesSp++
	return &g.frames[g.framesSp]
}
func (g *GlobalContext) PopFrame() *Frame {
	frame := &g.frames[g.framesSp]
	g.framesSp--
	return frame
}
func (g *GlobalContext) ReadRX() int   { return g.rx }
func (g *GlobalContext) ReadRY() int   { return g.ry }
func (g *GlobalContext) WriteRX(x int) { g.rx = x }
func (g *GlobalContext) WriteRY(y int) { g.ry = y }
func (g *GlobalContext) Run(fn CompiledFunction) Value {
	g.Init()
	fn.Invoke(g)

	for g.framesSp > 0 {
		g.currentFrame().ctx.pc++

		switch g.currentFrame().bytecode.ReadOperation(&g.currentFrame().ctx) {
		case OpPop:
			Pop(&g.currentFrame().ctx)
		case OpReturn:
			Return(&g.currentFrame().ctx)
		case OpReturnValue:
			ReturnValue(&g.currentFrame().ctx)
		case OpAdd:
			Add(&g.currentFrame().ctx)
		case OpAddInt:
			AddInt(&g.currentFrame().ctx)
		case OpAddFloat:
			AddFloat(&g.currentFrame().ctx)
		case OpAddArray:
			AddArray(&g.currentFrame().ctx)
		case OpAddBool:
			AddBool(&g.currentFrame().ctx)
		case OpSub:
			Sub(&g.currentFrame().ctx)
		case OpSubInt:
			SubInt(&g.currentFrame().ctx)
		case OpSubFloat:
			SubFloat(&g.currentFrame().ctx)
		case OpSubBool:
			SubBool(&g.currentFrame().ctx)
		case OpMul:
			Mul(&g.currentFrame().ctx)
		case OpMulInt:
			MulInt(&g.currentFrame().ctx)
		case OpMulFloat:
			MulFloat(&g.currentFrame().ctx)
		case OpMulBool:
			MulBool(&g.currentFrame().ctx)
		case OpDiv:
			Div(&g.currentFrame().ctx)
		case OpDivInt:
			DivInt(&g.currentFrame().ctx)
		case OpDivFloat:
			DivFloat(&g.currentFrame().ctx)
		case OpDivBool:
			DivBool(&g.currentFrame().ctx)
		case OpMod:
			Mod(&g.currentFrame().ctx)
		case OpModInt:
			ModInt(&g.currentFrame().ctx)
		case OpModFloat:
			ModFloat(&g.currentFrame().ctx)
		case OpModBool:
			ModBool(&g.currentFrame().ctx)
		case OpPow:
			Pow(&g.currentFrame().ctx)
		case OpPowInt:
			PowInt(&g.currentFrame().ctx)
		case OpPowFloat:
			PowFloat(&g.currentFrame().ctx)
		case OpPowBool:
			PowBool(&g.currentFrame().ctx)
		case OpBwAnd:
			BwAnd(&g.currentFrame().ctx)
		case OpBwOr:
			BwOr(&g.currentFrame().ctx)
		case OpBwXor:
			BwXor(&g.currentFrame().ctx)
		case OpBwNot:
			BwNot(&g.currentFrame().ctx)
		case OpShiftLeft:
			ShiftLeft(&g.currentFrame().ctx)
		case OpShiftRight:
			ShiftRight(&g.currentFrame().ctx)
		case OpEqual:
			Equal(&g.currentFrame().ctx)
		case OpNotEqual:
			NotEqual(&g.currentFrame().ctx)
		case OpIdentical:
			Identical(&g.currentFrame().ctx)
		case OpNotIdentical:
			NotIdentical(&g.currentFrame().ctx)
		case OpGreater:
			Greater(&g.currentFrame().ctx)
		case OpLess:
			Less(&g.currentFrame().ctx)
		case OpGreaterOrEqual:
			GreaterOrEqual(&g.currentFrame().ctx)
		case OpLessOrEqual:
			LessOrEqual(&g.currentFrame().ctx)
		case OpCompare:
			Compare(&g.currentFrame().ctx)
		case OpArrayFetch:
			ArrayFetch(&g.currentFrame().ctx)
		case OpConcat:
			Concat(&g.currentFrame().ctx)
		case OpAssertType:
			AssertType(&g.currentFrame().ctx)
		case OpAssign:
			Assign(&g.currentFrame().ctx)
		case OpAssignAdd:
			AssignAdd(&g.currentFrame().ctx)
		case OpAssignSub:
			AssignSub(&g.currentFrame().ctx)
		case OpAssignMul:
			AssignMul(&g.currentFrame().ctx)
		case OpAssignDiv:
			AssignDiv(&g.currentFrame().ctx)
		case OpAssignMod:
			AssignMod(&g.currentFrame().ctx)
		case OpAssignPow:
			AssignPow(&g.currentFrame().ctx)
		case OpAssignBwAnd:
			AssignBwAnd(&g.currentFrame().ctx)
		case OpAssignBwOr:
			AssignBwOr(&g.currentFrame().ctx)
		case OpAssignBwXor:
			AssignBwXor(&g.currentFrame().ctx)
		case OpAssignConcat:
			AssignConcat(&g.currentFrame().ctx)
		case OpAssignShiftLeft:
			AssignShiftLeft(&g.currentFrame().ctx)
		case OpAssignShiftRight:
			AssignShiftRight(&g.currentFrame().ctx)
		case OpArrayPut:
			ArrayPut(&g.currentFrame().ctx)
		case OpArrayPush:
			ArrayPush(&g.currentFrame().ctx)
		case OpCast:
			Cast(&g.currentFrame().ctx)
		case OpPreIncrement:
			PreIncrement(&g.currentFrame().ctx)
		case OpPostIncrement:
			PostIncrement(&g.currentFrame().ctx)
		case OpPreDecrement:
			PreDecrement(&g.currentFrame().ctx)
		case OpPostDecrement:
			PostDecrement(&g.currentFrame().ctx)
		case OpLoad:
			Load(&g.currentFrame().ctx)
		case OpLoadString:
			LoadString(&g.currentFrame().ctx)
		case OpConst:
			Const(&g.currentFrame().ctx)
		case OpJump:
			Jump(&g.currentFrame().ctx)
		case OpJumpZ:
			JumpZ(&g.currentFrame().ctx)
		case OpJumpNZ:
			JumpNZ(&g.currentFrame().ctx)
		case OpCall:
			Call(&g.currentFrame().ctx)
		}
	}

	return g.Pop()
}

type FunctionContext struct {
	Context

	global     *GlobalContext
	vars, args []Value
	symbols    map[String]int
	constants  []Value
	pc         int // Registers
}

func (ctx *FunctionContext) Arg(num int) Value      { return ctx.args[num] }
func (ctx *FunctionContext) Parent() Context        { return ctx.Context }
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.global }
func (ctx *FunctionContext) PushFrame() *Frame      { return ctx.global.PushFrame() }
func (ctx *FunctionContext) PopFrame() *Frame       { return ctx.global.PopFrame() }
func (ctx *FunctionContext) ReadRX() int            { return ctx.global.ReadRX() }
func (ctx *FunctionContext) ReadRY() int            { return ctx.global.ReadRY() }
func (ctx *FunctionContext) WriteRX(x int)          { ctx.global.WriteRX(x) }
func (ctx *FunctionContext) WriteRY(y int)          { ctx.global.WriteRY(y) }
func (ctx *FunctionContext) Pop() Value             { return ctx.global.Pop() }
func (ctx *FunctionContext) Push(v Value)           { ctx.global.Push(v) }
func (ctx *FunctionContext) TopIndex() int          { return ctx.global.TopIndex() }
func (ctx *FunctionContext) Slice(offsetX, offsetY int) []Value {
	return ctx.global.Slice(offsetX, offsetY)
}
func (ctx *FunctionContext) Sp(pointer int)          { ctx.global.Sp(pointer) }
func (ctx *FunctionContext) Top() Value              { return ctx.global.Top() }
func (ctx *FunctionContext) SetTop(v Value)          { ctx.global.SetTop(v) }
func (ctx *FunctionContext) MovePointer(pointer int) { ctx.global.MovePointer(pointer) }

type BuiltInFunction[RT Value] struct {
	Args int
	Fn   func(...Value) RT
}

func (f BuiltInFunction[RT]) NumArgs() int { return f.Args }
func (f BuiltInFunction[RT]) Invoke(ctx Context) {
	ctx.Push(f.Fn(ctx.Slice(-f.Args, 0)...))
}

type CompiledFunction struct {
	Instructions Bytecode
	Args, Vars   int
}

func (f CompiledFunction) NumArgs() int { return f.Args }
func (f CompiledFunction) Invoke(parent Context) {
	global := parent.Global()
	frame := global.PushFrame()
	frame.ctx.Context = parent
	frame.ctx.global = global
	frame.ctx.vars = frame.ctx.global.Slice(-f.Args, f.Vars)
	frame.ctx.args = frame.ctx.vars[:len(frame.ctx.vars)-f.Vars]
	frame.ctx.pc = -1
	frame.fp = parent.TopIndex() - f.Args
	frame.bytecode = f.Instructions
	parent.MovePointer(f.Vars + f.Args)
}
