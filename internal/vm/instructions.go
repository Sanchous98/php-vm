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
	op = Operator(binary.NativeEndian.Uint64(b[ctx.pc<<3:]))

	if op > _opOneOperand {
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
	OpForEachReset                     // FE_RESET

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

//go:noinline
func arrayCompare(ctx Context, x, y *Array) Int {
	if x.Count(ctx) < y.Count(ctx) {
		return -1
	} else if x.Count(ctx) > y.Count(ctx) {
		return +1
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
		if x.AsInt(ctx) < y.AsInt(ctx) {
			return -1
		} else if x.AsInt(ctx) > y.AsInt(ctx) {
			return +1
		}
	case FloatType:
		if x.AsFloat(ctx) < y.AsFloat(ctx) {
			return -1
		} else if x.AsFloat(ctx) > y.AsFloat(ctx) {
			return +1
		}
	case StringType:
		if x.AsString(ctx) < y.AsString(ctx) {
			return -1
		} else if x.AsString(ctx) > y.AsString(ctx) {
			return +1
		}
	case NullType:
		// Is possible if only both are null, so always equal
	case BoolType:
		if x.AsBool(ctx) && !y.AsBool(ctx) {
			return +1
		} else if !x.AsBool(ctx) && y.AsBool(ctx) {
			return -1
		}
	case ArrayType:
		return arrayCompare(ctx, x.AsArray(ctx), y.AsArray(ctx))
	}

	return 0
}

// Identical => $x === $y
func Identical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(left == right)
}

// NotIdentical => $x !== $y
func NotIdentical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(left != right)
}

// Not => !$x
func Not(ctx *FunctionContext) {
	*ctx.global.sp = !(*ctx.global.sp).AsBool(ctx)
}

// Equal => $x == $y
func Equal(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = equal(ctx, left, right)
}

// NotEqual => $x != $y
func NotEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = !equal(ctx, left, right)
}

func equal(ctx *FunctionContext, x, y Value) Bool {
	as := Juggle(x.Type(), y.Type())

	if as != ArrayType {
		return x.Cast(ctx, as) == y.Cast(ctx, as)
	}

	return arrayEqual(ctx, x.AsArray(ctx), y.AsArray(ctx))
}

//go:noinline
func arrayEqual(ctx *FunctionContext, x, y *Array) Bool {
	return Bool(maps.EqualFunc(x.AsArray(ctx).hash, y.AsArray(ctx).hash, func(x, y Ref) bool {
		return bool(equal(ctx, x, y))
	}))
}

// LessOrEqual => $x <= $y
func LessOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) < 1)
}

// GreaterOrEqual => $x >= $y
func GreaterOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) > -1)
}

// Less => $x < $y
func Less(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) < 0)
}

// Greater => $x > $y
func Greater(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = Bool(compare(ctx, left, right) > 0)
}

// Compare => $x <=> $y
func Compare(ctx *FunctionContext) {
	right := ctx.Pop()
	left := *ctx.global.sp
	*ctx.global.sp = compare(ctx, left, right)
}

// Const => 0
func Const(ctx *FunctionContext) {
	ctx.Push(ctx.global.Constants[ctx.global.r1])
}

// Load => $a
func Load(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.global.r1])
}

// LoadRef => &$a
func LoadRef(ctx *FunctionContext) {
	ctx.Push(NewRef(&ctx.vars[ctx.global.r1]))
}

// Assign => $a = 0
func Assign(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, ctx.Top())
}

func AssignRef(ctx *FunctionContext) {
	value := ctx.Pop()
	ref := *ctx.global.sp
	*ref.(Ref).Deref() = value
}

// AssignAdd => $a += 1
func AssignAdd(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.global.r1]

	switch Juggle((*v).Type(), right.Type()) {
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
func AssignSub(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.global.r1]

	switch FloatType {
	case (*v).Type(), right.Type():
		assignTryRef(v, (*v).AsFloat(ctx)-right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)-right.AsInt(ctx))
	}
	ctx.SetTop(*v)
}

// AssignMul => $a *= 1
func AssignMul(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.global.r1]

	switch FloatType {
	case (*v).Type(), right.Type():
		assignTryRef(&ctx.vars[ctx.global.r1], ctx.vars[ctx.global.r1].AsFloat(ctx)*right.AsFloat(ctx))
	default:
		assignTryRef(v, (*v).AsInt(ctx)*right.AsInt(ctx))
	}
	ctx.SetTop(*v)
}

// AssignDiv => $a /= 1
func AssignDiv(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.global.r1]

	if res := (*v).AsFloat(ctx) / right.AsFloat(ctx); res == Float(int(res)) {
		assignTryRef(v, res.AsInt(ctx))
	} else {
		assignTryRef(v, res)
	}
	ctx.SetTop(*v)
}

// AssignPow => $a **= 1
func AssignPow(ctx *FunctionContext) {
	right := ctx.Top()
	v := &ctx.vars[ctx.global.r1]
	as := Juggle((*v).Type(), right.Type())

	if as == BoolType {
		assignTryRef(v, (!right.AsBool(ctx) || (*v).AsBool(ctx)).AsInt(ctx))
	} else {
		res := Float(math.Pow(float64((*v).AsFloat(ctx)), float64(right.AsFloat(ctx))))
		assignTryRef(v, res.Cast(ctx, as))
	}
	ctx.SetTop(*v)
}

// AssignBwAnd => $a &= 1
func AssignBwAnd(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)&right)
	ctx.SetTop(*v)
}

// AssignBwOr => $a |= 1
func AssignBwOr(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)|right)
	ctx.SetTop(*v)
}

// AssignBwXor => $a ^= 1
func AssignBwXor(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)^right)
	ctx.SetTop(*v)
}

// AssignConcat => $a .= 1
func AssignConcat(ctx *FunctionContext) {
	right := ctx.Top().AsString(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsString(ctx)+right)
	ctx.SetTop(*v)
}

// AssignShiftLeft => $a <<= 1
func AssignShiftLeft(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)<<right)
	ctx.SetTop(*v)
}

// AssignShiftRight => $a >>= 1
func AssignShiftRight(ctx *FunctionContext) {
	right := ctx.Top().AsInt(ctx)
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, (*v).AsInt(ctx)>>right)
	ctx.SetTop(*v)
}

// AssignMod => $a %= 1
func AssignMod(ctx *FunctionContext) {
	right := ctx.Top().AsFloat(ctx)
	v := &ctx.vars[ctx.global.r1]
	left := (*v).AsFloat(ctx)

	if res := Float(math.Mod(float64(left), float64(right))); res == Float(int(res)) {
		assignTryRef(v, res.AsInt(ctx))
	} else {
		assignTryRef(v, res)
	}
	ctx.SetTop(*v)
}

// Jump unconditional jump
func Jump(ctx *FunctionContext) {
	ctx.pc = int(ctx.global.r1) - 1
}

// JumpTrue if (true_statement)
func JumpTrue(ctx *FunctionContext) {
	if ctx.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// JumpFalse for ($i = 0; $i < 1; $i++) {}
func JumpFalse(ctx *FunctionContext) {
	if !ctx.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// Call => $b = someFunction($a, $x)
func Call(ctx *FunctionContext) {
	ctx.Push(ctx.global.Functions[ctx.global.r1].Invoke(ctx))
}

func Pop(ctx *FunctionContext) {
	ctx.MovePointer(-1)
}

func Pop2(ctx *FunctionContext) {
	ctx.MovePointer(-2)
}

// ReturnValue => return 0;
func ReturnValue(ctx *FunctionContext) {
	v := ctx.Top()
	Return(ctx)
	ctx.Push(v)
}

// Return => return;
func Return(ctx *FunctionContext) {
	ctx.Sp(ctx.fp)
}

// Add => 1 + 2
func Add(ctx *FunctionContext) {
	right := ctx.Pop()
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
	right := ctx.Pop()
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
	right := ctx.Pop()
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
	right := ctx.Pop()
	left := ctx.Pop()
	res := left.AsFloat(ctx) / right.AsFloat(ctx)

	switch FloatType {
	case left.Type(), right.Type():
	default:
		if res == Float(int(res)) {
			ctx.Push(res.AsInt(ctx))
		}
	}

	ctx.Push(res)
}

// Mod => 1 % 2
func Mod(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	as := Juggle(left.Type(), right.Type())

	switch as {
	case BoolType:
		if !right.AsBool(ctx) {
			panic("modulo by zero error")
		}
		ctx.SetTop(Int(0))
	default:
		res := Float(math.Mod(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx))))
		ctx.SetTop(res.Cast(ctx, as))
	}
}

// Pow => 1 ** 2
func Pow(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	as := Juggle(left.Type(), right.Type())

	if as == BoolType {
		ctx.SetTop((!right.AsBool(ctx) || left.AsBool(ctx)).AsInt(ctx))
	} else {
		res := Float(math.Pow(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx))))
		ctx.SetTop(res.Cast(ctx, as))
	}
}

// BwAnd => 1 & 2
func BwAnd(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left & right
}

// BwOr => 1 | 2
func BwOr(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left | right
}

// BwXor => 1 ^ 2
func BwXor(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
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
	right := ctx.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left << right
}

// ShiftRight => 1 >> 2
func ShiftRight(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := (*ctx.global.sp).AsInt(ctx)
	*ctx.global.sp = left >> right
}

// Cast => (type)$x
func Cast(ctx *FunctionContext) {
	*ctx.global.sp = (*ctx.global.sp).Cast(ctx, Type(ctx.global.r1))
}

// PreIncrement => ++$x
func PreIncrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	switch (*v).Type() {
	case FloatType:
		assignTryRef(v, (*v).AsFloat(ctx)+1)
	default:
		assignTryRef(v, (*v).AsInt(ctx)+1)
	}

	if (*v).IsRef() {
		ctx.Push(*(*v).(Ref).Deref())
	} else {
		ctx.Push(*v)
	}
}

// PreDecrement => --$x
func PreDecrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	switch (*v).Type() {
	case FloatType:
		assignTryRef(v, (*v).AsFloat(ctx)-1)
	default:
		assignTryRef(v, (*v).AsInt(ctx)-1)
	}

	if (*v).IsRef() {
		ctx.Push(*(*v).(Ref).Deref())
	} else {
		ctx.Push(*v)
	}
}

// PostIncrement => $x++
func PostIncrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	if (*v).IsRef() {
		ctx.Push(*(*v).(Ref).Deref())
	} else {
		ctx.Push(*v)
	}

	switch (*v).Type() {
	case FloatType:
		assignTryRef(v, (*v).AsFloat(ctx)+1)
	default:
		assignTryRef(v, (*v).AsInt(ctx)+1)
	}
}

// PostDecrement => $x--
func PostDecrement(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]

	if (*v).IsRef() {
		ctx.Push(*(*v).(Ref).Deref())
	} else {
		ctx.Push(*v)
	}

	switch (*v).Type() {
	case FloatType:
		assignTryRef(v, (*v).AsFloat(ctx)-1)
	default:
		assignTryRef(v, (*v).AsInt(ctx)-1)
	}
}

// Concat => $a . "string"
func Concat(ctx *FunctionContext) {
	right := ctx.Pop().AsString(ctx)
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

	fmt.Fprint(ctx.global.out, values...)
}

// IsSet => isset($x)
func IsSet(ctx *FunctionContext) {
	v := *ctx.global.sp
	*ctx.global.sp = Bool(v != nil && v != Null{})
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

	if (*ctx.global.sp).IsRef() {
		arr = ctx.Pop().AsArray(ctx)
	} else {
		arr = ctx.Top().AsArray(ctx)
	}

	ctx.Push(arr.assign(ctx, key))
}

// ArrayAccessPush => $x[] = 1
func ArrayAccessPush(ctx *FunctionContext) {
	var arr *Array

	if (*ctx.global.sp).IsRef() {
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

func ForEachInit(ctx *FunctionContext) {
	iterable := ctx.Pop()
	switch iterable.(type) {
	case Iterator:
	case IteratorAggregate:
		iterable = iterable.(IteratorAggregate).GetIterator(ctx)
	}
	iterable.(Iterator).Rewind(ctx)
	ctx.Push(iterable)
}

func ForEachKey(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, ctx.Top().(Iterator).Key(ctx))
}

func ForEachValue(ctx *FunctionContext) {
	variable := &ctx.vars[ctx.global.r1]
	value := ctx.Top().(Iterator).Current(ctx)
	if value.IsRef() {
		value = *value.(Ref).Deref()
	}
	assignTryRef(variable, value)
}

func ForEachValueRef(ctx *FunctionContext) {
	v := &ctx.vars[ctx.global.r1]
	assignTryRef(v, ctx.Top().(Iterator).Current(ctx))
}

func ForEachNext(ctx *FunctionContext) {
	ctx.Top().(Iterator).Next(ctx)
}

func ForEachValid(ctx *FunctionContext) {
	ctx.Push(ctx.Top().(Iterator).Valid(ctx))
}
