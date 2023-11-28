package vm

type Callable interface {
	Invoke(Context)
}

type Arg struct {
	Name     string
	Type     Type
	ByRef    bool
	Variadic bool
	Default  Value
}

type argList []Arg

func (a argList) Map(ctx Context, args []Value) []Value {
	for i, arg := range a {
		if args[i] == nil {
			args[i] = arg.Default
		}

		if arg.Type > 0 {
			args[i] = args[i].Cast(ctx, arg.Type)
		}

		if arg.ByRef {
			args[i] = NewRef(&args[i])
		}
	}

	return args
}

type BuiltInFunction[RT Value] struct {
	Args argList
	Fn   func(Context, ...Value) RT
}

func NewBuiltInFunction[RT Value, F ~func(Context, ...Value) RT](fn F, args ...Arg) BuiltInFunction[RT] {
	return BuiltInFunction[RT]{args, fn}
}
func (f BuiltInFunction[RT]) GetArgs() []Arg { return f.Args }
func (f BuiltInFunction[RT]) Invoke(ctx Context) {
	args := ctx.Slice(-len(f.Args), 0)
	res := f.Fn(ctx, f.Args.Map(ctx, args)...)
	ctx.MovePointer(-len(f.Args))
	ctx.Push(res)
}

type CompiledFunction struct {
	Instructions Bytecode
	Args, Vars   int
}

func (f CompiledFunction) Invoke(parent Context) {
	global := parent.Global()
	frame := global.NextFrame()
	frame.ctx.Context = parent
	frame.ctx.global = global
	frame.ctx.vars = frame.ctx.global.Slice(-f.Args, f.Vars)

	for i := range frame.ctx.vars {
		v := &frame.ctx.vars[i]
		if *v == nil {
			*v = Null{}
		}
	}

	frame.ctx.pc = -1
	frame.ctx.args = frame.ctx.vars[:len(frame.ctx.vars)-f.Vars]
	frame.fp = parent.TopIndex() - f.Args
	frame.bytecode = f.Instructions
	parent.MovePointer(f.Vars + f.Args)
}
