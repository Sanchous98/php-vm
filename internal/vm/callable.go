package vm

import (
	"unsafe"
)

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
	Fn   func(...Value) RT
}

func NewBuiltInFunction[RT Value, F ~func(...Value) RT](fn F, args ...Arg) BuiltInFunction[RT] {
	return BuiltInFunction[RT]{args, fn}
}
func (f BuiltInFunction[RT]) GetArgs() []Arg { return f.Args }
func (f BuiltInFunction[RT]) Invoke(ctx Context) Value {
	args := ctx.Slice(-len(f.Args), 0)
	res := f.Fn(f.Args.Map(ctx, args)...)
	ctx.MovePointer(-len(f.Args))
	return res
}

type CompiledFunction struct {
	Instructions Bytecode
	Args, Vars   int
}

func (f CompiledFunction) Invoke(parent Context) Value {
	global := parent.Global()

	ctx := FunctionContext{}
	ctx.Context = parent
	ctx.global = global
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
		switch f.Instructions.ReadOperation(noescape(&ctx)) {
		case OpPop:
			Pop(noescape(&ctx))
		case OpReturn:
			Return(noescape(&ctx))
			return nil
		case OpReturnValue:
			ReturnValue(noescape(&ctx))
			return ctx.Pop()
		case OpAdd:
			Add(noescape(&ctx))
		case OpSub:
			Sub(noescape(&ctx))
		case OpMul:
			Mul(noescape(&ctx))
		case OpDiv:
			Div(noescape(&ctx))
		case OpMod:
			Mod(noescape(&ctx))
		case OpPow:
			Pow(noescape(&ctx))
		case OpBwAnd:
			BwAnd(noescape(&ctx))
		case OpBwOr:
			BwOr(noescape(&ctx))
		case OpBwXor:
			BwXor(noescape(&ctx))
		case OpBwNot:
			BwNot(noescape(&ctx))
		case OpShiftLeft:
			ShiftLeft(noescape(&ctx))
		case OpShiftRight:
			ShiftRight(noescape(&ctx))
		case OpEqual:
			Equal(noescape(&ctx))
		case OpNotEqual:
			NotEqual(noescape(&ctx))
		case OpIdentical:
			Identical(noescape(&ctx))
		case OpNotIdentical:
			NotIdentical(noescape(&ctx))
		case OpNot:
			Not(noescape(&ctx))
		case OpGreater:
			Greater(noescape(&ctx))
		case OpLess:
			Less(noescape(&ctx))
		case OpGreaterOrEqual:
			GreaterOrEqual(noescape(&ctx))
		case OpLessOrEqual:
			LessOrEqual(noescape(&ctx))
		case OpCompare:
			Compare(noescape(&ctx))
		case OpArrayInit:
			ArrayInit(noescape(&ctx))
		case OpArrayLookup:
			ArrayLookup(noescape(&ctx))
		case OpArrayInsert:
			ArrayInsert(noescape(&ctx))
		case OpArrayPush:
			ArrayPush(noescape(&ctx))
		case OpConcat:
			Concat(noescape(&ctx))
		case OpRopeInit:
			RopeInit(noescape(&ctx))
		case OpRopePush:
			RopePush(noescape(&ctx))
		case OpRopeEnd:
			RopeEnd(noescape(&ctx))
		case OpAssertType:
			AssertType(noescape(&ctx))
		case OpAssign:
			Assign(noescape(&ctx))
		case OpAssignAdd:
			AssignAdd(noescape(&ctx))
		case OpAssignSub:
			AssignSub(noescape(&ctx))
		case OpAssignMul:
			AssignMul(noescape(&ctx))
		case OpAssignDiv:
			AssignDiv(noescape(&ctx))
		case OpAssignMod:
			AssignMod(noescape(&ctx))
		case OpAssignPow:
			AssignPow(noescape(&ctx))
		case OpAssignBwAnd:
			AssignBwAnd(noescape(&ctx))
		case OpAssignBwOr:
			AssignBwOr(noescape(&ctx))
		case OpAssignBwXor:
			AssignBwXor(noescape(&ctx))
		case OpAssignConcat:
			AssignConcat(noescape(&ctx))
		case OpAssignShiftLeft:
			AssignShiftLeft(noescape(&ctx))
		case OpAssignShiftRight:
			AssignShiftRight(noescape(&ctx))
		case OpCast:
			Cast(noescape(&ctx))
		case OpPreIncrement:
			PreIncrement(noescape(&ctx))
		case OpPostIncrement:
			PostIncrement(noescape(&ctx))
		case OpPreDecrement:
			PreDecrement(noescape(&ctx))
		case OpPostDecrement:
			PostDecrement(noescape(&ctx))
		case OpLoad:
			Load(noescape(&ctx))
		case OpLoadRef:
			LoadRef(noescape(&ctx))
		case OpConst:
			Const(noescape(&ctx))
		case OpJump:
			Jump(noescape(&ctx))
		case OpJumpTrue:
			JumpTrue(noescape(&ctx))
		case OpJumpFalse:
			JumpFalse(noescape(&ctx))
		case OpCall:
			Call(noescape(&ctx))
		case OpEcho:
			Echo(noescape(&ctx))
		case OpIsSet:
			IsSet(noescape(&ctx))
		}
	}

	return nil
}

func noescape[T any](v *T) *T {
	x := uintptr(unsafe.Pointer(v)) ^ 0
	return (*T)(unsafe.Pointer(x))
}
