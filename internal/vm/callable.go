package vm

type Callable interface {
	Name() String
	Invoke(Context, Class, *Object)
}

type Arg struct {
	Name     string
	Default  Value
	Type     Type
	ByRef    bool
	Variadic bool
}

type BuiltInFunction[RT Value] struct {
	Value
	FuncName String
	Fn       func(*FunctionContext) RT
}

func NewBuiltInFunction[RT Value, F ~func(*FunctionContext) RT](fn F, name String) BuiltInFunction[RT] {
	return BuiltInFunction[RT]{nil, name, fn}
}
func (f BuiltInFunction[RT]) GetArgs() []Arg { return nil }
func (f BuiltInFunction[RT]) Name() String   { return f.FuncName }
func (f BuiltInFunction[RT]) Invoke(parent Context, scope Class, this *Object) {
	var ctx FunctionContext
	parent.Child(&ctx, int(parent.Global().r1), scope, this)
	ctx.args = ctx.vars[:ctx.r1]
	ctx.stack[ctx.sp] = f.Fn(&ctx)
}

type CompiledFunction struct {
	Value

	FuncName     String
	Instructions Instructions
	Vars         int // Preallocate vars on stack
}

func (f CompiledFunction) Name() String { return f.FuncName }
func (f CompiledFunction) Invoke(parent Context, scope Class, this *Object) {
	frame := parent.NextFrame()
	parent.Child(&frame.ctx, f.Vars, scope, this)
	frame.ctx.args = frame.ctx.vars[:frame.ctx.r1]
	frame.sp = frame.ctx.sp
	frame.bytecode = f.Instructions
	frame.ctx.sp += f.Vars
}

func GetArgs(ctx *FunctionContext) []Value {
	return ctx.args
}
func ParseParameters(ctx *FunctionContext, params ...any) {
	// eliminate bounds' checks in loop
	_ = len(ctx.args) >= len(params)

	for i, param := range params {
		switch param := param.(type) {
		case *int:
			*param = int(ctx.args[i].(Int))
		case *Int:
			*param = ctx.args[i].(Int)
		case *float64:
			*param = float64(ctx.args[i].(Float))
		case *Float:
			*param = ctx.args[i].(Float)
		case *bool:
			*param = bool(ctx.args[i].(Bool))
		case *Bool:
			*param = ctx.args[i].(Bool)
		case *string:
			*param = string(ctx.args[i].(String))
		case *String:
			*param = ctx.args[i].(String)
		case *map[Value]Value:
			*param = make(map[Value]Value)
			for key, i := range (*(ctx.args[i].(*Array))).hash.keys {
				(*param)[key] = (*(ctx.args[i].(*Array))).hash.values[i]
			}
		case *map[any]any:
			*param = make(map[any]any)
			for key, i := range (*(ctx.args[i].(*Array))).hash.keys {
				(*param)[key] = (*(ctx.args[i].(*Array))).hash.values[i]
			}
		case *map[Value]any:
			*param = make(map[Value]any)
			for key, i := range (*(ctx.args[i].(*Array))).hash.keys {
				(*param)[key] = (*(ctx.args[i].(*Array))).hash.values[i]
			}
		case *map[any]Value:
			*param = make(map[any]Value)
			for key, i := range (*(ctx.args[i].(*Array))).hash.keys {
				(*param)[key] = (*(ctx.args[i].(*Array))).hash.values[i]
			}
		case *Array:
			*param = *(ctx.args[i].(*Array).Copy())
		case *map[string]any:
			*param = make(map[string]any)
			for name, i := range (*(ctx.args[i].(*Object))).props.keys {
				(*param)[string(name.AsString(ctx))] = (*(ctx.args[i].(*Object))).props.values[i]
			}
		case *map[string]Value:
			*param = make(map[string]Value)
			for name, i := range (*(ctx.args[i].(*Object))).props.keys {
				(*param)[string(name.AsString(ctx))] = (*(ctx.args[i].(*Object))).props.values[i]
			}
		case *map[String]Value:
			*param = make(map[String]Value)
			for name, i := range (*(ctx.args[i].(*Object))).props.keys {
				(*param)[name.AsString(ctx)] = (*(ctx.args[i].(*Object))).props.values[i]
			}
		case *Object:
			*param = *(ctx.args[i].(*Object))
		case *Value:
			*param = ctx.args[i]
		default:
			panic("unsupported type")
		}
	}
}
