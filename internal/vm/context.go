package vm

import (
	"context"
	"io"
	"sync/atomic"
	"unsafe"
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
}

type GlobalContext struct {
	context.Context
	Stack[Value]

	Constants   []Value
	Functions   []Callable
	initialized atomic.Bool

	in  io.Reader
	out io.Writer
	rx  uint64
	ry  unsafe.Pointer
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
func (g *GlobalContext) Run(fn CompiledFunction) Value {
	g.Init()
	//defer g.Reset()
	return fn.Invoke(noescape(g))
}

type FunctionContext struct {
	Context

	global     *GlobalContext
	vars, args []Value
	symbols    map[String]int
	pc, fp     int // Registers
}

func (ctx *FunctionContext) Arg(num int) Value      { return ctx.args[num] }
func (ctx *FunctionContext) Parent() Context        { return ctx.Context }
func (ctx *FunctionContext) Global() *GlobalContext { return ctx.global }
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
