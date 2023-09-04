package vm

import (
	"context"
	"io"
	"sync/atomic"
)

type Frame struct {
	ctx      FunctionContext
	bytecode Bytecode
	fp       int
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

	NextFrame() *Frame
	PopFrame() *Frame
}

type GlobalContext struct {
	context.Context
	Stack[Value]

	frames   [999]Frame
	framesSp int

	Constants   []Value
	Functions   []Callable
	initialized atomic.Bool

	in     io.Reader
	out    io.Writer
	rx, ry int
}

func NewGlobalContext(ctx context.Context) *GlobalContext {
	if ctx == nil {
		ctx = context.Background()
	}

	return &GlobalContext{Context: ctx}
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
func (g *GlobalContext) NextFrame() *Frame {
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
		g.frames[g.framesSp].ctx.pc++

		switch g.frames[g.framesSp].bytecode.ReadOperation(&g.frames[g.framesSp].ctx) {
		case OpPop:
			Pop(&g.frames[g.framesSp].ctx)
		case OpReturn:
			Return(&g.frames[g.framesSp].ctx)
		case OpReturnValue:
			ReturnValue(&g.frames[g.framesSp].ctx)
		case OpAdd:
			Add(&g.frames[g.framesSp].ctx)
		case OpSub:
			Sub(&g.frames[g.framesSp].ctx)
		case OpMul:
			Mul(&g.frames[g.framesSp].ctx)
		case OpDiv:
			Div(&g.frames[g.framesSp].ctx)
		case OpMod:
			Mod(&g.frames[g.framesSp].ctx)
		case OpPow:
			Pow(&g.frames[g.framesSp].ctx)
		case OpBwAnd:
			BwAnd(&g.frames[g.framesSp].ctx)
		case OpBwOr:
			BwOr(&g.frames[g.framesSp].ctx)
		case OpBwXor:
			BwXor(&g.frames[g.framesSp].ctx)
		case OpBwNot:
			BwNot(&g.frames[g.framesSp].ctx)
		case OpShiftLeft:
			ShiftLeft(&g.frames[g.framesSp].ctx)
		case OpShiftRight:
			ShiftRight(&g.frames[g.framesSp].ctx)
		case OpEqual:
			Equal(&g.frames[g.framesSp].ctx)
		case OpNotEqual:
			NotEqual(&g.frames[g.framesSp].ctx)
		case OpIdentical:
			Identical(&g.frames[g.framesSp].ctx)
		case OpNotIdentical:
			NotIdentical(&g.frames[g.framesSp].ctx)
		case OpGreater:
			Greater(&g.frames[g.framesSp].ctx)
		case OpLess:
			Less(&g.frames[g.framesSp].ctx)
		case OpGreaterOrEqual:
			GreaterOrEqual(&g.frames[g.framesSp].ctx)
		case OpLessOrEqual:
			LessOrEqual(&g.frames[g.framesSp].ctx)
		case OpCompare:
			Compare(&g.frames[g.framesSp].ctx)
		case OpConcat:
			Concat(&g.frames[g.framesSp].ctx)
		case OpAssertType:
			AssertType(&g.frames[g.framesSp].ctx)
		case OpAssign:
			Assign(&g.frames[g.framesSp].ctx)
		case OpAssignAdd:
			AssignAdd(&g.frames[g.framesSp].ctx)
		case OpAssignSub:
			AssignSub(&g.frames[g.framesSp].ctx)
		case OpAssignMul:
			AssignMul(&g.frames[g.framesSp].ctx)
		case OpAssignDiv:
			AssignDiv(&g.frames[g.framesSp].ctx)
		case OpAssignMod:
			AssignMod(&g.frames[g.framesSp].ctx)
		case OpAssignPow:
			AssignPow(&g.frames[g.framesSp].ctx)
		case OpAssignBwAnd:
			AssignBwAnd(&g.frames[g.framesSp].ctx)
		case OpAssignBwOr:
			AssignBwOr(&g.frames[g.framesSp].ctx)
		case OpAssignBwXor:
			AssignBwXor(&g.frames[g.framesSp].ctx)
		case OpAssignConcat:
			AssignConcat(&g.frames[g.framesSp].ctx)
		case OpAssignShiftLeft:
			AssignShiftLeft(&g.frames[g.framesSp].ctx)
		case OpAssignShiftRight:
			AssignShiftRight(&g.frames[g.framesSp].ctx)
		case OpCast:
			Cast(&g.frames[g.framesSp].ctx)
		case OpPreIncrement:
			PreIncrement(&g.frames[g.framesSp].ctx)
		case OpPostIncrement:
			PostIncrement(&g.frames[g.framesSp].ctx)
		case OpPreDecrement:
			PreDecrement(&g.frames[g.framesSp].ctx)
		case OpPostDecrement:
			PostDecrement(&g.frames[g.framesSp].ctx)
		case OpLoad:
			Load(&g.frames[g.framesSp].ctx)
		case OpLoadRef:
			LoadRef(&g.frames[g.framesSp].ctx)
		case OpConst:
			Const(&g.frames[g.framesSp].ctx)
		case OpJump:
			Jump(&g.frames[g.framesSp].ctx)
		case OpJumpTrue:
			JumpTrue(&g.frames[g.framesSp].ctx)
		case OpJumpFalse:
			JumpFalse(&g.frames[g.framesSp].ctx)
		case OpCall:
			Call(&g.frames[g.framesSp].ctx)
		case OpEcho:
			Echo(&g.frames[g.framesSp].ctx)
		}
	}

	return g.Pop()
}

type FunctionContext struct {
	Context

	global     *GlobalContext
	vars, args []Value
	symbols    map[String]int
	pc         int // Registers
}

func (ctx *FunctionContext) Arg(num int) Value      { return ctx.args[num] }
func (ctx *FunctionContext) Parent() Context        { return ctx.Context }
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.global }
func (ctx *FunctionContext) NextFrame() *Frame      { return ctx.global.NextFrame() }
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
