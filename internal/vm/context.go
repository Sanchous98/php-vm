package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"unsafe"
)

const frameSize = int(unsafe.Sizeof(Frame{}))

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

	frames [999]Frame
	frame  *Frame

	Constants     []Value
	Functions     []Callable
	FunctionNames []String
	initialized   sync.Once

	in  io.Reader
	out io.Writer
	r1  uint64 // register for double-wide operations
}

func NewGlobalContext(ctx context.Context, in io.Reader, out io.Writer) GlobalContext {
	if ctx == nil {
		ctx = context.Background()
	}

	if in == nil {
		in = os.Stdin
	}

	if out == nil {
		out = os.Stdout
	}

	return GlobalContext{Context: ctx, in: in, out: out}
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
	g.initialized.Do(func() {
		g.Stack.Init()
		g.frame = (*Frame)(unsafe.Add(unsafe.Pointer(&g.frames[0]), -frameSize))
	})
}
func (g *GlobalContext) Parent() Context                { return nil }
func (g *GlobalContext) Global() *GlobalContext         { return g }
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) Throw(Throwable)                { /* TODO */ }
func (g *GlobalContext) NextFrame() *Frame {
	g.frame = (*Frame)(unsafe.Add(unsafe.Pointer(g.frame), frameSize))
	return g.frame
}
func (g *GlobalContext) PopFrame() *Frame {
	frame := g.frame
	g.frame = (*Frame)(unsafe.Add(unsafe.Pointer(g.frame), -frameSize))
	return frame
}
func (g *GlobalContext) Run(fn CompiledFunction) {
	g.Init()
	fn.Invoke(g)

	for uintptr(unsafe.Pointer(g.frame)) >= uintptr(unsafe.Pointer(&g.frames[0])) && uintptr(unsafe.Pointer(g.frame)) <= uintptr(unsafe.Pointer(&g.frames[996])) {
		g.frame.ctx.pc++

		switch g.frame.bytecode.ReadOperation(&g.frame.ctx) {
		case OpPop:
			Pop(&g.frame.ctx)
		case OpPop2:
			Pop2(&g.frame.ctx)
		case OpReturn:
			Return(&g.frame.ctx)
		case OpReturnValue:
			ReturnValue(&g.frame.ctx)
		case OpAdd:
			Add(&g.frame.ctx)
		case OpSub:
			Sub(&g.frame.ctx)
		case OpMul:
			Mul(&g.frame.ctx)
		case OpDiv:
			Div(&g.frame.ctx)
		case OpMod:
			Mod(&g.frame.ctx)
		case OpPow:
			Pow(&g.frame.ctx)
		case OpBwAnd:
			BwAnd(&g.frame.ctx)
		case OpBwOr:
			BwOr(&g.frame.ctx)
		case OpBwXor:
			BwXor(&g.frame.ctx)
		case OpBwNot:
			BwNot(&g.frame.ctx)
		case OpShiftLeft:
			ShiftLeft(&g.frame.ctx)
		case OpShiftRight:
			ShiftRight(&g.frame.ctx)
		case OpEqual:
			Equal(&g.frame.ctx)
		case OpNotEqual:
			NotEqual(&g.frame.ctx)
		case OpIdentical:
			Identical(&g.frame.ctx)
		case OpNotIdentical:
			NotIdentical(&g.frame.ctx)
		case OpNot:
			Not(&g.frame.ctx)
		case OpGreater:
			Greater(&g.frame.ctx)
		case OpLess:
			Less(&g.frame.ctx)
		case OpGreaterOrEqual:
			GreaterOrEqual(&g.frame.ctx)
		case OpLessOrEqual:
			LessOrEqual(&g.frame.ctx)
		case OpCompare:
			Compare(&g.frame.ctx)
		case OpAssignRef:
			AssignRef(&g.frame.ctx)
		case OpArrayNew:
			ArrayNew(&g.frame.ctx)
		case OpArrayAccessRead:
			ArrayAccessRead(&g.frame.ctx)
		case OpArrayAccessWrite:
			ArrayAccessWrite(&g.frame.ctx)
		case OpArrayAccessPush:
			ArrayAccessPush(&g.frame.ctx)
		case OpArrayUnset:
			ArrayUnset(&g.frame.ctx)
		case OpConcat:
			Concat(&g.frame.ctx)
		case OpUnset:
			// TODO: Unset
		case OpForEachInit:
			ForEachInit(&g.frame.ctx)
		case OpForEachNext:
			ForEachNext(&g.frame.ctx)
		case OpForEachValid:
			ForEachValid(&g.frame.ctx)
		case OpThrow:
			Throw(&g.frame.ctx)
		case OpCallByName:
			CallByName(&g.frame.ctx)
		case OpAssertType:
			AssertType(&g.frame.ctx)
		case OpAssign:
			Assign(&g.frame.ctx)
		case OpAssignAdd:
			AssignAdd(&g.frame.ctx)
		case OpAssignSub:
			AssignSub(&g.frame.ctx)
		case OpAssignMul:
			AssignMul(&g.frame.ctx)
		case OpAssignDiv:
			AssignDiv(&g.frame.ctx)
		case OpAssignMod:
			AssignMod(&g.frame.ctx)
		case OpAssignPow:
			AssignPow(&g.frame.ctx)
		case OpAssignBwAnd:
			AssignBwAnd(&g.frame.ctx)
		case OpAssignBwOr:
			AssignBwOr(&g.frame.ctx)
		case OpAssignBwXor:
			AssignBwXor(&g.frame.ctx)
		case OpAssignConcat:
			AssignConcat(&g.frame.ctx)
		case OpAssignShiftLeft:
			AssignShiftLeft(&g.frame.ctx)
		case OpAssignShiftRight:
			AssignShiftRight(&g.frame.ctx)
		case OpCast:
			Cast(&g.frame.ctx)
		case OpPreIncrement:
			PreIncrement(&g.frame.ctx)
		case OpPostIncrement:
			PostIncrement(&g.frame.ctx)
		case OpPreDecrement:
			PreDecrement(&g.frame.ctx)
		case OpPostDecrement:
			PostDecrement(&g.frame.ctx)
		case OpLoad:
			Load(&g.frame.ctx)
		case OpLoadRef:
			LoadRef(&g.frame.ctx)
		case OpConst:
			Const(&g.frame.ctx)
		case OpJump:
			Jump(&g.frame.ctx)
		case OpJumpTrue:
			JumpTrue(&g.frame.ctx)
		case OpJumpFalse:
			JumpFalse(&g.frame.ctx)
		case OpCall:
			Call(&g.frame.ctx)
		case OpEcho:
			Echo(&g.frame.ctx)
		case OpIsSet:
			IsSet(&g.frame.ctx)
		case OpForEachKey:
			ForEachKey(&g.frame.ctx)
		case OpForEachValue:
			ForEachValue(&g.frame.ctx)
		case OpForEachValueRef:
			ForEachValueRef(&g.frame.ctx)
		}
	}
}
func (g *GlobalContext) Output() io.Writer { return g.out }
func (g *GlobalContext) Input() io.Reader  { return g.in }

type FunctionContext struct {
	Context

	global     *GlobalContext // for faster access to GlobalContext
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
