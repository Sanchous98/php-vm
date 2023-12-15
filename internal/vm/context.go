package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
)

type Context interface {
	context.Context

	Pop() Value
	Push(Value)
	TopIndex() int
	Slice(int, int) []Value
	Sp(int)
	Top() Value
	SetTop(Value)

	Child(int) *FunctionContext

	Parent() Context
	Global() *GlobalContext
	GetFunction(int) *Function
	FunctionByName(String) *Function

	Input() io.Reader
	Output() io.Writer

	Throw(Throwable)
}

type GlobalContext struct {
	context.Context
	Stack

	frames [128]FunctionContext
	fp     int
	frame  *FunctionContext

	Constants     []Value
	FunctionNames []String

	Functions []*Function

	in  io.Reader
	out io.Writer

	initialized sync.Once

	r1 uint32 // register for double-wide operations
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

func (g *GlobalContext) FunctionByName(name String) *Function {
	defer func() {
		if recover() != nil {
			g.Throw(NewThrowable(fmt.Sprintf("Function \"%s\" does not exist", name), EError))
		}
	}()

	return g.GetFunction(slices.Index(g.FunctionNames, name))
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
func (g *GlobalContext) Parent() Context                 { return nil }
func (g *GlobalContext) Global() *GlobalContext          { return g }
func (g *GlobalContext) GetFunction(index int) *Function { return g.Functions[index] }
func (g *GlobalContext) Throw(Throwable)                 { /* TODO: Keep try/catch stack and jump to it */ }
func (g *GlobalContext) Output() io.Writer               { return g.out }
func (g *GlobalContext) Input() io.Reader                { return g.in }
func (g *GlobalContext) Child(vars int) *FunctionContext {
	g.fp++
	g.frame = &g.frames[g.fp]

	if g.fp == 0 {
		g.frame.parent = g
	} else {
		g.frame.parent = &g.frames[g.fp-1]
	}

	g.frame.GlobalContext = g
	g.frame.vars = g.Slice(0, vars)
	g.frame.args = g.frame.vars[:g.r1]
	g.frame.pc = -1

	return g.frame
}

func (g *GlobalContext) Run(fn Function) {
	g.Init()
	fn.Invoke(g)

	var op Operator

	for g.fp >= 0 {
		g.frame.pc++

		switch op, g.r1 = g.frame.bytecode.ReadOperation(g.frame.pc); op {
		case OpNoop: // Noop
		case OpPop:
			Pop(g.frame)
		case OpPop2:
			Pop2(g.frame)
		case OpReturn:
			Return(g.frame)
		case OpReturnValue:
			ReturnValue(g.frame)
		case OpAdd:
			Add(g.frame)
		case OpSub:
			Sub(g.frame)
		case OpMul:
			Mul(g.frame)
		case OpDiv:
			Div(g.frame)
		case OpMod:
			Mod(g.frame)
		case OpPow:
			Pow(g.frame)
		case OpBwAnd:
			BwAnd(g.frame)
		case OpBwOr:
			BwOr(g.frame)
		case OpBwXor:
			BwXor(g.frame)
		case OpBwNot:
			BwNot(g.frame)
		case OpShiftLeft:
			ShiftLeft(g.frame)
		case OpShiftRight:
			ShiftRight(g.frame)
		case OpEqual:
			Equal(g.frame)
		case OpNotEqual:
			NotEqual(g.frame)
		case OpIdentical:
			Identical(g.frame)
		case OpNotIdentical:
			NotIdentical(g.frame)
		case OpNot:
			Not(g.frame)
		case OpGreater:
			Greater(g.frame)
		case OpLess:
			Less(g.frame)
		case OpGreaterOrEqual:
			GreaterOrEqual(g.frame)
		case OpLessOrEqual:
			LessOrEqual(g.frame)
		case OpCompare:
			Compare(g.frame)
		case OpCoalesce:
			Coalesce(g.frame)
		case OpAssignRef:
			AssignRef(g.frame)
		case OpArrayNew:
			ArrayNew(g.frame)
		case OpArrayAccessRead:
			ArrayAccessRead(g.frame)
		case OpArrayAccessWrite:
			ArrayAccessWrite(g.frame)
		case OpArrayAccessPush:
			ArrayAccessPush(g.frame)
		case OpArrayUnset:
			ArrayUnset(g.frame)
		case OpConcat:
			Concat(g.frame)
		case OpForEachInit:
			ForEachInit(g.frame)
		case OpForEachNext:
			ForEachNext(g.frame)
		case OpForEachValid:
			ForEachValid(g.frame)
		case OpThrow:
			Throw(g.frame)
		case OpInitCallVar:
			InitCallVar(g.frame)
		case OpAssign:
			Assign(g.frame)
		case OpAssignAdd:
			AssignAdd(g.frame)
		case OpAssignSub:
			AssignSub(g.frame)
		case OpAssignMul:
			AssignMul(g.frame)
		case OpAssignDiv:
			AssignDiv(g.frame)
		case OpAssignMod:
			AssignMod(g.frame)
		case OpAssignPow:
			AssignPow(g.frame)
		case OpAssignBwAnd:
			AssignBwAnd(g.frame)
		case OpAssignBwOr:
			AssignBwOr(g.frame)
		case OpAssignBwXor:
			AssignBwXor(g.frame)
		case OpAssignConcat:
			AssignConcat(g.frame)
		case OpAssignShiftLeft:
			AssignShiftLeft(g.frame)
		case OpAssignShiftRight:
			AssignShiftRight(g.frame)
		case OpAssignCoalesce:
			AssignCoalesce(g.frame)
		case OpUnset:
			Unset(g.frame)
		case OpCast:
			Cast(g.frame)
		case OpPreIncrement:
			PreIncrement(g.frame)
		case OpPostIncrement:
			PostIncrement(g.frame)
		case OpPreDecrement:
			PreDecrement(g.frame)
		case OpPostDecrement:
			PostDecrement(g.frame)
		case OpLoad:
			Load(g.frame)
		case OpLoadRef:
			LoadRef(g.frame)
		case OpConst:
			Const(g.frame)
		case OpJump:
			Jump(g.frame)
		case OpJumpTrue:
			JumpTrue(g.frame)
		case OpJumpFalse:
			JumpFalse(g.frame)
		case OpInitCall:
			InitCall(g.frame)
		case OpCall:
			Call(g.frame)
		case OpEcho:
			Echo(g.frame)
		case OpIsSet:
			IsSet(g.frame)
		case OpForEachKey:
			ForEachKey(g.frame)
		case OpForEachValue:
			ForEachValue(g.frame)
		case OpForEachValueRef:
			ForEachValueRef(g.frame)
		}
	}
}

type FunctionContext struct {
	*GlobalContext

	parent Context

	bytecode Instructions

	vars, args   []Value
	pc, returnSp int
}

func (ctx *FunctionContext) Parent() Context { return ctx.parent }
