package vm

type Callable interface {
	Invoke(Context) Value
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
func (f BuiltInFunction[RT]) Invoke(ctx Context) Value {
	args := ctx.Slice(-len(f.Args), 0)
	res := f.Fn(ctx, f.Args.Map(ctx, args)...)
	ctx.MovePointer(-len(f.Args))
	return res
}

type CompiledFunction struct {
	Instructions Bytecode
	Args, Vars   int
}

func (f CompiledFunction) Invoke(parent Context) Value {
	ctx := FunctionContext{}
	ctx.Context = parent
	ctx.global = parent.Global()
	ctx.vars = ctx.global.Slice(-f.Args, f.Vars)

	for i := range ctx.vars {
		v := &ctx.vars[i]
		if *v == nil {
			*v = Null{}
		}
	}

	ctx.args = ctx.vars[:len(ctx.vars)-f.Vars]
	ctx.fp = parent.TopIndex() - f.Args
	parent.MovePointer(f.Vars + f.Args)

	for ctx.pc = 0; ctx.pc<<3 < len(f.Instructions); ctx.pc++ {
		switch f.Instructions.ReadOperation(&ctx) {
		case OpPop:
			Pop(&ctx)
		case OpPop2:
			Pop2(&ctx)
		case OpReturn:
			Return(&ctx)
			return nil
		case OpReturnValue:
			ReturnValue(&ctx)
			return ctx.Pop()
		case OpAdd:
			Add(&ctx)
		case OpSub:
			Sub(&ctx)
		case OpMul:
			Mul(&ctx)
		case OpDiv:
			Div(&ctx)
		case OpMod:
			Mod(&ctx)
		case OpPow:
			Pow(&ctx)
		case OpBwAnd:
			BwAnd(&ctx)
		case OpBwOr:
			BwOr(&ctx)
		case OpBwXor:
			BwXor(&ctx)
		case OpBwNot:
			BwNot(&ctx)
		case OpShiftLeft:
			ShiftLeft(&ctx)
		case OpShiftRight:
			ShiftRight(&ctx)
		case OpEqual:
			Equal(&ctx)
		case OpNotEqual:
			NotEqual(&ctx)
		case OpIdentical:
			Identical(&ctx)
		case OpNotIdentical:
			NotIdentical(&ctx)
		case OpNot:
			Not(&ctx)
		case OpGreater:
			Greater(&ctx)
		case OpLess:
			Less(&ctx)
		case OpGreaterOrEqual:
			GreaterOrEqual(&ctx)
		case OpLessOrEqual:
			LessOrEqual(&ctx)
		case OpCompare:
			Compare(&ctx)
		case OpAssignRef:
			AssignRef(&ctx)
		case OpArrayNew:
			ArrayNew(&ctx)
		case OpArrayAccessRead:
			ArrayAccessRead(&ctx)
		case OpArrayAccessWrite:
			ArrayAccessWrite(&ctx)
		case OpArrayAccessPush:
			ArrayAccessPush(&ctx)
		case OpArrayUnset:
			ArrayUnset(&ctx)
		case OpConcat:
			Concat(&ctx)
		case OpUnset:
			// TODO: Unset
		case OpForEachInit:
			ForEachInit(&ctx)
		case OpForEachNext:
			ForEachNext(&ctx)
		case OpForEachValid:
			ForEachValid(&ctx)
		case OpAssertType:
			AssertType(&ctx)
		case OpAssign:
			Assign(&ctx)
		case OpAssignAdd:
			AssignAdd(&ctx)
		case OpAssignSub:
			AssignSub(&ctx)
		case OpAssignMul:
			AssignMul(&ctx)
		case OpAssignDiv:
			AssignDiv(&ctx)
		case OpAssignMod:
			AssignMod(&ctx)
		case OpAssignPow:
			AssignPow(&ctx)
		case OpAssignBwAnd:
			AssignBwAnd(&ctx)
		case OpAssignBwOr:
			AssignBwOr(&ctx)
		case OpAssignBwXor:
			AssignBwXor(&ctx)
		case OpAssignConcat:
			AssignConcat(&ctx)
		case OpAssignShiftLeft:
			AssignShiftLeft(&ctx)
		case OpAssignShiftRight:
			AssignShiftRight(&ctx)
		case OpCast:
			Cast(&ctx)
		case OpPreIncrement:
			PreIncrement(&ctx)
		case OpPostIncrement:
			PostIncrement(&ctx)
		case OpPreDecrement:
			PreDecrement(&ctx)
		case OpPostDecrement:
			PostDecrement(&ctx)
		case OpLoad:
			Load(&ctx)
		case OpLoadRef:
			LoadRef(&ctx)
		case OpConst:
			Const(&ctx)
		case OpJump:
			Jump(&ctx)
		case OpJumpTrue:
			JumpTrue(&ctx)
		case OpJumpFalse:
			JumpFalse(&ctx)
		case OpCall:
			Call(&ctx)
		case OpEcho:
			Echo(&ctx)
		case OpIsSet:
			IsSet(&ctx)
		case OpForEachKey:
			ForEachKey(&ctx)
		case OpForEachValue:
			ForEachValue(&ctx)
		case OpForEachValueRef:
			ForEachValueRef(&ctx)
		}
	}

	return nil
}
