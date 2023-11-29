package vm

import (
	"encoding/binary"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
)

type Bytecode []byte

func (b Bytecode) ReadOperation(ctx *FunctionContext) (op Operator) {
	// TODO: fix bounds checks
	if op = Operator(binary.NativeEndian.Uint64(b[ctx.pc<<3:])); op > _opOneOperand {
		ctx.pc++
		ctx.global.r1 = binary.NativeEndian.Uint64(b[ctx.pc<<3:])
	}

	return
}

func (b Bytecode) String() string {
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

func Reduce[T any, F ~func(prev T, operator Operator, operands ...int) T](bytecode Bytecode, f F, start T) T {
	ip := 0
	registers := make([]int, 0, 1)

	for ip < len(bytecode)>>3 {
		op := Operator(binary.NativeEndian.Uint64(bytecode[ip<<3:]))

		if op > _opOneOperand {
			ip++
			registers = append(registers, int(binary.NativeEndian.Uint64(bytecode[ip<<3:])))
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
	OpAssignRef                        // ASSIGN_REF
	OpArrayNew                         // ARRAY_NEW
	OpArrayAccessRead                  // ARRAY_ACCESS_READ
	OpArrayAccessWrite                 // ARRAY_ACCESS_WRITE
	OpArrayAccessPush                  // ARRAY_ACCESS_PUSH
	OpArrayUnset                       // ARRAY_UNSET
	OpConcat                           // CONCAT
	OpUnset                            // UNSET
	OpForEachInit                      // FE_INIT
	OpForEachNext                      // FE_NEXT
	OpForEachValid                     // FE_VALID
	OpThrow                            // THROW
	OpCallByName                       // CALL_BY_NAME

	_opOneOperand      Operator = iota - 1
	OpAssertType                // ASSERT_TYPE
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

func intSign[T ~int](x T) T { return (x >> 63) | T(uint(-x)>>63) }

//go:noinline
func arrayCompare(ctx Context, x, y *Array) Int {
	xCount := x.Count(ctx)
	yCount := y.Count(ctx)

	if sign := intSign(xCount - yCount); sign != 0 {
		return sign
	}

	for key, val := range x.hash {
		if v, ok := y.access(key); ok {
			return +1
		} else if c := compare(ctx, val, *v.Deref()); c != 0 {
			return c
		}
	}

	return 0
}

func compare(ctx Context, x, y Value) Int {
	if x.Type() == ArrayType && y.Type() != ArrayType {
		return +1
	} else if x.Type() != ArrayType && y.Type() == ArrayType {
		return -1
	}

	switch Juggle(x.Type(), y.Type()) {
	case IntType:
		return intSign(x.AsInt(ctx) - y.AsInt(ctx))
	case FloatType:
		return intSign(Int(x.AsFloat(ctx) - y.AsFloat(ctx)))
	case StringType:
		return Int(strings.Compare(string(x.AsString(ctx)), string(y.AsString(ctx))))
	case NullType:
		// Is possible if only both are null, so always equal
	case BoolType:
		return x.AsBool(ctx).AsInt(ctx) - y.AsBool(ctx).AsInt(ctx)
	case ArrayType:
		return arrayCompare(ctx, x.AsArray(ctx), y.AsArray(ctx))
	}

	return 0
}

// Identical => $x === $y
func Identical(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(left == right)
}

// NotIdentical => $x !== $y
func NotIdentical(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(left != right)
}

// Not => !$x
func Not(ctx *FunctionContext) {
	*ctx.global.sp = !(*ctx.global.sp).AsBool(ctx)
}

// Equal => $x == $y
func Equal(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = equal(ctx, left, right)
}

// NotEqual => $x != $y
func NotEqual(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = !equal(ctx, left, right)
}

func equal(ctx *FunctionContext, x, y Value) Bool {
	as := Juggle(x.Type(), y.Type())

	if as == ArrayType {
		return Bool(maps.EqualFunc(x.AsArray(ctx).hash, y.AsArray(ctx).hash, func(x, y Ref) bool {
			return bool(equal(ctx, x, y))
		}))
	}

	return x.Cast(ctx, as) == y.Cast(ctx, as)
}

// LessOrEqual => $x <= $y
func LessOrEqual(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) < 1)
}

// GreaterOrEqual => $x >= $y
func GreaterOrEqual(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) > -1)
}

// Less => $x < $y
func Less(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) < 0)
}

// Greater => $x > $y
func Greater(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) > 0)
}

// Compare => $x <=> $y
func Compare(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = compare(ctx, left, right)
}

// Const => 0
func Const(ctx *FunctionContext) {
	ctx.global.Push(ctx.global.Constants[ctx.global.r1])
}

// Load => $a
func Load(ctx *FunctionContext) {
	ctx.global.Push(ctx.vars[ctx.global.r1])
}

// LoadRef => &$a
func LoadRef(ctx *FunctionContext) {
	ctx.global.Push(NewRef(&ctx.vars[ctx.global.r1]))
}

// Assign => $a = 0
func Assign(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, *ctx.global.sp)
}

func AssignRef(ctx *FunctionContext) {
	value := ctx.global.Pop()
	ref := *ctx.global.sp
	*ref.(Ref).Deref() = value
}

// AssignAdd => $a += 1
func AssignAdd(ctx *FunctionContext) {
	right := *ctx.global.sp
	v := &ctx.vars[ctx.global.r1]

	switch Juggle((*v).Type(), right.Type()) {
	case ArrayType:
		assignTryRef(v, addArray((*v).AsArray(ctx), right.AsArray(ctx)))
	case FloatType:
		assignTryRef(v, (*v).AsFloat(ctx)+right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)+right.AsInt(ctx))
	}

	*ctx.global.sp = *v
}

// AssignSub => $a -= 1
func AssignSub(ctx *FunctionContext) {
	right := *ctx.global.sp
	v := &ctx.vars[ctx.global.r1]

	switch FloatType {
	case (*v).Type(), right.Type():
		assignTryRef(v, (*v).AsFloat(ctx)-right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)-right.AsInt(ctx))
	}
	*ctx.global.sp = *v
}

// AssignMul => $a *= 1
func AssignMul(ctx *FunctionContext) {
	right := *ctx.global.sp
	v := &ctx.vars[ctx.global.r1]

	switch FloatType {
	case (*v).Type(), right.Type():
		assignTryRef(v, ctx.vars[ctx.global.r1].AsFloat(ctx)*right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)*right.AsInt(ctx))
	}
	*ctx.global.sp = *v
}

// AssignDiv => $a /= 1
func AssignDiv(ctx *FunctionContext) {
	right := *ctx.global.sp
	v := &ctx.vars[ctx.global.r1]

	if res := (*v).AsFloat(ctx) / right.AsFloat(ctx); res == Float(int(res)) {
		assignTryRef(v, res.AsInt(ctx))
	} else {
		assignTryRef(v, res)
	}
	*ctx.global.sp = *v
}

// AssignPow => $a **= 1
func AssignPow(ctx *FunctionContext) {
	right := *ctx.global.sp
	v := &ctx.vars[ctx.global.r1]
	as := Juggle((*v).Type(), right.Type())

	var res Value

	switch as {
	case BoolType:
		res = (!right.AsBool(ctx) || (*v).AsBool(ctx)).AsInt(ctx)
	default:
		res = Float(math.Pow(float64((*v).AsFloat(ctx)), float64(right.AsFloat(ctx)))).Cast(ctx, as)
	}

	assignTryRef(v, res)
	*ctx.global.sp = *v
}

// AssignBwAnd => $a &= 1
func AssignBwAnd(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)&right)
	*ctx.global.sp = *v
}

// AssignBwOr => $a |= 1
func AssignBwOr(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)|right)
	*ctx.global.sp = *v
}

// AssignBwXor => $a ^= 1
func AssignBwXor(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)^right)
	*ctx.global.sp = *v
}

// AssignConcat => $a .= 1
func AssignConcat(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsString(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsString(ctx)+right)
	*ctx.global.sp = *v
}

// AssignShiftLeft => $a <<= 1
func AssignShiftLeft(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)<<right)
	*ctx.global.sp = *v
}

// AssignShiftRight => $a >>= 1
func AssignShiftRight(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)>>right)
	*ctx.global.sp = *v
}

// AssignMod => $a %= 1
func AssignMod(ctx *FunctionContext) {
	right := (*ctx.global.sp).AsFloat(ctx)
	v := &ctx.vars[ctx.global.r1]
	left := (*v).AsFloat(ctx)

	if res := Float(math.Mod(float64(left), float64(right))); res == Float(int(res)) {
		assignTryRef(v, res.AsInt(ctx))
	} else {
		assignTryRef(v, res)
	}
	*ctx.global.sp = *v
}

// Jump unconditional jump; goto
func Jump(ctx *FunctionContext) {
	ctx.pc = int(ctx.global.r1) - 1
}

// JumpTrue if (condition)
func JumpTrue(ctx *FunctionContext) {
	if ctx.global.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// JumpFalse for (...; condition; ...) {}
func JumpFalse(ctx *FunctionContext) {
	if !ctx.global.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// Call => $b = someFunction($a, $x)
func Call(ctx *FunctionContext) {
	ctx.global.Functions[ctx.global.r1].Invoke(ctx)
}

func Pop(ctx *FunctionContext) {
	ctx.global.MovePointer(-1)
}

func Pop2(ctx *FunctionContext) {
	ctx.global.MovePointer(-2)
}

// ReturnValue => return 0;
func ReturnValue(ctx *FunctionContext) {
	v := *ctx.global.sp
	Return(ctx)
	ctx.global.Push(v)
}

// Return => return;
func Return(ctx *FunctionContext) {
	ctx.global.Sp(ctx.global.PopFrame().fp)
}

// Add => 1 + 2
func Add(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp

	switch FloatType {
	case left.Type(), right.Type():
		*ctx.global.sp = left.AsFloat(ctx) + right.AsFloat(ctx)
		return
	}

	switch ArrayType {
	case left.Type(), right.Type():
		*ctx.global.sp = addArray(left.AsArray(ctx), right.AsArray(ctx))
	default:
		*ctx.global.sp = left.AsInt(ctx) + right.AsInt(ctx)
	}
}

//go:noinline
func addArray(left, right *Array) *Array {
	result := maps.Clone(right.hash)
	maps.Copy(result, left.hash)
	return &Array{hash: result, next: max(left.next, right.next)}
}

// Sub => 1 - 2
func Sub(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp

	switch FloatType {
	case left.Type(), right.Type():
		*ctx.global.sp = left.AsFloat(ctx) - right.AsFloat(ctx)
	default:
		*ctx.global.sp = left.AsInt(ctx) - right.AsInt(ctx)
	}
}

// Mul => 1 * 2
func Mul(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp

	switch FloatType {
	case left.Type(), right.Type():
		*ctx.global.sp = left.AsFloat(ctx) * right.AsFloat(ctx)
	default:
		*ctx.global.sp = left.AsInt(ctx) * right.AsInt(ctx)
	}
}

// Div => 1 / 2
func Div(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	res := left.AsFloat(ctx) / right.AsFloat(ctx)

	switch FloatType {
	case left.Type(), right.Type():
	default:
		if res == Float(int(res)) {
			*ctx.global.sp = res.AsInt(ctx)
			return
		}
	}

	*ctx.global.sp = res
}

// Mod => 1 % 2
func Mod(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	as := Juggle(left.Type(), right.Type())

	switch as {
	case BoolType:
		if !right.AsBool(ctx) {
			panic("modulo by zero error")
		}
		*ctx.global.sp = Int(0)
	default:
		*ctx.global.sp = Float(math.Mod(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx)))).Cast(ctx, as)
	}
}

// Pow => 1 ** 2
func Pow(ctx *FunctionContext) {
	right := ctx.global.Pop()
	left := *ctx.global.sp
	as := Juggle(left.Type(), right.Type())

	switch as {
	case BoolType:
		*ctx.global.sp = (!right.AsBool(ctx) || left.AsBool(ctx)).AsInt(ctx)
	default:
		*ctx.global.sp = Float(math.Pow(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx)))).Cast(ctx, as)
	}
}

// BwAnd => 1 & 2
func BwAnd(ctx *FunctionContext) {
	right := ctx.global.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left & right
}

// BwOr => 1 | 2
func BwOr(ctx *FunctionContext) {
	right := ctx.global.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left | right
}

// BwXor => 1 ^ 2
func BwXor(ctx *FunctionContext) {
	right := ctx.global.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left ^ right
}

// BwNot => ~1
func BwNot(ctx *FunctionContext) {
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = ^left
}

// ShiftLeft => 1 << 2
func ShiftLeft(ctx *FunctionContext) {
	right := ctx.global.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left << right
}

// ShiftRight => 1 >> 2
func ShiftRight(ctx *FunctionContext) {
	right := ctx.global.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left >> right
}

// Cast => (_type_)$x
func Cast(ctx *FunctionContext) {
	*ctx.global.sp = (*ctx.global.sp).Cast(ctx, Type(ctx.global.r1))
}

// PreIncrement => ++$x
func PreIncrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	switch (*v).Type() {
	case FloatType:
		*v = (*v).AsFloat(ctx) + 1
	default:
		*v = (*v).AsInt(ctx) + 1
	}

	ctx.global.Push(*v)
}

// PreDecrement => --$x
func PreDecrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	switch (*v).Type() {
	case FloatType:
		*v = (*v).AsFloat(ctx) - 1
	default:
		*v = (*v).AsInt(ctx) - 1
	}

	ctx.global.Push(*v)
}

// PostIncrement => $x++
func PostIncrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	ctx.global.Push(*v)

	switch (*v).Type() {
	case FloatType:
		*v = (*v).AsFloat(ctx) + 1
	default:
		*v = (*v).AsInt(ctx) + 1
	}
}

// PostDecrement => $x--
func PostDecrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	if (*v).IsRef() {
		v = (*v).(Ref).Deref()
	}

	ctx.global.Push(*v)

	switch (*v).Type() {
	case FloatType:
		*v = (*v).AsFloat(ctx) - 1
	default:
		*v = (*v).AsInt(ctx) - 1
	}
}

// Concat => $a . "string"
func Concat(ctx *FunctionContext) {
	right := ctx.global.Pop().AsString(ctx)
	left := (*ctx.global.sp).AsString(ctx)
	*ctx.global.sp = left + right
}

// AssertType => fn(int $a)
func AssertType(ctx *FunctionContext) {
	// TODO: check if strict types
	*ctx.global.sp = (*ctx.global.sp).Cast(ctx, Type(ctx.global.r1))
}

// Echo => echo $x, $y;
func Echo(ctx *FunctionContext) {
	count := ctx.global.r1
	values := make([]any, count)

	for i, v := range ctx.Slice(-int(count), 0) {
		// Need to convert every value to native Go string,
		// because fmt doesn't print POSIX control characters included in value of underlying type
		values[i] = string(v.AsString(ctx))
	}

	fmt.Fprint(ctx.Output(), values...)
}

// IsSet => isset($x)
func IsSet(ctx *FunctionContext) {
	v := *ctx.global.sp
	*ctx.global.sp = Bool(v != nil && v != Null{})
}

// ArrayNew => $x = [];
func ArrayNew(ctx *FunctionContext) {
	ctx.global.Push(NewArray(nil))
}

// ArrayAccessRead => $x['test']
func ArrayAccessRead(ctx *FunctionContext) {
	key := ctx.global.Pop()
	arr := ctx.global.Pop().AsArray(ctx)

	if v, ok := arr.access(key); ok {
		ctx.global.Push(*v.Deref())
	} else {
		ctx.global.Push(Null{})
	}
}

// ArrayAccessWrite => $x['test'] = 1
func ArrayAccessWrite(ctx *FunctionContext) {
	key := ctx.global.Pop()

	var arr *Array

	if (*ctx.global.sp).IsRef() {
		arr = ctx.global.Pop().AsArray(ctx)
	} else {
		arr = (*ctx.global.sp).AsArray(ctx)
	}

	ctx.global.Push(arr.assign(ctx, key))
}

// ArrayAccessPush => $x[] = 1
func ArrayAccessPush(ctx *FunctionContext) {
	var arr *Array

	if (*ctx.global.sp).IsRef() {
		arr = ctx.global.Pop().AsArray(ctx)
	} else {
		arr = (*ctx.global.sp).AsArray(ctx)
	}

	ctx.global.Push(arr.assign(ctx, nil))
}

// ArrayUnset => unset($x['test'])
func ArrayUnset(ctx *FunctionContext) {
	key := ctx.global.Pop()
	arr := ctx.global.Pop().AsArray(ctx)
	arr.delete(key)
	ctx.global.Push(arr)
}

// ForEachInit => foreach([1,2] as ...)
func ForEachInit(ctx *FunctionContext) {
	iterable := ctx.global.Pop()
	switch iterable.(type) {
	case Iterator:
	case IteratorAggregate:
		iterable = iterable.(IteratorAggregate).GetIterator(ctx)
	default:
		ctx.Throw(NewThrowable("not iterable", EError))
		return
	}
	iterable.(Iterator).Rewind(ctx)
	ctx.global.Push(iterable)
}

// ForEachKey => foreach(... as $key => ...)
func ForEachKey(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*ctx.global.sp).(Iterator).Key(ctx))
}

// ForEachValue => foreach(... as $value)
func ForEachValue(ctx *FunctionContext) {
	variable := &ctx.vars[ctx.global.r1]
	value := (*ctx.global.sp).(Iterator).Current(ctx)
	if value.IsRef() {
		value = *value.(Ref).Deref()
	}
	assignTryRef(variable, value)
}

// ForEachValueRef => foreach(... as &$value)
func ForEachValueRef(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*ctx.global.sp).(Iterator).Current(ctx))
}

func ForEachNext(ctx *FunctionContext) {
	(*ctx.global.sp).(Iterator).Next(ctx)
}

// ForEachValid checks if there are items in iterator
func ForEachValid(ctx *FunctionContext) {
	ctx.global.Push((*ctx.global.sp).(Iterator).Valid(ctx))
}

// Throw => throw new Exception();
func Throw(ctx *FunctionContext) {}

// CallByName => $func = "func_name"; $func();
func CallByName(ctx *FunctionContext) {
	ctx.global.FunctionByName(ctx.global.Pop().AsString(ctx)).Invoke(ctx)
}
