package vm

import (
	"fmt"
	"golang.org/x/exp/maps"
	"math"
	"strconv"
	"strings"
)

type Bytecode []byte

func (b Bytecode) ReadOperation(ctx *FunctionContext) Operator {
	op := Operator(b[ctx.pc])

	if op > _opOneOperand {
		ctx.pc++
		ctx.rx = int(b[ctx.pc])
	}

	return op
}

func (b Bytecode) String() string {
	var ip int
	return string(Reduce(b, func(prev String, operator Operator, operands ...byte) String {
		strOperands := make([]string, 0, len(operands))

		for _, op := range operands {
			strOperands = append(strOperands, strconv.FormatUint(uint64(op), 10))
		}

		prev += String(fmt.Sprintf("\n%.5d: %-13s %s", ip, operator.String(), strings.Join(strOperands, ", ")))
		ip += 1 + len(operands)
		return prev
	}, ""))
}

func Reduce[T Value, F ~func(prev T, operator Operator, operands ...byte) T](bytecode Bytecode, f F, start T) T {
	ip := 0
	registers := make([]byte, 0, 2)

	for ip < len(bytecode) {
		op := Operator(bytecode[ip])

		if op > _opOneOperand {
			ip++
			registers = append(registers, bytecode[ip])
		}

		start = f(start, op, registers...)
		ip++
		registers = registers[:0]
	}

	return start
}

//go:generate stringer -type=Operator -linecomment
type Operator byte

const (
	OpNoop           Operator = iota // NOOP
	OpReturn                         // RETURN
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

	_opOneOperand   Operator = iota - 1
	OpAssertType             // ASSERT_TYPE
	OpAssign                 // ASSIGN
	OpAssignAdd              // ASSIGN_ADD
	OpAssignSub              // ASSIGN_SUB
	OpAssignMul              // ASSIGN_MUL
	OpAssignDiv              // ASSIGN_DIV
	OpAssignMod              // ASSIGN_MOD
	OpArrayPut               // ARRAY_PUT
	OpArrayPush              // ARRAY_PUSH
	OpCast                   // CAST
	OpPreIncrement           // PRE_INC
	OpPostIncrement          // POST_INC
	OpPreDecrement           // PRE_DEC
	OpPostDecrement          // POST_DEC
	OpLoad                   // LOAD
	OpConst                  // CONST
	OpJump                   // JUMP
	OpJumpZ                  // JUMP_Z
	OpJumpNZ                 // JUMP_NZ
	OpCall                   // CALL
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
	left := ctx.Pop()
	ctx.Push(Bool(left == right))
}

// NotIdentical => x !== y
func NotIdentical(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()

	ctx.Push(Bool(left != right))
}

// Equal => x == y
func Equal(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(equal(ctx, left, right))
}

// NotEqual => x != y
func NotEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(!equal(ctx, left, right))
}

func equal(ctx *FunctionContext, x, y Value) Bool {
	as := Juggle(x.Type(), y.Type())

	if as == ArrayType {
		return Bool(maps.EqualFunc(x.AsArray(ctx), y.AsArray(ctx), func(x Value, y Value) bool {
			return bool(equal(ctx, x, y))
		}))
	}

	return x.Cast(ctx, as) == y.Cast(ctx, as)
}

// LessOrEqual => x <= y
func LessOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(Bool(compare(ctx, left, right) < 1))
}

// GreaterOrEqual => x >= y
func GreaterOrEqual(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(Bool(compare(ctx, left, right) > -1))
}

// Less => x < y
func Less(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(Bool(compare(ctx, left, right) < 0))
}

// Greater => x > y
func Greater(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(Bool(compare(ctx, left, right) > 0))
}

// Compare => x <=> y
func Compare(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Pop()
	ctx.Push(compare(ctx, left, right))
}

// Const => 0
func Const(ctx *FunctionContext) {
	ctx.Push(ctx.constants[ctx.rx])
}

// Load => $a
func Load(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.rx])
}

// Assign => $a = 0
func Assign(ctx *FunctionContext) {
	ctx.vars[ctx.rx] = ctx.Pop()
}

// AssignAdd => $a += 1
func AssignAdd(ctx *FunctionContext) {
	right := ctx.Pop()

	switch FloatType {
	case ctx.vars[ctx.rx].Type(), right.Type():
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].AsFloat(ctx) + right.AsFloat(ctx)
	default:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].AsInt(ctx) + right.AsInt(ctx)
	}
}

// AssignSub => $a -= 1
func AssignSub(ctx *FunctionContext) {
	right := ctx.Pop()

	switch FloatType {
	case ctx.vars[ctx.rx].Type(), right.Type():
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].AsFloat(ctx) - right.AsFloat(ctx)
	default:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].AsInt(ctx) - right.AsInt(ctx)
	}
}

// AssignMul => $a *= 1
func AssignMul(ctx *FunctionContext) {
	right := ctx.Pop()

	switch FloatType {
	case ctx.vars[ctx.rx].Type(), right.Type():
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].AsFloat(ctx) * right.AsFloat(ctx)
	default:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].AsInt(ctx) * right.AsInt(ctx)
	}
}

// AssignDiv => $a /= 1
func AssignDiv(ctx *FunctionContext) {
	right := ctx.Pop()

	if res := ctx.vars[ctx.rx].AsFloat(ctx) / right.AsFloat(ctx); res == Float(int(res)) {
		ctx.vars[ctx.rx] = res.AsInt(ctx)
	} else {
		ctx.vars[ctx.rx] = res
	}
}

// AssignMod => $a %= 1
func AssignMod(ctx *FunctionContext) {
	right := ctx.Pop().AsFloat(ctx)
	left := ctx.vars[ctx.rx].AsFloat(ctx)

	if res := Float(math.Mod(float64(left), float64(right))); res == Float(int(res)) {
	    ctx.vars[ctx.rx] = res.AsInt(ctx)
	} else {
		ctx.vars[ctx.rx] = res
	}
}

// Jump unconditional jump
func Jump(ctx *FunctionContext) {
	ctx.pc = ctx.rx - 1
}

// JumpZ if (true_statement)
func JumpZ(ctx *FunctionContext) {
	if ctx.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// JumpNZ for ($i = 0; $i < 1; $i++) {}
func JumpNZ(ctx *FunctionContext) {
	if !ctx.Pop().AsBool(ctx) {
		Jump(ctx)
	}
}

// Call => someFunction($a, $x)
func Call(ctx *FunctionContext) {
	fn := ctx.GetFunction(ctx.rx)
	res := fn.Invoke(ctx)
	ctx.Push(res)
}

// Return => return 0
func Return(ctx *FunctionContext) {
	ctx.returned = true
}

// Add => 1 + 2
func Add(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Offset(0)

	if left.Type() == ArrayType || right.Type() == ArrayType {
        result := maps.Clone(left.AsArray(ctx))

		for key, val := range right.AsArray(ctx) {
			if _, ok := result[key]; !ok {
				result[key] = val
			}
		}

		ctx.Put(ctx.TopIndex(), result)
		return
	}

	if left.Type() == FloatType || right.Type() == FloatType {
		ctx.Put(ctx.TopIndex(), left.AsFloat(ctx)+right.AsFloat(ctx))
		return
	}

	ctx.Put(ctx.TopIndex(), left.AsInt(ctx)+right.AsInt(ctx))
}

func AddInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Offset(0).(Int)
	ctx.Put(ctx.TopIndex(), left+right)
}

func AddFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Offset(0).(Float)
	ctx.Put(ctx.TopIndex(), left+right)
}

func AddBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Offset(0).(Bool)

	if right && left {
		ctx.Put(ctx.TopIndex(), Int(2))
	} else if right || left {
		ctx.Put(ctx.TopIndex(), Int(1))
	} else {
		ctx.Put(ctx.TopIndex(), Int(0))
	}
}

func AddArray(ctx *FunctionContext) {
	right := ctx.Pop().(Array)
	left := ctx.Offset(0).(Array)

	result := maps.Clone(left.AsArray(ctx))

	for key, val := range right.AsArray(ctx) {
		if _, ok := result[key]; !ok {
			result[key] = val
		}
	}

	ctx.Put(ctx.TopIndex(), result)
}

// Sub => 1 - 2
func Sub(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Offset(0)

	switch FloatType {
	case left.Type(), right.Type():
		ctx.Put(ctx.TopIndex(), left.AsFloat(ctx)-right.AsFloat(ctx))
	default:
		ctx.Put(ctx.TopIndex(), left.AsInt(ctx)-right.AsInt(ctx))
	}
}

func SubInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Offset(0).(Int)
	ctx.Put(ctx.TopIndex(), left-right)
}

func SubFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Offset(0).(Float)
	ctx.Put(ctx.TopIndex(), left-right)
}

func SubBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Offset(0).(Bool)

	if left && !right {
		ctx.Put(ctx.TopIndex(), Int(1))
	} else if !left && right {
		ctx.Put(ctx.TopIndex(), Int(-1))
	} else {
		ctx.Put(ctx.TopIndex(), Int(0))
	}
}

// Mul => 1 * 2
func Mul(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Offset(0)

	switch FloatType {
	case left.Type(), right.Type():
		ctx.Put(ctx.TopIndex(), left.AsFloat(ctx)*right.AsFloat(ctx))
	default:
		ctx.Put(ctx.TopIndex(), left.AsInt(ctx)*right.AsInt(ctx))
	}
}

func MulInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Offset(0).(Int)
	ctx.Put(ctx.TopIndex(), left*right)
}

func MulFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Offset(0).(Float)
	ctx.Put(ctx.TopIndex(), left*right)
}

func MulBool(ctx *FunctionContext) {
	right := ctx.Pop().(Bool)
	left := ctx.Offset(0).(Bool)

	if left && right {
		ctx.Put(ctx.TopIndex(), Int(1))
	} else {
		ctx.Put(ctx.TopIndex(), Int(0))
	}
}

// Div => 1 / 2
func Div(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Offset(0)

	if res := left.AsFloat(ctx) / right.AsFloat(ctx); res == Float(int(res)) {
		ctx.Put(ctx.TopIndex(), res.AsInt(ctx))
	} else {
		ctx.Put(ctx.TopIndex(), res)
	}
}

func DivInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Offset(0).(Int)
	ctx.Put(ctx.TopIndex(), Float(left)/Float(right))
}

func DivFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Offset(0).(Float)
	ctx.Put(ctx.TopIndex(), left/right)
}

func DivBool(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Offset(0).AsInt(ctx)
	ctx.Put(ctx.TopIndex(), left/right)
}

// Mod => 1 % 2
func Mod(ctx *FunctionContext) {
	right := ctx.Pop()
	left := ctx.Offset(0)
	as := Juggle(left.Type(), right.Type())

	switch as {
	case BoolType:
		// true = 1, false = 0. Dividing by zero is not allowed, dividing by 1 result to 0 always
		ctx.Push(Int(0))
	default:
		res := Float(math.Mod(float64(left.AsFloat(ctx)), float64(right.AsFloat(ctx))))
		ctx.Put(ctx.TopIndex(), res.Cast(ctx, as))
	}
}

func ModInt(ctx *FunctionContext) {
	right := ctx.Pop().(Int)
	left := ctx.Offset(0).(Int)
	ctx.Put(ctx.TopIndex(), Int(math.Mod(float64(left), float64(right))))
}

func ModFloat(ctx *FunctionContext) {
	right := ctx.Pop().(Float)
	left := ctx.Offset(0).(Float)
	ctx.Put(ctx.TopIndex(), Float(math.Mod(float64(left), float64(right))))
}

func ModBool(ctx *FunctionContext) {
	right := ctx.Pop().AsInt(ctx)
	left := ctx.Offset(0).AsInt(ctx)
	ctx.Put(ctx.TopIndex(), Int(math.Mod(float64(left), float64(right))))
}

// Cast => (type)$x
func Cast(ctx *FunctionContext) {
	val := ctx.Pop()
	ctx.Push(val.Cast(ctx, Type(ctx.rx)))
}

// PreIncrement => ++$x
func PreIncrement(ctx *FunctionContext) {
	switch ctx.vars[ctx.rx].(type) {
	case Float:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Float) + 1
	case Int:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Int) + 1
	}
}

// PreDecrement => --$x
func PreDecrement(ctx *FunctionContext) {
	switch ctx.vars[ctx.rx].Type() {
	case FloatType:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Float) - 1
	case IntType:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Int) - 1
	}
}

// PostIncrement => $x++
func PostIncrement(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.rx])

	switch ctx.vars[ctx.rx].Type() {
	case FloatType:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Float) + 1
	case IntType:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Int) + 1
	}
}

// PostDecrement => $x--
func PostDecrement(ctx *FunctionContext) {
	ctx.Push(ctx.vars[ctx.rx])

	switch ctx.vars[ctx.rx].Type() {
	case FloatType:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Float) + 1
	case IntType:
		ctx.vars[ctx.rx] = ctx.vars[ctx.rx].(Int) + 1
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
	arr := ctx.vars[ctx.rx].AsArray(ctx)
	val := ctx.Pop()
	key := ctx.Pop()
	ctx.vars[ctx.rx] = maps.Clone(arr)
	ctx.vars[ctx.rx].(Array)[key] = val
}

// ArrayPush => $a[] = 1
func ArrayPush(ctx *FunctionContext) {
	val := ctx.Pop()
	ctx.vars[ctx.rx] = maps.Clone(ctx.vars[ctx.rx].AsArray(ctx))
	ctx.vars[ctx.rx].(Array)[ctx.vars[ctx.rx].(Array).NextKey()] = val
}

// Concat => $a . "string"
func Concat(ctx *FunctionContext) {
	right := ctx.Pop().AsString(ctx)
	left := ctx.Offset(0).AsString(ctx)
	ctx.Put(ctx.TopIndex(), left+right)
}

// AsertType => fn(int $a)
func AssertType(ctx *FunctionContext) {
	ctx.Put(ctx.TopIndex(), ctx.Offset(0).Cast(ctx, Type(ctx.rx)))
}
