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
	bytecode Instructions
	sp       int
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

	frames [997]Frame
	fp     int

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
		g.fp = -1
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
	g.fp++
	return &g.frames[g.fp]
}
func (g *GlobalContext) PopFrame() *Frame {
	frame := &g.frames[g.fp]
	g.fp--
	return frame
}

func (g *GlobalContext) Run(fn CompiledFunction) {
	g.Init()
	fn.Invoke(g, nil, nil)

	for g.fp >= 0 && g.fp < len(g.frames) {
		g.frames[g.fp].ctx.pc++

		if g.frames[g.fp].ctx.pc >= len(g.frames[g.fp].bytecode) {
			g.fp--
			return
		}

		switch g.frames[g.fp].bytecode.ReadOperation(&g.frames[g.fp].ctx) {
		case OpPop:
			Pop(&g.frames[g.fp].ctx)
		case OpPop2:
			Pop2(&g.frames[g.fp].ctx)
		case OpReturn:
			Return(&g.frames[g.fp].ctx)
		case OpReturnValue:
			ReturnValue(&g.frames[g.fp].ctx)
		case OpAdd:
			Add(&g.frames[g.fp].ctx)
		case OpSub:
			Sub(&g.frames[g.fp].ctx)
		case OpMul:
			Mul(&g.frames[g.fp].ctx)
		case OpDiv:
			Div(&g.frames[g.fp].ctx)
		case OpMod:
			Mod(&g.frames[g.fp].ctx)
		case OpPow:
			Pow(&g.frames[g.fp].ctx)
		case OpBwAnd:
			BwAnd(&g.frames[g.fp].ctx)
		case OpBwOr:
			BwOr(&g.frames[g.fp].ctx)
		case OpBwXor:
			BwXor(&g.frames[g.fp].ctx)
		case OpBwNot:
			BwNot(&g.frames[g.fp].ctx)
		case OpShiftLeft:
			ShiftLeft(&g.frames[g.fp].ctx)
		case OpShiftRight:
			ShiftRight(&g.frames[g.fp].ctx)
		case OpEqual:
			Equal(&g.frames[g.fp].ctx)
		case OpNotEqual:
			NotEqual(&g.frames[g.fp].ctx)
		case OpIdentical:
			Identical(&g.frames[g.fp].ctx)
		case OpNotIdentical:
			NotIdentical(&g.frames[g.fp].ctx)
		case OpNot:
			Not(&g.frames[g.fp].ctx)
		case OpGreater:
			Greater(&g.frames[g.fp].ctx)
		case OpLess:
			Less(&g.frames[g.fp].ctx)
		case OpGreaterOrEqual:
			GreaterOrEqual(&g.frames[g.fp].ctx)
		case OpLessOrEqual:
			LessOrEqual(&g.frames[g.fp].ctx)
		case OpCompare:
			Compare(&g.frames[g.fp].ctx)
		case OpCoalesce:
			Coalesce(&g.frames[g.fp].ctx)
		case OpAssignRef:
			AssignRef(&g.frames[g.fp].ctx)
		case OpArrayNew:
			ArrayNew(&g.frames[g.fp].ctx)
		case OpArrayAccessRead:
			ArrayAccessRead(&g.frames[g.fp].ctx)
		case OpArrayAccessWrite:
			ArrayAccessWrite(&g.frames[g.fp].ctx)
		case OpArrayAccessPush:
			ArrayAccessPush(&g.frames[g.fp].ctx)
		case OpArrayUnset:
			ArrayUnset(&g.frames[g.fp].ctx)
		case OpConcat:
			Concat(&g.frames[g.fp].ctx)
		case OpForEachInit:
			ForEachInit(&g.frames[g.fp].ctx)
		case OpForEachNext:
			ForEachNext(&g.frames[g.fp].ctx)
		case OpForEachValid:
			ForEachValid(&g.frames[g.fp].ctx)
		case OpThrow:
			Throw(&g.frames[g.fp].ctx)
		case OpInitCallVar:
			InitCallVar(&g.frames[g.fp].ctx)
		case OpAssign:
			Assign(&g.frames[g.fp].ctx)
		case OpAssignAdd:
			AssignAdd(&g.frames[g.fp].ctx)
		case OpAssignSub:
			AssignSub(&g.frames[g.fp].ctx)
		case OpAssignMul:
			AssignMul(&g.frames[g.fp].ctx)
		case OpAssignDiv:
			AssignDiv(&g.frames[g.fp].ctx)
		case OpAssignMod:
			AssignMod(&g.frames[g.fp].ctx)
		case OpAssignPow:
			AssignPow(&g.frames[g.fp].ctx)
		case OpAssignBwAnd:
			AssignBwAnd(&g.frames[g.fp].ctx)
		case OpAssignBwOr:
			AssignBwOr(&g.frames[g.fp].ctx)
		case OpAssignBwXor:
			AssignBwXor(&g.frames[g.fp].ctx)
		case OpAssignConcat:
			AssignConcat(&g.frames[g.fp].ctx)
		case OpAssignShiftLeft:
			AssignShiftLeft(&g.frames[g.fp].ctx)
		case OpAssignShiftRight:
			AssignShiftRight(&g.frames[g.fp].ctx)
		case OpAssignCoalesce:
			AssignCoalesce(&g.frames[g.fp].ctx)
		case OpUnset:
			Unset(&g.frames[g.fp].ctx)
		case OpCast:
			Cast(&g.frames[g.fp].ctx)
		case OpPreIncrement:
			PreIncrement(&g.frames[g.fp].ctx)
		case OpPostIncrement:
			PostIncrement(&g.frames[g.fp].ctx)
		case OpPreDecrement:
			PreDecrement(&g.frames[g.fp].ctx)
		case OpPostDecrement:
			PostDecrement(&g.frames[g.fp].ctx)
		case OpLoad:
			Load(&g.frames[g.fp].ctx)
		case OpLoadRef:
			LoadRef(&g.frames[g.fp].ctx)
		case OpConst:
			Const(&g.frames[g.fp].ctx)
		case OpJump:
			Jump(&g.frames[g.fp].ctx)
		case OpJumpTrue:
			JumpTrue(&g.frames[g.fp].ctx)
		case OpJumpFalse:
			JumpFalse(&g.frames[g.fp].ctx)
		case OpInitCall:
			InitCall(&g.frames[g.fp].ctx)
		case OpCall:
			Call(&g.frames[g.fp].ctx)
		case OpEcho:
			Echo(&g.frames[g.fp].ctx)
		case OpIsSet:
			IsSet(&g.frames[g.fp].ctx)
		case OpForEachKey:
			ForEachKey(&g.frames[g.fp].ctx)
		case OpForEachValue:
			ForEachValue(&g.frames[g.fp].ctx)
		case OpForEachValueRef:
			ForEachValueRef(&g.frames[g.fp].ctx)
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
	vars, args []Value
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
