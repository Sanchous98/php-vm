package vm

import (
	"fmt"
	"php-vm/pkg/binary"
	"php-vm/pkg/slices"
	"strconv"
	"strings"
)

type Instructions []uint64

func NewInstructions(bytecode []byte) (res Instructions) {
	res = make([]uint64, len(bytecode)>>3)
	binary.NativeEndian.ConvertUint64(bytecode, res)
	return
}

func (b Instructions) ReadOperation(pc int) (op Operator, r1 uint32) {
	const last32bits = uint64(^uint32(0))

	op = Operator(b[pc] >> 32)
	r1 = uint32(b[pc] & last32bits)
	return
}
func (b Instructions) String() string {
	const doubleWideOpStart = 40
	var str strings.Builder
	var ip int

	slices.Reduce(b, func(index int, operator uint64, prev *strings.Builder) *strings.Builder {
		const last32bits = 0x00000000_FFFFFFFF

		op := Operator(b[ip] >> 32)
		r1 := uint32(b[ip] & last32bits)

		if op >= doubleWideOpStart {
			prev.WriteString(fmt.Sprintf("\n%.5d: %-13s %s", ip, op.String(), strconv.FormatUint(uint64(r1), 10)))
		} else {
			prev.WriteString(fmt.Sprintf("\n%.5d: %-13s", ip, op.String()))
		}

		ip++
		return prev
	}, &str)

	return str.String()
}

//go:generate stringer -type=Operator -linecomment -output=instructions_string.go
type Operator uint32

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
	OpAssign                           // ASSIGN
	OpAssignAdd                        // ASSIGN_ADD
	OpAssignSub                        // ASSIGN_SUB
	OpAssignMul                        // ASSIGN_MUL
	OpAssignDiv                        // ASSIGN_DIV
	OpAssignMod                        // ASSIGN_MOD
	OpAssignPow                        // ASSIGN_POW
	OpAssignBwAnd                      // ASSIGN_BW_AND
	OpAssignBwOr                       // ASSIGN_BW_OR
	OpAssignBwXor                      // ASSIGN_BW_XOR
	OpAssignConcat                     // ASSIGN_CONCAT
	OpAssignShiftLeft                  // ASSIGN_LSHIFT
	OpAssignShiftRight                 // ASSIGN_RSHIFT
	OpAssignCoalesce                   // ASSIGN_COALESCE
	OpUnset                            // UNSET
	OpCast                             // CAST
	OpPreIncrement                     // PRE_INC
	OpPostIncrement                    // POST_INC
	OpPreDecrement                     // PRE_DEC
	OpPostDecrement                    // POST_DEC
	OpLoad                             // LOAD
	OpLoadRef                          // LOAD_REF
	OpConst                            // CONST
	OpJump                             // JUMP
	OpJumpTrue                         // JUMP_TRUE
	OpJumpFalse                        // JUMP_FALSE
	OpInitCall                         // INIT_CALL
	OpCall                             // CALL
	OpEcho                             // ECHO
	OpIsSet                            // ISSET
	OpForEachKey                       // FE_KEY
	OpForEachValue                     // FE_VALUE
	OpForEachValueRef                  // FE_VALUE_REF
)

func assignTryRef(ref *Value, v Value) {
	if (*ref).IsRef() {
		*(*ref).(Ref).Deref() = v
	} else {
		*ref = v
	}
}

// Identical => $x === $y
func Identical(ctx *FunctionContext) {
	ctx.sp--
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].(Identifiable).Identical(ctx.stack[ctx.sp+1])
}

// NotIdentical => $x !== $y
func NotIdentical(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(!x.(Identifiable).Identical(y))
}

// Not => !$x
func Not(ctx *FunctionContext) {
	ctx.SetTop(!ctx.Top().AsBool(ctx))
}

// Equal => $x == $y
func Equal(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(Comparable).Equal(ctx, y))
}

// NotEqual => $x != $y
func NotEqual(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(!x.(Comparable).Equal(ctx, y))
}

// LessOrEqual => $x <= $y
func LessOrEqual(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(Bool(x.(Comparable).Compare(ctx, y) < 1))
}

// GreaterOrEqual => $x >= $y
func GreaterOrEqual(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(Bool(x.(Comparable).Compare(ctx, y) > -1))
}

// Less => $x < $y
func Less(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(Bool(x.(Comparable).Compare(ctx, y) < 0))
}

// Greater => $x > $y
func Greater(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(Bool(x.(Comparable).Compare(ctx, y) > 0))
}

// Compare => $x <=> $y
func Compare(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(Comparable).Compare(ctx, y))
}

// Coalesce => $x ?? $y
// TODO: probably can be replaced with condition
func Coalesce(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()

	if x == (Null{}) {
		ctx.SetTop(y)
	}
}

// Const => 0
func Const(ctx *FunctionContext) { ctx.Push(ctx.Constants[ctx.r1]) }

// Load => $a
func Load(ctx *FunctionContext) { ctx.Push(ctx.vars[ctx.r1]) }

// LoadRef => &$a
func LoadRef(ctx *FunctionContext) { ctx.Push(NewRef(&ctx.vars[ctx.r1])) }

// Assign => $a = 0
func Assign(ctx *FunctionContext) { assignTryRef(&ctx.vars[ctx.r1], ctx.Top()) }

// AssignRef = &$a = 0
func AssignRef(ctx *FunctionContext) {
	y := ctx.Pop()
	*ctx.Top().(Ref).Deref() = y
}

// AssignAdd => $a += 1
func AssignAdd(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(Addable).Add(ctx, ctx.Top()))
	ctx.SetTop(*v)
}

// AssignSub => $a -= 1
func AssignSub(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Sub(ctx, ctx.Top()))
	ctx.SetTop(*v)
}

// AssignMul => $a *= 1
func AssignMul(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Mul(ctx, ctx.Top()))
	ctx.SetTop(*v)
}

// AssignDiv => $a /= 1
func AssignDiv(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Div(ctx, ctx.Top()))
	ctx.SetTop(*v)
}

// AssignPow => $a **= 1
func AssignPow(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Pow(ctx, ctx.Top()))
	ctx.SetTop(*v)
}

// AssignBwAnd => $a &= 1
func AssignBwAnd(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).BwAnd(ctx, ctx.Top().AsInt(ctx)))
	ctx.SetTop(*v)
}

// AssignBwOr => $a |= 1
func AssignBwOr(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).BwOr(ctx, ctx.Top().AsInt(ctx)))
	ctx.SetTop(*v)
}

// AssignBwXor => $a ^= 1
func AssignBwXor(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).BwXor(ctx, ctx.Top().AsInt(ctx)))
	ctx.SetTop(*v)
}

// AssignConcat => $a .= 1
func AssignConcat(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsString(ctx)+ctx.Top().AsString(ctx))
	ctx.SetTop(*v)
}

// AssignShiftLeft => $a <<= 1
func AssignShiftLeft(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).ShiftLeft(ctx, ctx.Top().AsInt(ctx)))
	ctx.SetTop(*v)
}

// AssignShiftRight => $a >>= 1
func AssignShiftRight(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsBits).ShiftRight(ctx, ctx.Top().AsInt(ctx)))
	ctx.SetTop(*v)
}

// AssignMod => $a %= 1
func AssignMod(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).(SupportsMath).Mod(ctx, ctx.Top()))
	ctx.SetTop(*v)
}

// AssignCoalesce => $a ??= 1
func AssignCoalesce(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	if *v == (Null{}) {
		assignTryRef(v, ctx.Top())
	}
	ctx.SetTop(*v)
}

// Jump unconditional jump; goto
func Jump(ctx *FunctionContext) {
	ctx.pc = int(ctx.r1) - 1
}

// JumpTrue if (condition)
func JumpTrue(ctx *FunctionContext) {
	if ctx.stack[ctx.sp].AsBool(ctx) {
		ctx.pc = int(ctx.r1) - 1
	}

	ctx.sp--
}

// JumpFalse for (...; condition; ...) {}
func JumpFalse(ctx *FunctionContext) {
	if !ctx.stack[ctx.sp].AsBool(ctx) {
		ctx.pc = int(ctx.r1) - 1
	}

	ctx.sp--
}

func Pop(ctx *FunctionContext) { ctx.Pop() }

func Pop2(ctx *FunctionContext) { ctx.Pop(); ctx.Pop() }

// ReturnValue => return 0;
func ReturnValue(ctx *FunctionContext) {
	v := ctx.stack[ctx.sp]
	Return(ctx)
	ctx.stack[ctx.sp] = v
}

// Return => return;
func Return(ctx *FunctionContext) {
	ctx.sp = ctx.returnSp
	ctx.fp--

	if ctx.fp >= 0 {
		ctx.frame = &ctx.frames[ctx.fp]
	}
}

// Add => 1 + 2
func Add(ctx *FunctionContext) {
	ctx.sp--
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].(Addable).Add(ctx, ctx.stack[ctx.sp+1])
}

// Sub => 1 - 2
func Sub(ctx *FunctionContext) {
	ctx.sp--
	ctx.stack[ctx.sp] = ctx.stack[ctx.sp].(SupportsMath).Sub(ctx, ctx.stack[ctx.sp+1])
}

// Mul => 1 * 2
func Mul(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsMath).Mul(ctx, y))
}

// Div => 1 / 2
func Div(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsMath).Div(ctx, y))
}

// Mod => 1 % 2
func Mod(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsMath).Mod(ctx, y))
}

// Pow => 1 ** 2
func Pow(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsMath).Pow(ctx, y))
}

// BwAnd => 1 & 2
func BwAnd(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsBits).BwAnd(ctx, y))
}

// BwOr => 1 | 2
func BwOr(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsBits).BwOr(ctx, y))
}

// BwXor => 1 ^ 2
func BwXor(ctx *FunctionContext) {
	y := ctx.Pop()
	x := ctx.Top()
	ctx.SetTop(x.(SupportsBits).BwXor(ctx, y))
}

// BwNot => ~1
func BwNot(ctx *FunctionContext) {
	ctx.SetTop(ctx.Top().(SupportsBits).BwNot(ctx))
}

// ShiftLeft => 1 << 2
func ShiftLeft(ctx *FunctionContext) {
	y := ctx.Pop().AsInt(ctx)
	x := ctx.Top()
	ctx.SetTop(x.(SupportsBits).ShiftLeft(ctx, y))
}

// ShiftRight => 1 >> 2
func ShiftRight(ctx *FunctionContext) {
	y := ctx.Pop().AsInt(ctx)
	x := ctx.Top()
	ctx.SetTop(x.(SupportsBits).ShiftRight(ctx, y))
}

// Cast => (_type_)$x
func Cast(ctx *FunctionContext) {
	ctx.SetTop(ctx.Top().Cast(ctx, Type(ctx.r1)))
}

// PreIncrement => ++$x
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
func Concat(ctx *FunctionContext) {
	y := ctx.Pop().AsString(ctx)
	x := ctx.Top().AsString(ctx)
	ctx.SetTop(x + y)
}

// Echo => echo $x, $y;
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
func IsSet(ctx *FunctionContext) {
	ctx.SetTop(Bool(ctx.Top() != Null{} && ctx.Top() != nil))
}

// ArrayNew => $x = [];
func ArrayNew(ctx *FunctionContext) {
	ctx.Push(NewArray(nil))
}

// ArrayAccessRead => $x['test']
func ArrayAccessRead(ctx *FunctionContext) {
	key := ctx.Pop()
	arr := ctx.Pop().AsArray(ctx)

	if v, ok := arr.access(key); ok {
		ctx.Push(*v.Deref())
	} else {
		ctx.Push(Null{})
	}
}

// ArrayAccessWrite => $x['test'] = 1
func ArrayAccessWrite(ctx *FunctionContext) {
	key := ctx.Pop()

	var arr *Array

	if ctx.Top().IsRef() {
		arr = ctx.Pop().AsArray(ctx)
	} else {
		arr = ctx.Top().AsArray(ctx)
	}

	ctx.Push(arr.assign(ctx, key))
}

// ArrayAccessPush => $x[] = 1
func ArrayAccessPush(ctx *FunctionContext) {
	var arr *Array

	if ctx.Top().IsRef() {
		arr = ctx.Pop().AsArray(ctx)
	} else {
		arr = ctx.Top().AsArray(ctx)
	}

	ctx.Push(arr.assign(ctx, nil))
}

// ArrayUnset => unset($x['test'])
func ArrayUnset(ctx *FunctionContext) {
	key := ctx.Pop()
	arr := ctx.Pop().AsArray(ctx)
	arr.delete(key)
	ctx.Push(arr)
}

// ForEachInit => foreach([1,2] as ...)
func ForEachInit(ctx *FunctionContext) {
	top := ctx.Top()

	switch top.(type) {
	case Iterator:
		top.(Iterator).Rewind(ctx)
	case IteratorAggregate:
		iterable := top.(IteratorAggregate).GetIterator(ctx)
		iterable.Rewind(ctx)
		ctx.SetTop(iterable)
	default:
		ctx.Throw(NewThrowable("not iterable", EError))
		return
	}
}

// ForEachKey => foreach(... as $key => ...)
func ForEachKey(ctx *FunctionContext) {
	assignTryRef(&ctx.vars[ctx.r1], ctx.Top().(Iterator).Key(ctx))
}

// ForEachValue => foreach(... as $value)
func ForEachValue(ctx *FunctionContext) {
	variable := &ctx.vars[ctx.r1]
	value := ctx.Top().(Iterator).Current(ctx)
	if value.IsRef() {
		value = *value.(Ref).Deref()
	}
	assignTryRef(variable, value)
}

// ForEachValueRef => foreach(... as &$value)
func ForEachValueRef(ctx *FunctionContext) {
	assignTryRef(&ctx.vars[ctx.r1], ctx.Top().(Iterator).Current(ctx))
}

func ForEachNext(ctx *FunctionContext) { ctx.Top().(Iterator).Next(ctx) }

// ForEachValid checks if there are items in iterator
func ForEachValid(ctx *FunctionContext) {
	ctx.Push(ctx.Top().(Iterator).Valid(ctx))
}

// Throw => throw new Exception();
func Throw(ctx *FunctionContext) {}

// InitCall => someFunction(...args...);
func InitCall(ctx *FunctionContext) {
	ctx.Push(ctx.Functions[ctx.r1])
}

// InitCallVar => $someFunctionName(...args...);
func InitCallVar(ctx *FunctionContext) {
	ctx.SetTop(ctx.Top().(*Function)) // TODO: Must actually convert to function
}

func Call(ctx *FunctionContext) {
	ctx.sp -= int(ctx.r1)
	ctx.stack[ctx.sp].(*Function).Invoke(ctx)
}

func Unset(ctx *FunctionContext) { ctx.vars[ctx.r1] = Null{} }
