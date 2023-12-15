package vm

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

type Instructions []uint64

func NewInstructions(bytecode []byte) Instructions {
	res := make([]uint64, 0, len(bytecode)>>3)

	for ip := 0; ip < len(bytecode); ip += 8 {
		res = append(res, binary.NativeEndian.Uint64(bytecode[ip:]))
	}

	return res
}

func (b Instructions) ReadOperation(ctx *FunctionContext) (op Operator) {
	if op = Operator(b[ctx.pc]); op > _opOneOperand {
		ctx.pc++
		ctx.r1 = b[ctx.pc]
	}

	return
}
func (b Instructions) String() string {
	var ip int
	return string(Reduce(b, func(prev String, operator Operator, operands ...int) String {
		strOperands := make([]string, 0, len(operands))

		for _, op := range operands {
			strOperands = append(strOperands, strconv.FormatUint(uint64(op), 10))
		}

		prev += String(fmt.Sprintf("\n%.5d: %-13s %s", ip, operator.String(), strings.Join(strOperands, ", ")))
		ip += 1 + len(operands)
		return prev
	}, ""))
}

func Reduce[T any, F ~func(prev T, operator Operator, operands ...int) T](bytecode Instructions, f F, start T) T {
	ip := 0
	registers := make([]int, 0, 1)

	for ip < len(bytecode) {
		op := Operator(bytecode[ip])

		if op > _opOneOperand {
			ip++
			registers = append(registers, int(bytecode[ip]))
		}

		start = f(start, op, registers...)
		ip++
		registers = registers[:0]
	}

	return start
}

//go:generate stringer -type=Operator -linecomment
type Operator uint64

const (
	OpNoop             Operator = iota // NOOP
	OpPop                              // POP
	OpPop2                             // POP2
	OpReturn                           // RETURN
	OpReturnValue                      // RETURN_VAL
	OpAdd                              // ADD
	OpSub                              // SUB
	OpMul                              // MUL
	OpDiv                              // DIV
	OpMod                              // MOD
	OpPow                              // POW
	OpBwAnd                            // BW_AND
	OpBwOr                             // BW_OR
	OpBwXor                            // BW_XOR
	OpBwNot                            // BW_NOT
	OpShiftLeft                        // LSHIFT
	OpShiftRight                       // RSHIFT
	OpEqual                            // EQUAL
	OpNotEqual                         // NOT_EQUAL
	OpIdentical                        // IDENTICAL
	OpNotIdentical                     // NOT_IDENTICAL
	OpNot                              // NOT
	OpGreater                          // GT
	OpLess                             // LT
	OpGreaterOrEqual                   // GTE
	OpLessOrEqual                      // LTE
	OpCompare                          // COMPARE
	OpCoalesce                         // COALESCE
	OpAssignRef                        // ASSIGN_REF
	OpArrayNew                         // ARRAY_NEW
	OpArrayAccessRead                  // ARRAY_ACCESS_READ
	OpArrayAccessWrite                 // ARRAY_ACCESS_WRITE
	OpArrayAccessPush                  // ARRAY_ACCESS_PUSH
	OpArrayUnset                       // ARRAY_UNSET
	OpConcat                           // CONCAT
	OpForEachInit                      // FE_INIT
	OpForEachNext                      // FE_NEXT
	OpForEachValid                     // FE_VALID
	OpThrow                            // THROW
	OpInitCallVar                      // INIT_CALL_VAR

	_opOneOperand      Operator = iota - 1
	OpAssign                    // ASSIGN
	OpAssignAdd                 // ASSIGN_ADD
	OpAssignSub                 // ASSIGN_SUB
	OpAssignMul                 // ASSIGN_MUL
	OpAssignDiv                 // ASSIGN_DIV
	OpAssignMod                 // ASSIGN_MOD
	OpAssignPow                 // ASSIGN_POW
	OpAssignBwAnd               // ASSIGN_BW_AND
	OpAssignBwOr                // ASSIGN_BW_OR
	OpAssignBwXor               // ASSIGN_BW_XOR
	OpAssignConcat              // ASSIGN_CONCAT
	OpAssignShiftLeft           // ASSIGN_LSHIFT
	OpAssignShiftRight          // ASSIGN_RSHIFT
	OpAssignCoalesce            // ASSIGN_COALESCE
	OpUnset                     // UNSET
	OpCast                      // CAST
	OpPreIncrement              // PRE_INC
	OpPostIncrement             // POST_INC
	OpPreDecrement              // PRE_DEC
	OpPostDecrement             // POST_DEC
	OpLoad                      // LOAD
	OpLoadRef                   // LOAD_REF
	OpConst                     // CONST
	OpJump                      // JUMP
	OpJumpTrue                  // JUMP_TRUE
	OpJumpFalse                 // JUMP_FALSE
	OpInitCall                  // INIT_CALL
	OpCall                      // CALL
	OpEcho                      // ECHO
	OpIsSet                     // ISSET
	OpForEachKey                // FE_KEY
	OpForEachValue              // FE_VALUE
	OpForEachValueRef           // FE_VALUE_REF
)

func assignTryRef(ref *Value, v Value) {
	if (*ref).IsRef() {
		*(*ref).(Ref).Deref() = v
	} else {
		*ref = v
	}
}

// Identical => $x === $y
//
//go:nosplit
func Identical(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(Identifiable).Identical(ctx.stack[ctx.sp])
	ctx.sp--
}

// NotIdentical => $x !== $y
//
//go:nosplit
func NotIdentical(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = !ctx.stack[ctx.sp-1].(Identifiable).Identical(ctx.stack[ctx.sp])
	ctx.sp--
}

// Not => !$x
//
//go:nosplit
func Not(ctx *FunctionContext) {
	ctx.stack[ctx.sp] = !ctx.stack[ctx.sp].AsBool(ctx)
}

// Equal => $x == $y
//
//go:nosplit
func Equal(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(Comparable).Equal(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// NotEqual => $x != $y
//
//go:nosplit
func NotEqual(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = !ctx.stack[ctx.sp-1].(Comparable).Equal(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// LessOrEqual => $x <= $y
//
//go:nosplit
func LessOrEqual(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = Bool(ctx.stack[ctx.sp-1].(Comparable).Compare(ctx, ctx.stack[ctx.sp]) < 1)
	ctx.sp--
}

// GreaterOrEqual => $x >= $y
//
//go:nosplit
func GreaterOrEqual(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = Bool(ctx.stack[ctx.sp-1].(Comparable).Compare(ctx, ctx.stack[ctx.sp]) > -1)
	ctx.sp--
}

// Less => $x < $y
//
//go:nosplit
func Less(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = Bool(ctx.stack[ctx.sp-1].(Comparable).Compare(ctx, ctx.stack[ctx.sp]) < 0)
	ctx.sp--
}

// Greater => $x > $y
//
//go:nosplit
func Greater(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = Bool(ctx.stack[ctx.sp-1].(Comparable).Compare(ctx, ctx.stack[ctx.sp]) > 0)
	ctx.sp--
}

// Compare => $x <=> $y
//
//go:nosplit
func Compare(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(Comparable).Compare(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// Coalesce => $x ?? $y
// TODO: probably can be replaced with condition
//
//go:nosplit
func Coalesce(ctx *FunctionContext) {
	if ctx.stack[ctx.sp-1] == (Null{}) {
		ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp]
	}
	ctx.sp--
}

// Const => 0
//
//go:nosplit
func Const(ctx *FunctionContext) {
	ctx.sp++
	ctx.stack[ctx.sp] = ctx.Constants[ctx.r1]
}

// Load => $a
//
//go:nosplit
func Load(ctx *FunctionContext) {
	ctx.sp++
	ctx.stack[ctx.sp] = ctx.vars[ctx.r1]
}

// LoadRef => &$a
//
//go:nosplit
func LoadRef(ctx *FunctionContext) {
	ctx.sp++
	ctx.stack[ctx.sp] = NewRef(&ctx.vars[ctx.r1])
}

// Assign => $a = 0
//
//go:nosplit
func Assign(ctx *FunctionContext) { assignTryRef(&ctx.vars[ctx.r1], ctx.stack[ctx.sp]) }

// AssignRef = &$a = 0
//
//go:nosplit
func AssignRef(ctx *FunctionContext) {
	*ctx.stack[ctx.sp-1].(Ref).Deref() = ctx.stack[ctx.sp]
	ctx.sp--
}

// AssignAdd => $a += 1
//
//go:nosplit
func AssignAdd(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(Addable).Add(ctx, ctx.stack[ctx.sp]))
	ctx.stack[ctx.sp] = *v
}

// AssignSub => $a -= 1
//
//go:nosplit
func AssignSub(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Sub(ctx, ctx.stack[ctx.sp]))
	ctx.stack[ctx.sp] = *v
}

// AssignMul => $a *= 1
//
//go:nosplit
func AssignMul(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Mul(ctx, ctx.stack[ctx.sp]))
	ctx.stack[ctx.sp] = *v
}

// AssignDiv => $a /= 1
//
//go:nosplit
func AssignDiv(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Div(ctx, ctx.stack[ctx.sp]))
	ctx.stack[ctx.sp] = *v
}

// AssignPow => $a **= 1
//
//go:nosplit
func AssignPow(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Pow(ctx, ctx.stack[ctx.sp]))
	ctx.stack[ctx.sp] = *v
}

// AssignBwAnd => $a &= 1
//
//go:nosplit
func AssignBwAnd(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).BwAnd(ctx, ctx.stack[ctx.sp].AsInt(ctx)))
	ctx.stack[ctx.sp] = *v
}

// AssignBwOr => $a |= 1
//
//go:nosplit
func AssignBwOr(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).BwOr(ctx, ctx.stack[ctx.sp].AsInt(ctx)))
	ctx.stack[ctx.sp] = *v
}

// AssignBwXor => $a ^= 1
//
//go:nosplit
func AssignBwXor(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).BwXor(ctx, ctx.stack[ctx.sp].AsInt(ctx)))
	ctx.stack[ctx.sp] = *v
}

// AssignConcat => $a .= 1
//
//go:nosplit
func AssignConcat(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsString(ctx)+ctx.stack[ctx.sp].AsString(ctx))
	ctx.stack[ctx.sp] = *v
}

// AssignShiftLeft => $a <<= 1
//
//go:nosplit
func AssignShiftLeft(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).ShiftLeft(ctx, ctx.stack[ctx.sp].AsInt(ctx)))
	ctx.stack[ctx.sp] = *v
}

// AssignShiftRight => $a >>= 1
//
//go:nosplit
func AssignShiftRight(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).ShiftRight(ctx, ctx.stack[ctx.sp].AsInt(ctx)))
	ctx.stack[ctx.sp] = *v
}

// AssignMod => $a %= 1
//
//go:nosplit
func AssignMod(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Mod(ctx, ctx.stack[ctx.sp]))
	ctx.stack[ctx.sp] = *v
}

// AssignCoalesce => $a ??= 1
//
//go:nosplit
func AssignCoalesce(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	if *v == (Null{}) {
		assignTryRef(v, (*v).(SupportsMath).Mod(ctx, ctx.stack[ctx.sp]))
	}
	ctx.stack[ctx.sp] = *v
}

// Jump unconditional jump; goto
//
//go:nosplit
func Jump(ctx *FunctionContext) { ctx.pc = int(ctx.r1) - 1 }

// JumpTrue if (condition)
//
//go:nosplit
func JumpTrue(ctx *FunctionContext) {
	if ctx.stack[ctx.sp].AsBool(ctx) {
		Jump(ctx)
	}
	ctx.sp--
}

// JumpFalse for (...; condition; ...) {}
//
//go:nosplit
func JumpFalse(ctx *FunctionContext) {
	if !ctx.stack[ctx.sp].AsBool(ctx) {
		Jump(ctx)
	}
	ctx.sp--
}

//go:nosplit
func Pop(ctx *FunctionContext) { ctx.sp -= 1 }

//go:nosplit
func Pop2(ctx *FunctionContext) { ctx.sp -= 2 }

// ReturnValue => return 0;
//
//go:nosplit
func ReturnValue(ctx *FunctionContext) {
	v := ctx.stack[ctx.sp]
	Return(ctx)
	ctx.stack[ctx.sp] = v
}

// Return => return;
//
//go:nosplit
func Return(ctx *FunctionContext) { ctx.sp = ctx.PopFrame().sp }

// Add => 1 + 2
//
//go:nosplit
func Add(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(Addable).Add(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// Sub => 1 - 2
//
//go:nosplit
func Sub(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsMath).Sub(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// Mul => 1 * 2
//
//go:nosplit
func Mul(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsMath).Mul(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// Div => 1 / 2
//
//go:nosplit
func Div(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsMath).Div(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// Mod => 1 % 2
//
//go:nosplit
func Mod(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsMath).Mod(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// Pow => 1 ** 2
//
//go:nosplit
func Pow(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsMath).Pow(ctx, ctx.stack[ctx.sp])
	ctx.sp--
}

// BwAnd => 1 & 2
//
//go:nosplit
func BwAnd(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsBits).BwAnd(ctx, ctx.stack[ctx.sp].AsInt(ctx))
	ctx.sp--
}

// BwOr => 1 | 2
//
//go:nosplit
func BwOr(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsBits).BwOr(ctx, ctx.stack[ctx.sp].AsInt(ctx))
	ctx.sp--
}

// BwXor => 1 ^ 2
//
//go:nosplit
func BwXor(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsBits).BwXor(ctx, ctx.stack[ctx.sp].AsInt(ctx))
	ctx.sp--
}

// BwNot => ~1
//
//go:nosplit
func BwNot(ctx *FunctionContext) {
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].(SupportsBits).BwNot(ctx)
}

// ShiftLeft => 1 << 2
//
//go:nosplit
func ShiftLeft(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsBits).ShiftLeft(ctx, ctx.stack[ctx.sp].AsInt(ctx))
	ctx.sp--
}

// ShiftRight => 1 >> 2
//
//go:nosplit
func ShiftRight(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].(SupportsBits).ShiftRight(ctx, ctx.stack[ctx.sp].AsInt(ctx))
	ctx.sp--
}

// Cast => (_type_)$x
//
//go:nosplit
func Cast(ctx *FunctionContext) {
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].Cast(ctx, Type(ctx.r1))
}

// PreIncrement => ++$x
//
//go:nosplit
func PreIncrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	switch (*v).(type) {
	case Float:
		*v = (*v).(Float) + 1
	default:
		*v = (*v).AsInt(ctx) + 1
	}

	ctx.Push(*v)
}

// PreDecrement => --$x
//
//go:nosplit
func PreDecrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	switch (*v).(type) {
	case Float:
		*v = (*v).(Float) - 1
	default:
		*v = (*v).AsInt(ctx) - 1
	}

	ctx.Push(*v)
}

// PostIncrement => $x++
//
//go:nosplit
func PostIncrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	ctx.Push(*v)

	switch (*v).(type) {
	case Float:
		*v = (*v).(Float) + 1
	default:
		*v = (*v).AsInt(ctx) + 1
	}
}

// PostDecrement => $x--
//
//go:nosplit
func PostDecrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	ctx.Push(*v)

	switch (*v).(type) {
	case Float:
		*v = (*v).(Float) - 1
	default:
		*v = (*v).AsInt(ctx) - 1
	}
}

// Concat => $a . "string"
//
//go:nosplit
func Concat(ctx *FunctionContext) {
	ctx.stack[ctx.sp-1] = ctx.stack[ctx.sp-1].AsString(ctx) + ctx.stack[ctx.sp].AsString(ctx)
}

// Echo => echo $x, $y;
//
//go:nosplit
func Echo(ctx *FunctionContext) {
	count := ctx.r1
	values := make([]any, count)

	for i, v := range ctx.Slice(-int(count), 0) {
		// Need to convert every value to native Go string,
		// because fmt doesn't print POSIX control characters included in value of underlying type
		values[i] = string(v.AsString(ctx))
	}

	if _, err := fmt.Fprint(ctx.Output(), values...); err != nil {
		ctx.Throw(NewThrowable(err.Error(), ECoreError))
	}
}

// IsSet => isset($x)
//
//go:nosplit
func IsSet(ctx *FunctionContext) {
	ctx.stack[ctx.sp] = Bool(ctx.stack[ctx.sp] != Null{} && ctx.stack[ctx.sp] != nil)
}

// ArrayNew => $x = [];
//
//go:nosplit
func ArrayNew(ctx *FunctionContext) {
	ctx.sp++
	ctx.stack[ctx.sp] = NewArray(nil)
}

// ArrayAccessRead => $x['test']
//
//go:nosplit
func ArrayAccessRead(ctx *FunctionContext) {
	key := ctx.stack[ctx.sp]
	arr := ctx.stack[ctx.sp-1].AsArray(ctx)

	if v, ok := arr.access(key); ok {
		ctx.stack[ctx.sp-1] = *v.Deref()
	} else {
		ctx.stack[ctx.sp-1] = Null{}
	}
	ctx.sp--
}

// ArrayAccessWrite => $x['test'] = 1
//
//go:nosplit
func ArrayAccessWrite(ctx *FunctionContext) {
	key := ctx.stack[ctx.sp]
	arr := ctx.stack[ctx.sp-1].AsArray(ctx)

	if ctx.stack[ctx.sp-1].IsRef() {
		ctx.sp--
	}

	ctx.stack[ctx.sp] = arr.assign(ctx, key)
}

// ArrayAccessPush => $x[] = 1
//
//go:nosplit
func ArrayAccessPush(ctx *FunctionContext) {
	arr := ctx.stack[ctx.sp].AsArray(ctx)

	if ctx.Top().IsRef() {
		ctx.sp--
	}

	ctx.sp++
	ctx.stack[ctx.sp] = arr.assign(ctx, nil)
}

// ArrayUnset => unset($x['test'])
//
//go:nosplit
func ArrayUnset(ctx *FunctionContext) {
	key := ctx.stack[ctx.sp]
	arr := ctx.stack[ctx.sp-1].AsArray(ctx)
	ctx.sp--
	arr.delete(key)
	ctx.stack[ctx.sp] = arr
}

// ForEachInit => foreach([1,2] as ...)
//
//go:nosplit
func ForEachInit(ctx *FunctionContext) {
	iterable := ctx.stack[ctx.sp]
	switch iterable.(type) {
	case Iterator:
	case IteratorAggregate:
		iterable = iterable.(IteratorAggregate).GetIterator(ctx)
	default:
		ctx.Throw(NewThrowable("not iterable", EError))
		return
	}
	iterable.(Iterator).Rewind(ctx)
	ctx.stack[ctx.sp] = iterable
}

// ForEachKey => foreach(... as $key => ...)
//
//go:nosplit
func ForEachKey(ctx *FunctionContext) {
	assignTryRef(&ctx.vars[ctx.r1], ctx.stack[ctx.sp].(Iterator).Key(ctx))
}

// ForEachValue => foreach(... as $value)
//
//go:nosplit
func ForEachValue(ctx *FunctionContext) {
	variable := &ctx.vars[ctx.r1]
	value := ctx.stack[ctx.sp].(Iterator).Current(ctx)
	if value.IsRef() {
		value = *value.(Ref).Deref()
	}
	assignTryRef(variable, value)
}

// ForEachValueRef => foreach(... as &$value)
//
//go:nosplit
func ForEachValueRef(ctx *FunctionContext) {
	assignTryRef(&ctx.vars[ctx.r1], ctx.stack[ctx.sp].(Iterator).Current(ctx))
}

//go:nosplit
func ForEachNext(ctx *FunctionContext) { ctx.stack[ctx.sp].(Iterator).Next(ctx) }

// ForEachValid checks if there are items in iterator
//
//go:nosplit
func ForEachValid(ctx *FunctionContext) {
	ctx.stack[ctx.sp+1] = ctx.stack[ctx.sp].(Iterator).Valid(ctx)
	ctx.sp++
}

// Throw => throw new Exception();
//
//go:nosplit
func Throw(ctx *FunctionContext) {}

// InitCall => someFunction(...args...);
//
//go:nosplit
func InitCall(ctx *FunctionContext) {
	ctx.sp++
	ctx.stack[ctx.sp] = ctx.Functions[ctx.r1].(Value)
}

// InitCallVar => $someFunctionName(...args...);
//
//go:nosplit
func InitCallVar(ctx *FunctionContext) {
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].AsCallable(ctx).(Value)
}

//go:nosplit
func Call(ctx *FunctionContext) {
	ctx.sp -= int(ctx.r1)
	ctx.stack[ctx.sp].(Callable).Invoke(ctx, nil, nil)
}

//go:nosplit
func CallMethod(ctx *FunctionContext) {
	ctx.sp -= int(ctx.r1)
	fn := ctx.stack[ctx.sp]
	obj := ctx.stack[ctx.sp-1].AsObject(ctx)
	fn.(Callable).Invoke(ctx, nil, obj)
	ctx.sp--
}

//go:nosplit
func CallStaticMethod(ctx *FunctionContext) {
	ctx.sp -= int(ctx.r1)
	fn := ctx.stack[ctx.sp]
	cls := ctx.stack[ctx.sp-1].AsString(ctx)
	fn.(Callable).Invoke(ctx, ctx.ClassByName(cls), nil)
	ctx.sp--
}

//go:nosplit
func Unset(ctx *FunctionContext) { ctx.vars[ctx.r1] = Null{} }
