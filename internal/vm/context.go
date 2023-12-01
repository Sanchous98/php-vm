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
	bytecode Instructions
	fp       int
}

type Try struct {
	frame, fp, pc int
}

type Context interface {
	context.Context
	Init()
	Pop() Value
	Push(Value)
	TopIndex() int
	Slice(int, int) []Value
	Sp(int)
	Top() Value
	SetTop(Value)

	NextFrame() *Frame
	PopFrame() *Frame
	Child(*FunctionContext, int, Class, *Object)

	Parent() Context
	Global() *GlobalContext
	GetFunction(int) Callable
	FunctionByName(String) Callable
	GetClass(int) Class
	ClassByName(String) Class

	Input() io.Reader
	Output() io.Writer

	Throw(Throwable)
	Scope() Class
	This() *Object
}

type GlobalContext struct {
	context.Context
	Stack[Value]

	frames [999]Frame
	frame  int

	Constants     []Value
	Functions     []Callable
	Classes       []Class
	FunctionNames []String
	ClassNames    []String
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
func (g *GlobalContext) ClassByName(name String) Class {
	defer func() {
		if err := recover(); err != nil {
			g.Throw(NewThrowable(fmt.Sprintf("Class \"%s\" does not exist", name), EError))
		}
	}()

	return g.GetClass(slices.Index(g.ClassNames, name))
}
func (g *GlobalContext) Init() {
	g.initialized.Do(func() {
		g.Stack.Init()
		for i := range g.stack {
			g.stack[i] = Null{}
		}
		g.frame = -1
	})
}
func (g *GlobalContext) Scope() Class                   { return nil }
func (g *GlobalContext) This() *Object                  { return nil }
func (g *GlobalContext) Parent() Context                { return nil }
func (g *GlobalContext) Global() *GlobalContext         { return g }
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) GetClass(index int) Class       { return g.Classes[index] }
func (g *GlobalContext) Throw(Throwable)                { /* TODO: Keep try/catch stack and jump to t */ }
func (g *GlobalContext) NextFrame() *Frame {
	g.frame++
	return &g.frames[g.frame]
}
func (g *GlobalContext) PopFrame() *Frame {
	frame := &g.frames[g.frame]
	g.frame--
	return frame
}
func (g *GlobalContext) Run(fn CompiledFunction) {
	g.Init()
	fn.Invoke(g, nil, nil)

	for g.frame >= 0 && g.frame < len(g.frames) {
		g.frames[g.frame].ctx.pc++

		if g.frames[g.frame].ctx.pc >= len(g.frames[g.frame].bytecode) {
			g.PopFrame()
			return
		}

		switch g.frames[g.frame].bytecode.ReadOperation(&g.frames[g.frame].ctx) {
		case OpPop:
			Pop(&g.frames[g.frame].ctx)
		case OpPop2:
			Pop2(&g.frames[g.frame].ctx)
		case OpReturn:
			Return(&g.frames[g.frame].ctx)
		case OpReturnValue:
			ReturnValue(&g.frames[g.frame].ctx)
		case OpAdd:
			Add(&g.frames[g.frame].ctx)
		case OpSub:
			Sub(&g.frames[g.frame].ctx)
		case OpMul:
			Mul(&g.frames[g.frame].ctx)
		case OpDiv:
			Div(&g.frames[g.frame].ctx)
		case OpMod:
			Mod(&g.frames[g.frame].ctx)
		case OpPow:
			Pow(&g.frames[g.frame].ctx)
		case OpBwAnd:
			BwAnd(&g.frames[g.frame].ctx)
		case OpBwOr:
			BwOr(&g.frames[g.frame].ctx)
		case OpBwXor:
			BwXor(&g.frames[g.frame].ctx)
		case OpBwNot:
			BwNot(&g.frames[g.frame].ctx)
		case OpShiftLeft:
			ShiftLeft(&g.frames[g.frame].ctx)
		case OpShiftRight:
			ShiftRight(&g.frames[g.frame].ctx)
		case OpEqual:
			Equal(&g.frames[g.frame].ctx)
		case OpNotEqual:
			NotEqual(&g.frames[g.frame].ctx)
		case OpIdentical:
			Identical(&g.frames[g.frame].ctx)
		case OpNotIdentical:
			NotIdentical(&g.frames[g.frame].ctx)
		case OpNot:
			Not(&g.frames[g.frame].ctx)
		case OpGreater:
			Greater(&g.frames[g.frame].ctx)
		case OpLess:
			Less(&g.frames[g.frame].ctx)
		case OpGreaterOrEqual:
			GreaterOrEqual(&g.frames[g.frame].ctx)
		case OpLessOrEqual:
			LessOrEqual(&g.frames[g.frame].ctx)
		case OpCompare:
			Compare(&g.frames[g.frame].ctx)
		case OpCoalesce:
			Coalesce(&g.frames[g.frame].ctx)
		case OpAssignRef:
			AssignRef(&g.frames[g.frame].ctx)
		case OpArrayNew:
			ArrayNew(&g.frames[g.frame].ctx)
		case OpArrayAccessRead:
			ArrayAccessRead(&g.frames[g.frame].ctx)
		case OpArrayAccessWrite:
			ArrayAccessWrite(&g.frames[g.frame].ctx)
		case OpArrayAccessPush:
			ArrayAccessPush(&g.frames[g.frame].ctx)
		case OpArrayUnset:
			ArrayUnset(&g.frames[g.frame].ctx)
		case OpConcat:
			Concat(&g.frames[g.frame].ctx)
		case OpUnset:
			// TODO: Unset
		case OpForEachInit:
			ForEachInit(&g.frames[g.frame].ctx)
		case OpForEachNext:
			ForEachNext(&g.frames[g.frame].ctx)
		case OpForEachValid:
			ForEachValid(&g.frames[g.frame].ctx)
		case OpThrow:
			Throw(&g.frames[g.frame].ctx)
		case OpInitCallByName:
			InitCallByName(&g.frames[g.frame].ctx)
		case OpAssign:
			Assign(&g.frames[g.frame].ctx)
		case OpAssignAdd:
			AssignAdd(&g.frames[g.frame].ctx)
		case OpAssignSub:
			AssignSub(&g.frames[g.frame].ctx)
		case OpAssignMul:
			AssignMul(&g.frames[g.frame].ctx)
		case OpAssignDiv:
			AssignDiv(&g.frames[g.frame].ctx)
		case OpAssignMod:
			AssignMod(&g.frames[g.frame].ctx)
		case OpAssignPow:
			AssignPow(&g.frames[g.frame].ctx)
		case OpAssignBwAnd:
			AssignBwAnd(&g.frames[g.frame].ctx)
		case OpAssignBwOr:
			AssignBwOr(&g.frames[g.frame].ctx)
		case OpAssignBwXor:
			AssignBwXor(&g.frames[g.frame].ctx)
		case OpAssignConcat:
			AssignConcat(&g.frames[g.frame].ctx)
		case OpAssignShiftLeft:
			AssignShiftLeft(&g.frames[g.frame].ctx)
		case OpAssignShiftRight:
			AssignShiftRight(&g.frames[g.frame].ctx)
		case OpAssignCoalesce:
			AssignCoalesce(&g.frames[g.frame].ctx)
		case OpCast:
			Cast(&g.frames[g.frame].ctx)
		case OpPreIncrement:
			PreIncrement(&g.frames[g.frame].ctx)
		case OpPostIncrement:
			PostIncrement(&g.frames[g.frame].ctx)
		case OpPreDecrement:
			PreDecrement(&g.frames[g.frame].ctx)
		case OpPostDecrement:
			PostDecrement(&g.frames[g.frame].ctx)
		case OpLoad:
			Load(&g.frames[g.frame].ctx)
		case OpLoadRef:
			LoadRef(&g.frames[g.frame].ctx)
		case OpConst:
			Const(&g.frames[g.frame].ctx)
		case OpJump:
			Jump(&g.frames[g.frame].ctx)
		case OpJumpTrue:
			JumpTrue(&g.frames[g.frame].ctx)
		case OpJumpFalse:
			JumpFalse(&g.frames[g.frame].ctx)
		case OpInitCall:
			InitCall(&g.frames[g.frame].ctx)
		case OpCall:
			Call(&g.frames[g.frame].ctx)
		case OpEcho:
			Echo(&g.frames[g.frame].ctx)
		case OpIsSet:
			IsSet(&g.frames[g.frame].ctx)
		case OpForEachKey:
			ForEachKey(&g.frames[g.frame].ctx)
		case OpForEachValue:
			ForEachValue(&g.frames[g.frame].ctx)
		case OpForEachValueRef:
			ForEachValueRef(&g.frames[g.frame].ctx)
		}
	}
}
func (g *GlobalContext) Output() io.Writer  { return g.out }
func (g *GlobalContext) Input() io.Reader   { return g.in }
func (g *GlobalContext) StackTrace() String { panic("not implemented") }
func (g *GlobalContext) Child(child *FunctionContext, vars int, scope Class, this *Object) {
	child.parent = g
	child.GlobalContext = g
	child.vars = g.Slice(0, vars)
	child.pc = -1
	child.scope = scope
	child.this = this
}

type FunctionContext struct {
	*GlobalContext

	scope      Class
	this       *Object
	parent     Context
	vars, Args []Value
	pc         int
}

func (ctx *FunctionContext) Scope() Class       { return ctx.scope }
func (ctx *FunctionContext) This() *Object      { return ctx.this }
func (ctx *FunctionContext) Parent() Context    { return ctx.parent }
func (ctx *FunctionContext) StackTrace() String { panic("not implemented") }
func (ctx *FunctionContext) Child(child *FunctionContext, vars int, scope Class, this *Object) {
	child.parent = ctx
	child.GlobalContext = ctx.GlobalContext
	child.vars = ctx.Slice(0, vars)
	child.pc = -1
	child.scope = scope
	child.this = this
}
