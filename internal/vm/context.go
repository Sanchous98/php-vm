package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
)

type Frame struct {
	ctx      FunctionContext
	bytecode Bytecode
	fp       int
}

type Context interface {
	context.Context
	stackIface[Value]

	NextFrame() *Frame
	PopFrame() *Frame

	Parent() Context
	Global() *GlobalContext
	GetFunction(int) Callable
	FunctionByName(String) Callable
	Throw(Throwable)
	Input() io.Reader
	Output() io.Writer
}

type GlobalContext struct {
	context.Context
	Stack[Value]

	frames   [997]Frame
	framesSp int

	Constants     []Value
	Functions     []Callable
	FunctionNames []String
	initialized   sync.Once

	in  io.Reader
	out io.Writer
	r1  uint64 // register for double-wide operations
}

func NewGlobalContext(ctx context.Context, in io.Reader, out io.Writer) *GlobalContext {
	if ctx == nil {
		ctx = context.Background()
	}

	if in == nil {
		in = os.Stdin
	}

	if out == nil {
		out = os.Stdout
	}

	return &GlobalContext{Context: ctx, in: in, out: out}
}

func (g *GlobalContext) FunctionByName(name String) Callable {
	defer func() {
		if err := recover(); err != nil {
			g.Throw(NewThrowable(fmt.Sprintf("Function \"%s\" does not exist", name), EError))
		}
	}()

	return g.GetFunction(slices.Index(g.FunctionNames, name))
}
func (g *GlobalContext) Init() {
	g.initialized.Do(g.Stack.Init)
	g.framesSp = -1
}
func (g *GlobalContext) Parent() Context                { return nil }
func (g *GlobalContext) Global() *GlobalContext         { return g }
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) Throw(Throwable)                { /* TODO */ }
func (g *GlobalContext) NextFrame() *Frame {
	g.framesSp++
	return &g.frames[g.framesSp]
}
func (g *GlobalContext) PopFrame() *Frame {
	frame := &g.frames[g.framesSp]
	g.framesSp--
	return frame
}
func (g *GlobalContext) Run(fn CompiledFunction) {
	g.Init()
	fn.Invoke(g)

	for g.framesSp >= 0 {
		g.frames[g.framesSp].ctx.pc++

		switch g.frames[g.framesSp].bytecode.ReadOperation(&g.frames[g.framesSp].ctx) {
		case OpPop:
			Pop(&g.frames[g.framesSp].ctx)
		case OpPop2:
			Pop2(&g.frames[g.framesSp].ctx)
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
		case OpNot:
			Not(&g.frames[g.framesSp].ctx)
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
		case OpAssignRef:
			AssignRef(&g.frames[g.framesSp].ctx)
		case OpArrayNew:
			ArrayNew(&g.frames[g.framesSp].ctx)
		case OpArrayAccessRead:
			ArrayAccessRead(&g.frames[g.framesSp].ctx)
		case OpArrayAccessWrite:
			ArrayAccessWrite(&g.frames[g.framesSp].ctx)
		case OpArrayAccessPush:
			ArrayAccessPush(&g.frames[g.framesSp].ctx)
		case OpArrayUnset:
			ArrayUnset(&g.frames[g.framesSp].ctx)
		case OpConcat:
			Concat(&g.frames[g.framesSp].ctx)
		case OpUnset:
			// TODO: Unset
		case OpForEachInit:
			ForEachInit(&g.frames[g.framesSp].ctx)
		case OpForEachNext:
			ForEachNext(&g.frames[g.framesSp].ctx)
		case OpForEachValid:
			ForEachValid(&g.frames[g.framesSp].ctx)
		case OpThrow:
			Throw(&g.frames[g.framesSp].ctx)
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
		case OpIsSet:
			IsSet(&g.frames[g.framesSp].ctx)
		case OpForEachKey:
			ForEachKey(&g.frames[g.framesSp].ctx)
		case OpForEachValue:
			ForEachValue(&g.frames[g.framesSp].ctx)
		case OpForEachValueRef:
			ForEachValueRef(&g.frames[g.framesSp].ctx)
		}
	}
}
func (g *GlobalContext) Output() io.Writer { return g.out }
func (g *GlobalContext) Input() io.Reader  { return g.in }

type FunctionContext struct {
	Context

	global     *GlobalContext // faster access to GlobalContext
	vars, args []Value
	pc, fp     int // Registers
}

func (ctx *FunctionContext) FunctionByName(name String) Callable {
	return ctx.global.FunctionByName(name)
}
func (ctx *FunctionContext) Output() io.Writer         { return ctx.global.Output() }
func (ctx *FunctionContext) Input() io.Reader          { return ctx.global.Input() }
func (ctx *FunctionContext) Throw(throwable Throwable) { ctx.global.Throw(throwable) }
func (ctx *FunctionContext) Arg(num int) Value         { return ctx.args[num] }
func (ctx *FunctionContext) Parent() Context           { return ctx.Context }
func (ctx *FunctionContext) Global() *GlobalContext    { return ctx.global }
func (ctx *FunctionContext) Pop() Value                { return ctx.global.Pop() }
func (ctx *FunctionContext) Push(v Value)              { ctx.global.Push(v) }
func (ctx *FunctionContext) TopIndex() int             { return ctx.global.TopIndex() }
func (ctx *FunctionContext) Slice(offsetX, offsetY int) []Value {
	return ctx.global.Slice(offsetX, offsetY)
}
func (ctx *FunctionContext) Sp(pointer int)          { ctx.global.Sp(pointer) }
func (ctx *FunctionContext) Top() Value              { return ctx.global.Top() }
func (ctx *FunctionContext) SetTop(v Value)          { ctx.global.SetTop(v) }
func (ctx *FunctionContext) MovePointer(pointer int) { ctx.global.MovePointer(pointer) }
