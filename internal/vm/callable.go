package vm

type Callable interface {
	NumArgs() int
	Invoke(Context)
}

type Arg struct {
	ByRef   bool
	Name    string
	Type    Type
	Default Value
}

type BuiltInFunction[RT Value] struct {
	Args []Arg
	Fn   func(...Value) RT
}

func NewBuiltInFunction[RT Value, F ~func(...Value) RT](fn F, args ...Arg) BuiltInFunction[RT] {
	return BuiltInFunction[RT]{args, fn}
}
func (f BuiltInFunction[RT]) NumArgs() int { return len(f.Args) }
func (f BuiltInFunction[RT]) Invoke(ctx Context) {
	res := f.Fn(ctx.Slice(-f.NumArgs(), 0)...)
	ctx.MovePointer(-f.NumArgs())
	ctx.Push(res)
}

type CompiledFunction struct {
	Instructions Bytecode
	Args, Vars   int
}

func (f CompiledFunction) NumArgs() int { return f.Args }
func (f CompiledFunction) Invoke(parent Context) {
	global := parent.Global()
	frame := global.NextFrame()
	frame.ctx.Context = parent
	frame.ctx.global = global
	frame.ctx.vars = frame.ctx.global.Slice(-f.Args, f.Vars)
	frame.ctx.args = frame.ctx.vars[:len(frame.ctx.vars)-f.Vars]
	frame.ctx.pc = -1
	frame.fp = parent.TopIndex() - f.Args
	frame.bytecode = f.Instructions
	parent.MovePointer(f.Vars + f.Args)
}
