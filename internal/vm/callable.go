package vm

type Callable interface {
	Value

	Invoke(Context)
}

type ArgInfo struct {
	Name     String
	Type     typeHint
	Default  Value
	IsRef    bool
	Variadic bool
}

type BuiltInFunction[T Value] func(*FunctionContext) T

func (f BuiltInFunction[T]) Exec(ctx *FunctionContext) {
	ctx.stack[ctx.sp] = f(ctx)
	ctx.fp--

	if ctx.fp >= 0 {
		ctx.frame = &ctx.frames[ctx.fp]
	}
}

func (b Instructions) Exec(ctx *FunctionContext) {
	ctx.args = ctx.vars[:ctx.r1]
	ctx.returnSp = ctx.sp
	ctx.bytecode = b
	ctx.sp += int(ctx.r1)
}

type Executable interface {
	Exec(*FunctionContext)
}

type Function struct {
	Value

	// TODO: Implement symbol table
	FuncName String
	Args     []*ArgInfo
	Vars     []String // Preallocate vars on stack

	Executable Executable
}

func (f *Function) GetArgs() []*ArgInfo { return f.Args }
func (f *Function) Name() String        { return f.FuncName }
func (f *Function) Invoke(parent Context) {
	f.Executable.Exec(parent.Child(len(f.Vars)))
}

func ParseParameters(ctx *FunctionContext, params ...any) {
	// eliminate bounds' checks in loop
	args := ctx.args
	_ = args[len(params)-1]

	for i, param := range params {
		switch param := param.(type) {
		case *int:
			*param = int(args[i].(Int))
		case *Int:
			*param = args[i].(Int)
		case *float64:
			*param = float64(args[i].(Float))
		case *Float:
			*param = args[i].(Float)
		case *bool:
			*param = bool(args[i].(Bool))
		case *Bool:
			*param = args[i].(Bool)
		case *string:
			*param = string(args[i].(String))
		case *String:
			*param = args[i].(String)
		case *map[Value]Value:
			*param = make(map[Value]Value)
			for key, v := range args[i].(*Array).hash.iterate() {
				(*param)[key] = v
			}
		case *map[any]any:
			*param = make(map[any]any)
			for key, v := range args[i].(*Array).hash.iterate() {
				(*param)[key] = v
			}
		case *map[Value]any:
			*param = make(map[Value]any)
			for key, v := range args[i].(*Array).hash.iterate() {
				(*param)[key] = v
			}
		case *map[any]Value:
			*param = make(map[any]Value)
			for key, v := range args[i].(*Array).hash.iterate() {
				(*param)[key] = v
			}
		case *Array:
			*param = *(args[i].(*Array).Copy())
		case *Value:
			*param = args[i]
		default:
			panic("unsupported type")
		}
	}
}
