package vm

import (
	"encoding/binary"
	"fmt"
	"maps"
	"math"
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
	OpUnset                            // UNSET
	OpForEachInit                      // FE_INIT
	OpForEachNext                      // FE_NEXT
	OpForEachValid                     // FE_VALID
	OpThrow                            // THROW
	OpInitCallByName                   // INIT_CALL_BY_NAME

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

func intSign[T ~int](x T) T { return (x >> 63) | T(uint(-x)>>63) }

//go:noinline
func arrayCompare(ctx Context, x, y *Array) Int {
	xCount := x.Count(ctx)
	yCount := y.Count(ctx)

	if sign := intSign(xCount - yCount); sign != 0 {
		return sign
	}

	for key, val := range x.hash.internal {
		if v, ok := y.access(key); ok {
			return +1
		} else if c := compare(ctx, val.v, *v.Deref()); c != 0 {
			return c
		}
	}

	return 0
}

func compare(ctx Context, x, y Value) Int {
	if x.Type().shape == ArrayType && y.Type().shape != ArrayType {
		return +1
	} else if x.Type().shape != ArrayType && y.Type().shape == ArrayType {
		return -1
	}

	switch Juggle(x.Type().shape, y.Type().shape) {
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
	case ObjectType:
		// TODO:
	}

	return 0
}

// Identical => $x === $y
//
//go:nosplit
func Identical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(left == right || left.Type().shape == ArrayType && right.Type().shape == ArrayType && arrayCompare(ctx, left.(*Array), right.(*Array)) == 0))
}

// NotIdentical => $x !== $y
//
//go:nosplit
func NotIdentical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(left != right || left.Type().shape == ArrayType && right.Type().shape == ArrayType && arrayCompare(ctx, left.(*Array), right.(*Array)) != 0))
}

// Not => !$x
//
//go:nosplit
func Not(ctx *FunctionContext) {
	ctx.SetTop(!ctx.Top().AsBool(ctx))
}

// Equal => $x == $y
//
//go:nosplit
func Equal(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(equal(ctx, left, right))
}

// NotEqual => $x != $y
//
//go:nosplit
func NotEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(!equal(ctx, left, right))
}

func equal(ctx *FunctionContext, x, y Value) Bool {
	if x == y {
		return true
	}

	as := Juggle(x.Type().shape, y.Type().shape)

	if as == ArrayType {
		return arrayCompare(ctx, x.AsArray(ctx), y.AsArray(ctx)) == 0
	}

	return x.Cast(ctx, as) == y.Cast(ctx, as)
}

// LessOrEqual => $x <= $y
//
//go:nosplit
func LessOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) < 1))
}

// GreaterOrEqual => $x >= $y
//
//go:nosplit
func GreaterOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) > -1))
}

// Less => $x < $y
//
//go:nosplit
func Less(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) < 0))
}

// Greater => $x > $y
//
//go:nosplit
func Greater(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) > 0))
}

// Compare => $x <=> $y
//
//go:nosplit
func Compare(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(compare(ctx, left, right))
}

//go:nosplit
func Coalesce(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	if left == (Null{}) {
		ctx.SetTop(right)
	} else {
		ctx.SetTop(left)
	}
}

// Const => 0
//
//go:nosplit
func Const(ctx *FunctionContext) {
	ctx.Push(ctx.Constants[ctx.r1])
}

// Load => $a
//
//go:nosplit
func Load(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.r1])
}

// LoadRef => &$a
//
//go:nosplit
func LoadRef(ctx *FunctionContext) {
	ctx.Push(NewRef(&ctx.vars[ctx.r1]))
}

// Assign => $a = 0
//
//go:nosplit
func Assign(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, ctx.stack[ctx.sp])
}

//go:nosplit
func AssignRef(ctx *FunctionContext) {
	value := ctx.Pop()
	ref := ctx.Top()
	*ref.(Ref).Deref() = value
}

// AssignAdd => $a += 1
//
//go:nosplit
func AssignAdd(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.r1]

	switch Juggle((*v).Type().shape, right.Type().shape) {
	case ArrayType:
		assignTryRef(v, addArray((*v).AsArray(ctx), right.AsArray(ctx)))
	case FloatType:
		assignTryRef(v, (*v).AsFloat(ctx)+right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)+right.AsInt(ctx))
	}

	ctx.SetTop(*v)
}

// AssignSub => $a -= 1
//
//go:nosplit
func AssignSub(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.r1]

	switch FloatType {
	case (*v).Type().shape, right.Type().shape:
		assignTryRef(v, (*v).AsFloat(ctx)-right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)-right.AsInt(ctx))
	}
	ctx.SetTop(*v)
}

// AssignMul => $a *= 1
//
//go:nosplit
func AssignMul(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.r1]

	switch FloatType {
	case (*v).Type().shape, right.Type().shape:
		assignTryRef(v, ctx.vars[ctx.r1].AsFloat(ctx)*right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)*right.AsInt(ctx))
	}
	ctx.SetTop(*v)
}

// AssignDiv => $a /= 1
//
//go:nosplit
func AssignDiv(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.r1]

	if res := (*v).AsFloat(ctx) / right.AsFloat(ctx); res == Float(int(res)) {
		assignTryRef(v, res.AsInt(ctx))
	} else {
		assignTryRef(v, res)
	}
	ctx.SetTop(*v)
}

// AssignPow => $a **= 1
//
//go:nosplit
func AssignPow(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.r1]
	as := Juggle((*v).Type().shape, right.Type().shape)

	var res Value

	switch as {
	case BoolType:
		res = (!right.AsBool(ctx) || (*v).AsBool(ctx)).AsInt(ctx)
	default:
		res = Float(math.Pow(float64((*v).AsFloat(ctx)), float64(right.AsFloat(ctx)))).Cast(ctx, as)
	}

	assignTryRef(v, res)
	ctx.SetTop(*v)
}

// AssignBwAnd => $a &= 1
//
//go:nosplit
func AssignBwAnd(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsInt(ctx)&right)
	ctx.SetTop(*v)
}

// AssignBwOr => $a |= 1
//
//go:nosplit
func AssignBwOr(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsInt(ctx)|right)
	ctx.SetTop(*v)
}

// AssignBwXor => $a ^= 1
//
//go:nosplit
func AssignBwXor(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsInt(ctx)^right)
	ctx.SetTop(*v)
}

// AssignConcat => $a .= 1
//
//go:nosplit
func AssignConcat(ctx *FunctionContext) {
	right := ctx.Top().AsString(ctx)
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsString(ctx)+right)
	ctx.SetTop(*v)
}

// AssignShiftLeft => $a <<= 1
//
//go:nosplit
func AssignShiftLeft(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsInt(ctx)<<right)
	ctx.SetTop(*v)
}

// AssignShiftRight => $a >>= 1
//
//go:nosplit
func AssignShiftRight(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, (*v).AsInt(ctx)>>right)
	ctx.SetTop(*v)
}

// AssignMod => $a %= 1
//
//go:nosplit
func AssignMod(ctx *FunctionContext) {
	right := ctx.Top().AsFloat(ctx)
	v := &ctx.vars[ctx.r1]
	left := (*v).AsFloat(ctx)

	if res := Float(math.Mod(float64(left), float64(right))); res == Float(int(res)) {
		assignTryRef(v, res.AsInt(ctx))
	} else {
		assignTryRef(v, res)
	}
	ctx.SetTop(*v)
}

// AssignCoalesce => $a ??= 1
//
//go:nosplit
func AssignCoalesce(ctx *FunctionContext) {
	right := ctx.Top()
	left := &ctx.vars[ctx.r1]
	if *left == (Null{}) {
		*left = right
	}
	ctx.SetTop(*left)
}

// Jump unconditional jump; goto
//
//go:nosplit
func Jump(ctx *FunctionContext) {
	ctx.pc = int(ctx.r1) - 1
}

// JumpTrue if (condition)
//
//go:nosplit
func JumpTrue(ctx *FunctionContext) {
	if ctx.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// JumpFalse for (...; condition; ...) {}
//
//go:nosplit
func JumpFalse(ctx *FunctionContext) {
	if !ctx.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

//go:nosplit
func Pop(ctx *FunctionContext) {
	ctx.sp -= 1
}

//go:nosplit
func Pop2(ctx *FunctionContext) {
	ctx.sp -= 2
}

// ReturnValue => return 0;
//
//go:nosplit
func ReturnValue(ctx *FunctionContext) {
	v := ctx.Top()
	Return(ctx)
	ctx.SetTop(v)
}

// Return => return;
//
//go:nosplit
func Return(ctx *FunctionContext) {
	ctx.Sp(ctx.PopFrame().fp)
}

// Add => 1 + 2
//
//go:nosplit
func Add(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	switch FloatType {
	case left.Type().shape, right.Type().shape:
		ctx.SetTop(left.AsFloat(ctx) + right.AsFloat(ctx))
		return
	}

	switch ArrayType {
	case left.Type().shape, right.Type().shape:
		ctx.SetTop(addArray(left.AsArray(ctx), right.AsArray(ctx)))
	default:
		ctx.SetTop(left.AsInt(ctx) + right.AsInt(ctx))
	}
}

func addArray(left, right *Array) *Array {
	result := maps.Clone(right.hash.internal)
	maps.Copy(result, left.hash.internal)
	return &Array{hash: HashTable[Value, Value]{result}, next: max(left.next, right.next)}
}

// Sub => 1 - 2
//
//go:nosplit
func Sub(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	switch FloatType {
	case left.Type().shape, right.Type().shape:
		ctx.SetTop(left.AsFloat(ctx) - right.AsFloat(ctx))
	default:
		ctx.SetTop(left.AsInt(ctx) - right.AsInt(ctx))
	}
}

// Mul => 1 * 2
//
//go:nosplit
func Mul(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	switch FloatType {
	case left.Type().shape, right.Type().shape:
		ctx.SetTop(left.AsFloat(ctx) * right.AsFloat(ctx))
	default:
		ctx.SetTop(left.AsInt(ctx) * right.AsInt(ctx))
	}
}

// Div => 1 / 2
//
//go:nosplit
func Div(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	res := left.AsFloat(ctx) / right.AsFloat(ctx)

	switch FloatType {
	case left.Type().shape, right.Type().shape:
	default:
		if res == Float(int(res)) {
			ctx.SetTop(res.AsInt(ctx))
			return
		}
	}

	ctx.SetTop(res)
}

// Mod => 1 % 2
//
//go:nosplit
func Mod(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	as := Juggle(left.Type().shape, right.Type().shape)

	switch as {
	case BoolType:
		if !right.AsBool(ctx) {
			panic("modulo by zero error")
		}
		ctx.SetTop(Int(0))
	default:
		ctx.SetTop(Float(math.Mod(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx)))).Cast(ctx, as))
	}
}

// Pow => 1 ** 2
//
//go:nosplit
func Pow(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	as := Juggle(left.Type().shape, right.Type().shape)

	switch as {
	case BoolType:
		ctx.SetTop((!right.AsBool(ctx) || left.AsBool(ctx)).AsInt(ctx))
	default:
		ctx.SetTop(Float(math.Pow(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx)))).Cast(ctx, as))
	}
}

// BwAnd => 1 & 2
//
//go:nosplit
func BwAnd(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(left & right)
}

// BwOr => 1 | 2
//
//go:nosplit
func BwOr(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(left | right)
}

// BwXor => 1 ^ 2
//
//go:nosplit
func BwXor(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(left ^ right)
}

// BwNot => ~1
//
//go:nosplit
func BwNot(ctx *FunctionContext) {
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(^left)
}

// ShiftLeft => 1 << 2
//
//go:nosplit
func ShiftLeft(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(left << right)
}

// ShiftRight => 1 >> 2
//
//go:nosplit
func ShiftRight(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(left >> right)
}

// Cast => (_type_)$x
//
//go:nosplit
func Cast(ctx *FunctionContext) {
	ctx.SetTop(ctx.Top().Cast(ctx, TypeShape(ctx.r1)))
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

	switch (*v).Type().shape {
	case FloatType:
		*v = (*v).AsFloat(ctx) - 1
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

	switch (*v).Type().shape {
	case FloatType:
		*v = (*v).AsFloat(ctx) + 1
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

	switch (*v).Type().shape {
	case FloatType:
		*v = (*v).AsFloat(ctx) - 1
	default:
		*v = (*v).AsInt(ctx) - 1
	}
}

// Concat => $a . "string"
//
//go:nosplit
func Concat(ctx *FunctionContext) {
	right := ctx.Pop().AsString(ctx)
	left := ctx.Top().AsString(ctx)
	ctx.SetTop(left + right)
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

	fmt.Fprint(ctx.Output(), values...)
}

// IsSet => isset($x)
//
//go:nosplit
func IsSet(ctx *FunctionContext) {
	v := ctx.Top()
	ctx.SetTop(Bool(v != nil && v != Null{}))
}

// ArrayNew => $x = [];
//
//go:nosplit
func ArrayNew(ctx *FunctionContext) {
	ctx.Push(NewArray(nil))
}

// ArrayAccessRead => $x['test']
//
//go:nosplit
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
//
//go:nosplit
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
//
//go:nosplit
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
//
//go:nosplit
func ArrayUnset(ctx *FunctionContext) {
	key := ctx.Pop()
	arr := ctx.Pop().AsArray(ctx)
	arr.delete(key)
	ctx.Push(arr)
}

// ForEachInit => foreach([1,2] as ...)
//
//go:nosplit
func ForEachInit(ctx *FunctionContext) {
	iterable := ctx.Top()
	switch iterable.(type) {
	case Iterator:
	case IteratorAggregate:
		iterable = iterable.(IteratorAggregate).GetIterator(ctx)
	default:
		ctx.Throw(NewThrowable("not iterable", EError))
		return
	}
	iterable.(Iterator).Rewind(ctx)
	ctx.SetTop(iterable)
}

// ForEachKey => foreach(... as $key => ...)
//
//go:nosplit
func ForEachKey(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, ctx.Top().(Iterator).Key(ctx))
}

// ForEachValue => foreach(... as $value)
//
//go:nosplit
func ForEachValue(ctx *FunctionContext) {
	variable := &ctx.vars[ctx.r1]
	value := ctx.Top().(Iterator).Current(ctx)
	if value.IsRef() {
		value = *value.(Ref).Deref()
	}
	assignTryRef(variable, value)
}

// ForEachValueRef => foreach(... as &$value)
//
//go:nosplit
func ForEachValueRef(ctx *FunctionContext) {
	v := &ctx.vars[ctx.r1]
	assignTryRef(v, ctx.Top().(Iterator).Current(ctx))
}

//go:nosplit
func ForEachNext(ctx *FunctionContext) {
	ctx.Top().(Iterator).Next(ctx)
}

// ForEachValid checks if there are items in iterator
//
//go:nosplit
func ForEachValid(ctx *FunctionContext) {
	ctx.Push(ctx.Top().(Iterator).Valid(ctx))
}

// Throw => throw new Exception();
//
//go:nosplit
func Throw(ctx *FunctionContext) {}

// InitCall => someFunction(...Args...);
//
//go:nosplit
func InitCall(ctx *FunctionContext) {
	ctx.Push(ctx.Functions[ctx.r1])
}

// InitCallByName => $someFunctionName(...Args...);
//
//go:nosplit
func InitCallByName(ctx *FunctionContext) {
	name := ctx.Pop().AsString(ctx)
	ctx.Push(ctx.FunctionByName(name))
}

//go:nosplit
func Call(ctx *FunctionContext) {
	ctx.sp -= int(ctx.r1)
	ctx.Top().(Callable).Invoke(ctx, nil, nil)
}
