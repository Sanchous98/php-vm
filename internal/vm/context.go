package vm

import (
	"context"
	"io"
	"os"
	"sync"
)

type Context interface {
	context.Context
	stackIface[Value]

	Parent() Context
	Global() *GlobalContext
	GetFunction(int) Callable
	Throw(Throwable)
}

type GlobalContext struct {
	context.Context
	Stack[Value]

	Constants   []Value
	Functions   []Callable
	initialized sync.Once

	in  io.Reader
	out io.Writer
	r1  uint64
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

func (g *GlobalContext) Init()                          { g.initialized.Do(g.Stack.Init) }
func (g *GlobalContext) Parent() Context                { return nil }
func (g *GlobalContext) Global() *GlobalContext         { return g }
func (g *GlobalContext) GetFunction(index int) Callable { return g.Functions[index] }
func (g *GlobalContext) Throw(Throwable)                {}
func (g *GlobalContext) Run(fn CompiledFunction) Value {
	g.Init()
	return fn.Invoke(noescape(g))
}

type FunctionContext struct {
	Context

	global     *GlobalContext // faster access to GlobalContext
	vars, args []Value
	pc, fp     int // Registers
}

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
