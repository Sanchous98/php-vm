package vm

import (
	"php-vm/pkg/slices"
)

type Callable interface {
	Value
	Invoke(Context, Class, *Object)
}

type Arg struct {
	Name     string
	Default  Value
	Type     TypeShape
	ByRef    bool
	Variadic bool
}

type argList []Arg

func (a argList) Map(ctx Context, args []Value) ([]Value, Throwable) {
	if len(args) < len(slices.Filter(a, func(i int, arg Arg) bool { return !arg.Variadic && arg.Default == nil })) {
		return nil, NewThrowable("not enough arguments", EError)
	}

	i := 0
	for _, arg := range a {
		if args[i] == nil {
			args[i] = arg.Default
		}

		if arg.Type > 0 {
			args[i] = args[i].Cast(ctx, arg.Type)
		}

		if arg.ByRef {
			args[i] = NewRef(&args[i])
		}

		if arg.Variadic {
			variadics := make(map[Value]Value, len(args[i:]))
			for j, v := range args[i:] {
				if arg.Type > 0 {
					variadics[Int(j)] = v.Cast(ctx, arg.Type)
				}
			}
			args = args[:i+1]
			args[i] = NewArray(variadics, Int(len(variadics)))
			break
		}

		i++
	}

	return args, nil
}

type BuiltInFunction[RT Value] struct {
	Value // Only for sake of possibility to put function on stack

	Name String
	Fn   func(*FunctionContext) RT
}

func NewBuiltInFunction[RT Value, F ~func(*FunctionContext) RT](fn F, name String) BuiltInFunction[RT] {
	return BuiltInFunction[RT]{nil, name, fn}
}
func (f BuiltInFunction[RT]) GetArgs() []Arg { return nil }
func (f BuiltInFunction[RT]) Invoke(parent Context, scope Class, this *Object) {
	var ctx FunctionContext
	parent.Child(&ctx, int(parent.Global().r1), scope, this)
	ctx.Args = ctx.vars[:ctx.r1]
	res := f.Fn(&ctx)
	ctx.SetTop(res)
}

type CompiledFunction struct {
	Value // Only for sake of possibility to put function on stack

	Name         String
	Instructions Instructions
	Vars         int // Preallocate vars on stack
}

func (f CompiledFunction) Invoke(parent Context, scope Class, this *Object) {
	frame := parent.Global().NextFrame()
	parent.Child(&frame.ctx, f.Vars, scope, this)
	frame.ctx.Args = frame.ctx.vars[:frame.ctx.r1]
	frame.fp = parent.TopIndex()
	frame.bytecode = f.Instructions
	frame.ctx.sp += f.Vars
}

func ParseParameters(ctx *FunctionContext, params ...any) {
	// eliminate bounds' checks in loop
	_ = len(ctx.Args) >= len(params)

	for i, param := range params {
		switch param := param.(type) {
		case *int:
			*param = int(ctx.Args[i].(Int))
		case *Int:
			*param = ctx.Args[i].(Int)
		case *float64:
			*param = float64(ctx.Args[i].(Float))
		case *Float:
			*param = ctx.Args[i].(Float)
		case *bool:
			*param = bool(ctx.Args[i].(Bool))
		case *Bool:
			*param = ctx.Args[i].(Bool)
		case *string:
			*param = string(ctx.Args[i].(String))
		case *String:
			*param = ctx.Args[i].(String)
		case *map[Value]Value:
			*param = make(map[Value]Value)
			for key, value := range (*(ctx.Args[i].(*Array))).hash.internal {
				(*param)[key] = value.v
			}
		case *map[any]any:
			*param = make(map[any]any)
			for key, value := range (*(ctx.Args[i].(*Array))).hash.internal {
				(*param)[key] = value.v
			}
		case *map[Value]any:
			*param = make(map[Value]any)
			for key, value := range (*(ctx.Args[i].(*Array))).hash.internal {
				(*param)[key] = value.v
			}
		case *map[any]Value:
			*param = make(map[any]Value)
			for key, value := range (*(ctx.Args[i].(*Array))).hash.internal {
				(*param)[key] = value.v
			}
		case *Array:
			*param = *(ctx.Args[i].(*Array).Copy())
		case *map[string]any:
			*param = make(map[string]any)
			for name, value := range (*(ctx.Args[i].(*Object))).props.internal {
				(*param)[string(name)] = value.v
			}
		case *map[string]Value:
			*param = make(map[string]Value)
			for name, value := range (*(ctx.Args[i].(*Object))).props.internal {
				(*param)[string(name)] = value.v
			}
		case *map[String]Value:
			*param = make(map[String]Value)
			for name, value := range (*(ctx.Args[i].(*Object))).props.internal {
				(*param)[name] = value.v
			}
		case *Object:
			*param = *(ctx.Args[i].(*Object))
		case *Value:
			*param = ctx.Args[i]
		default:
			panic("unsupported type")
		}
	}
}
