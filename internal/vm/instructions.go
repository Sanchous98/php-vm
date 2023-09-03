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
		ctx.WriteRX(int(binary.NativeEndian.Uint64(b[ctx.pc<<3:])))
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
	OpNoop           Operator = iota // NOOP
	OpPop                            // POP
	OpReturn                         // RETURN
	OpReturnValue                    // RETURN_VAL
	OpAdd                            // ADD
	OpAddInt                         // ADD_INT
	OpAddFloat                       // ADD_FLOAT
	OpAddArray                       // ADD_ARRAY
	OpAddBool                        // ADD_BOOL
	OpSub                            // SUB
	OpSubInt                         // SUB_INT
	OpSubFloat                       // SUB_FLOAT
	OpSubBool                        // SUB_BOOL
	OpMul                            // MUL
	OpMulInt                         // MUL_INT
	OpMulFloat                       // MUL_FLOAT
	OpMulBool                        // MUL_BOOL
	OpDiv                            // DIV
	OpDivInt                         // DIV_INT
	OpDivFloat                       // DIV_FLOAT
	OpDivBool                        // DIV_BOOL
	OpMod                            // MOD
	OpModInt                         // MOD_INT
	OpModFloat                       // MOD_FLOAT
	OpModBool                        // MOD_BOOL
	OpPow                            // POW
	OpPowInt                         // POW_INT
	OpPowFloat                       // POW_FLOAT
	OpPowBool                        // POW_BOOL
	OpBwAnd                          // BW_AND
	OpBwOr                           // BW_OR
	OpBwXor                          // BW_XOR
	OpBwNot                          // BW_NOT
	OpShiftLeft                      // LSHIFT
	OpShiftRight                     // RSHIFT
	OpEqual                          // EQUAL
	OpNotEqual                       // NOT_EQUAL
	OpIdentical                      // IDENTICAL
	OpNotIdentical                   // NOT_IDENTICAL
	OpGreater                        // GT
	OpLess                           // LT
	OpGreaterOrEqual                 // GTE
	OpLessOrEqual                    // LTE
	OpCompare                        // COMPARE
	OpArrayFetch                     // ARRAY_FETCH
	OpConcat                         // CONCAT

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
	OpArrayPut                  // ARRAY_PUT
	OpArrayPush                 // ARRAY_PUSH
	OpCast                      // CAST
	OpPreIncrement              // PRE_INC
	OpPostIncrement             // POST_INC
	OpPreDecrement              // PRE_DEC
	OpPostDecrement             // POST_DEC
	OpLoad                      // LOAD
	OpConst                     // CONST
	OpJump                      // JUMP
	OpJumpTrue                  // JUMP_TRUE
	OpJumpFalse                 // JUMP_FALSE
	OpCall                      // CALL
	OpEcho                      // ECHO
)

func arrayCompare(ctx *FunctionContext, x, y Array) Int {
	if len(x) < len(y) {
		return -1
	} else if len(x) > len(y) {
		return +1
	}

	for key, val := range x {
		if v, ok := y[key]; !ok {
			return +1
		} else if c := compare(ctx, val, v); c != 0 {
			return c
		}
	}

	return 0
}

func compare(ctx *FunctionContext, x, y Value) Int {
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

// Identical => x === y
func Identical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(left == right))
}

// NotIdentical => x !== y
func NotIdentical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(left != right))
}

// Equal => x == y
func Equal(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(equal(ctx, left, right))
}

// NotEqual => x != y
func NotEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(!equal(ctx, left, right))
}

func equal(ctx *FunctionContext, x, y Value) Bool {
	as := Juggle(x.Type(), y.Type())

	if as == ArrayType {
		return Bool(maps.EqualFunc(x.AsArray(ctx), y.AsArray(ctx), func(x, y Value) bool {
			return bool(equal(ctx, x, y))
		}))
	}

	return x.Cast(ctx, as) == y.Cast(ctx, as)
}

// LessOrEqual => x <= y
func LessOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) < 1))
}

// GreaterOrEqual => x >= y
func GreaterOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) > -1))
}

// Less => x < y
func Less(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) < 0))
}

// Greater => x > y
func Greater(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(Bool(compare(ctx, left, right) > 0))
}

// Compare => x <=> y
func Compare(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()
	ctx.SetTop(compare(ctx, left, right))
}

// Const => 0
func Const(ctx *FunctionContext) {
	ctx.Push(ctx.global.Constants[ctx.ReadRX()])
}

// Load => $a
func Load(ctx *FunctionContext) {
	if ctx.vars[ctx.ReadRX()] == nil {
		ctx.vars[ctx.ReadRX()] = Null{}
	}

	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// Assign => $a = 0
func Assign(ctx *FunctionContext) {
	ctx.vars[ctx.ReadRX()] = ctx.Pop()
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignAdd => $a += 1
func AssignAdd(ctx *FunctionContext) {
	right := ctx.Pop()

	switch Juggle(ctx.vars[ctx.ReadRX()].Type(), right.Type()) {
	case ArrayType:
		ctx.vars[ctx.ReadRX()] = addArray(ctx.vars[ctx.ReadRX()].AsArray(ctx), right.AsArray(ctx))
	case FloatType:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsFloat(ctx) + right.AsFloat(ctx)
	default:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) + right.AsInt(ctx)
	}

	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignSub => $a -= 1
func AssignSub(ctx *FunctionContext) {
	right := ctx.Pop()

	switch FloatType {
	case ctx.vars[ctx.ReadRX()].Type(), right.Type():
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsFloat(ctx) - right.AsFloat(ctx)
	default:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) - right.AsInt(ctx)
	}
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignMul => $a *= 1
func AssignMul(ctx *FunctionContext) {
	right := ctx.Pop()

	switch FloatType {
	case ctx.vars[ctx.ReadRX()].Type(), right.Type():
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsFloat(ctx) * right.AsFloat(ctx)
	default:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) * right.AsInt(ctx)
	}
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignDiv => $a /= 1
func AssignDiv(ctx *FunctionContext) {
	right := ctx.Pop()

	if res := ctx.vars[ctx.ReadRX()].AsFloat(ctx) / right.AsFloat(ctx); res == Float(int(res)) {
		ctx.vars[ctx.ReadRX()] = res.AsInt(ctx)
	} else {
		ctx.vars[ctx.ReadRX()] = res
	}
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignPow => $a **= 1
func AssignPow(ctx *FunctionContext) {
	right := ctx.Pop()
	as := Juggle(ctx.vars[ctx.ReadRX()].Type(), right.Type())

	if as == BoolType {
		ctx.vars[ctx.ReadRX()] = (!right.AsBool(ctx) || ctx.vars[ctx.ReadRX()].AsBool(ctx)).AsInt(ctx)
	} else {
		res := Float(math.Pow(float64(ctx.vars[ctx.ReadRX()].AsFloat(ctx)), float64(right.AsFloat(ctx))))
		ctx.vars[ctx.ReadRX()] = res.Cast(ctx, as)
	}
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignBwAnd => $a &= 1
func AssignBwAnd(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) & right
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignBwOr => $a |= 1
func AssignBwOr(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) | right
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignBwXor => $a ^= 1
func AssignBwXor(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) ^ right
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignConcat => $a .= 1
func AssignConcat(ctx *FunctionContext) {
	right := ctx.Pop().AsString(ctx)
	ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsString(ctx) + right
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignShiftLeft => $a <<= 1
func AssignShiftLeft(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) << right
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignShiftRight => $a >>= 1
func AssignShiftRight(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) >> right
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// AssignMod => $a %= 1
func AssignMod(ctx *FunctionContext) {
	right := ctx.Pop().AsFloat(ctx)
	left := ctx.vars[ctx.ReadRX()].AsFloat(ctx)

	if res := Float(math.Mod(float64(left), float64(right))); res == Float(int(res)) {
		ctx.vars[ctx.ReadRX()] = res.AsInt(ctx)
	} else {
		ctx.vars[ctx.ReadRX()] = res
	}
	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// Jump unconditional jump
func Jump(ctx *FunctionContext) {
	ctx.pc = ctx.ReadRX() - 1
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
	fn := ctx.GetFunction(ctx.ReadRX())
	fn.Invoke(ctx)
}

func Pop(ctx *FunctionContext) {
	ctx.Pop()
}

// ReturnValue => return 0;
func ReturnValue(ctx *FunctionContext) {
	v := ctx.Top()
	Return(ctx)
	ctx.Push(v)
}

// Return => return;
func Return(ctx *FunctionContext) {
	f := ctx.PopFrame()
	ctx.Sp(f.fp)
}

// Add => 1 + 2
func Add(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	switch FloatType {
	case left.Type(), right.Type():
		ctx.SetTop(left.AsFloat(ctx) + right.AsFloat(ctx))
		return
	}

	switch ArrayType {
	case left.Type(), right.Type():
		ctx.SetTop(addArray(left.AsArray(ctx), right.AsArray(ctx)))
	default:
		ctx.SetTop(left.AsInt(ctx) + right.AsInt(ctx))
	}
}

func addArray(left, right Array) Array {
	result := maps.Clone(right)
	maps.Copy(result, left)
	return result
}

func AddInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)
	ctx.SetTop(left + right)
}

func AddFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Top().(Float)
	ctx.SetTop(left + right)
}

func AddBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Top().(Bool)

	if right && left {
		ctx.SetTop(Int(2))
	} else if right || left {
		ctx.SetTop(Int(1))
	} else {
		ctx.SetTop(Int(0))
	}
}

func AddArray(ctx *FunctionContext) {
	right := ctx.Pop().(Array)
	left := ctx.Top().(Array)
	ctx.SetTop(addArray(left, right))
}

// Sub => 1 - 2
func Sub(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	switch FloatType {
	case left.Type(), right.Type():
		ctx.SetTop(left.AsFloat(ctx) - right.AsFloat(ctx))
	default:
		ctx.SetTop(left.AsInt(ctx) - right.AsInt(ctx))
	}
}

func SubInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)
	ctx.SetTop(left - right)
}

func SubFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Top().(Float)
	ctx.SetTop(left - right)
}

func SubBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Top().(Bool)

	if left && !right {
		ctx.SetTop(Int(1))
	} else if !left && right {
		ctx.SetTop(Int(-1))
	} else {
		ctx.SetTop(Int(0))
	}
}

// Mul => 1 * 2
func Mul(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Top()

	switch FloatType {
	case left.Type(), right.Type():
		ctx.SetTop(left.AsFloat(ctx) * right.AsFloat(ctx))
	default:
		ctx.SetTop(left.AsInt(ctx) * right.AsInt(ctx))
	}
}

func MulInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)
	ctx.SetTop(left * right)
}

func MulFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Top().(Float)
	ctx.SetTop(left * right)
}

func MulBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Top().(Bool)

	if left && right {
		ctx.SetTop(Int(1))
	} else {
		ctx.SetTop(Int(0))
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

func DivInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)
	ctx.SetTop(Float(left) / Float(right))
}

func DivFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Top().(Float)
	ctx.SetTop(left / right)
}

func DivBool(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)
	ctx.SetTop(left / right)
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

func ModInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)
	ctx.SetTop(Int(math.Mod(float64(left), float64(right))))
}

func ModFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Top().(Float)
	ctx.SetTop(Float(math.Mod(float64(left), float64(right))))
}

func ModBool(ctx *FunctionContext) {
	if ctx.Top().(Bool) {
		panic("modulo by zero error")
	}

	ctx.SetTop(Int(0))
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

func PowInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)
	ctx.SetTop(Int(math.Pow(float64(left), float64(right))))
}

func PowFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Top().(Float)
	ctx.SetTop(Float(math.Mod(float64(left), float64(right))))
}

func PowBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Top().(Bool)

	if !right || left {
		ctx.SetTop(Int(1))
	} else {
		ctx.SetTop(Int(0))
	}
}

// BwAnd => 1 & 2
func BwAnd(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)

	ctx.SetTop(left & right)
}

// BwOr => 1 | 2
func BwOr(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)

	ctx.SetTop(left | right)
}

// BwXor => 1 ^ 2
func BwXor(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Top().(Int)

	ctx.SetTop(left ^ right)
}

// BwNot => ~1
func BwNot(ctx *FunctionContext) {
	left := ctx.Top().(Int)
	ctx.SetTop(^left)
}

// ShiftLeft => 1 << 2
func ShiftLeft(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)

	ctx.SetTop(left << right)
}

// ShiftRight => 1 >> 2
func ShiftRight(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Top().AsInt(ctx)

	ctx.SetTop(left >> right)
}

// Cast => (type)$x
func Cast(ctx *FunctionContext) {
	val := ctx.Pop()
	ctx.Push(val.Cast(ctx, Type(ctx.ReadRX())))
}

// PreIncrement => ++$x
func PreIncrement(ctx *FunctionContext) {
	switch ctx.vars[ctx.ReadRX()].(type) {
	case Float:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Float) + 1
	case Int:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Int) + 1
	}

	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// PreDecrement => --$x
func PreDecrement(ctx *FunctionContext) {
	switch ctx.vars[ctx.ReadRX()].Type() {
	case FloatType:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Float) - 1
	case IntType:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Int) - 1
	}

	ctx.Push(ctx.vars[ctx.ReadRX()])
}

// PostIncrement => $x++
func PostIncrement(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.ReadRX()])

	switch ctx.vars[ctx.ReadRX()].Type() {
	case FloatType:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Float) + 1
	default:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].AsInt(ctx) + 1
	}
}

// PostDecrement => $x--
func PostDecrement(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.ReadRX()])

	switch ctx.vars[ctx.ReadRX()].Type() {
	case FloatType:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Float) + 1
	case IntType:
		ctx.vars[ctx.ReadRX()] = ctx.vars[ctx.ReadRX()].(Int) + 1
	}
}

// ArrayFetch => $a[0]
func ArrayFetch(ctx *FunctionContext) {
	key := ctx.Pop()
	array := ctx.Pop().AsArray(ctx)
	fetch := array[key]

	if fetch == nil {
		ctx.Push(Null{})
	} else {
		ctx.Push(fetch)
	}
}

// ArrayPut => $a[0] = 1
func ArrayPut(ctx *FunctionContext) {
	arr := ctx.vars[ctx.ReadRX()].AsArray(ctx)
	val := ctx.Pop()
	key := ctx.Pop()
	ctx.vars[ctx.ReadRX()] = maps.Clone(arr)
	ctx.vars[ctx.ReadRX()].(Array)[key] = val
}

// ArrayPush => $a[] = 1
func ArrayPush(ctx *FunctionContext) {
	val := ctx.Pop()
	ctx.vars[ctx.ReadRX()] = maps.Clone(ctx.vars[ctx.ReadRX()].AsArray(ctx))
	ctx.vars[ctx.ReadRX()].(Array)[ctx.vars[ctx.ReadRX()].(Array).NextKey()] = val
}

// Concat => $a . "string"
func Concat(ctx *FunctionContext) {
	right := ctx.Pop().AsString(ctx)
	left := ctx.Top().AsString(ctx)
	ctx.SetTop(left + right)
}

// AssertType => fn(int $a)
func AssertType(ctx *FunctionContext) {
	ctx.SetTop(ctx.Top().Cast(ctx, Type(ctx.ReadRX())))
}

// Echo => echo $x, $y;
func Echo(ctx *FunctionContext) {
	count := ctx.ReadRX()
	values := make([]any, count)

	for i, v := range ctx.Slice(-count, 0) {
		values[i] = v
	}

	fmt.Print(values...)
}
