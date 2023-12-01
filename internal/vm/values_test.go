package vm

import (
	"fmt"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"math"
	"math/rand"
	"testing"
)

type IntTest struct{ suite.Suite }

func (t *IntTest) TestIsRef()   { t.False(Int(0).IsRef()) }
func (t *IntTest) TestType()    { t.Equal(IntType, Int(0).Type().shape) }
func (t *IntTest) TestAsInt()   { t.Equal(Int(0), Int(0).AsInt(nil)) }
func (t *IntTest) TestAsFloat() { t.Equal(Float(0), Int(0).AsFloat(nil)) }
func (t *IntTest) TestAsBool() {
	cases := [...]struct {
		expected Bool
		value    Int
	}{
		{true, math.MaxInt},
		{true, 1},
		{false, 0},
		{true, -1},
		{true, math.MinInt},
	}

	for _, c := range cases {
		t.Equal(c.expected, c.value.AsBool(nil))
	}
}
func (t *IntTest) TestAsString() {
	randomInt := Value(Int(rand.Int()))
	t.EqualValues(fmt.Sprint(randomInt), randomInt.AsString(nil))
}
func (t *IntTest) TestAsNull() { t.Equal(Null{}, Int(0).AsNull(nil)) }
func (t *IntTest) TestAsArray() {
	randomInt := Value(Int(rand.Int()))
	t.Equal(&Array{hash: HashTable[Value, Value]{map[Value]*htValue[Value]{String("scalar"): {randomInt}}}, next: math.MinInt}, randomInt.AsArray(nil))
}
func (t *IntTest) TestAsObject()  {}
func (t *IntTest) TestDebugInfo() { t.EqualValues("int(0)", Int(0).DebugInfo(nil)) }

type FloatTest struct{ suite.Suite }

func (t *FloatTest) TestIsRef() { t.False(Float(0).IsRef()) }
func (t *FloatTest) TestType()  { t.Equal(FloatType, Float(0).Type().shape) }
func (t *FloatTest) TestAsInt() {
	t.Equal(Int(0), Float(0).AsInt(nil))
	t.Equal(Int(1), Float(1.1).AsInt(nil))
}
func (t *FloatTest) TestAsFloat() {
	t.Equal(Float(0), Float(0).AsFloat(nil))
	t.Equal(Float(1.1), Float(1.1).AsFloat(nil))
}
func (t *FloatTest) TestAsBool() {
	cases := [...]struct {
		expected Bool
		value    Float
	}{
		{true, math.MaxFloat64},
		{true, 1},
		{false, 0},
		{true, 0.1},
		{true, -1},
		{true, 1.1},
	}

	for _, c := range cases {
		t.Equal(c.expected, c.value.AsBool(nil))
	}
}
func (t *FloatTest) TestAsString() {
	randomFloat := Value(Float(rand.Float64()))
	t.EqualValues(fmt.Sprint(randomFloat), randomFloat.AsString(nil))
}
func (t *FloatTest) TestAsNull() { t.Equal(Null{}, Float(0).AsNull(nil)) }
func (t *FloatTest) TestAsArray() {
	randomFloat := Value(Float(rand.Float64()))
	t.Equal(&Array{hash: HashTable[Value, Value]{map[Value]*htValue[Value]{String("scalar"): {randomFloat}}}, next: math.MinInt}, randomFloat.AsArray(nil))
}
func (t *FloatTest) TestAsObject()  {}
func (t *FloatTest) TestDebugInfo() { t.EqualValues("float(0)", Float(0).DebugInfo(nil)) }

type BoolTest struct{ suite.Suite }

func (t *BoolTest) TestIsRef() { t.False(Bool(false).IsRef()) }
func (t *BoolTest) TestType()  { t.Equal(BoolType, Bool(false).Type().shape) }
func (t *BoolTest) TestAsInt() {
	t.Equal(Int(0), Bool(false).AsInt(nil))
	t.Equal(Int(1), Bool(true).AsInt(nil))
}
func (t *BoolTest) TestAsFloat() {
	t.Equal(Float(0), Bool(false).AsFloat(nil))
	t.Equal(Float(1), Bool(true).AsFloat(nil))
}
func (t *BoolTest) TestAsBool() {
	t.Equal(Bool(false), Bool(false).AsBool(nil))
	t.Equal(Bool(true), Bool(true).AsBool(nil))
}
func (t *BoolTest) TestAsString() {
	t.Equal(String("false"), Bool(false).AsString(nil))
	t.Equal(String("true"), Bool(true).AsString(nil))
}
func (t *BoolTest) TestAsNull() {
	t.Equal(Null{}, Bool(false).AsNull(nil))
	t.Equal(Null{}, Bool(true).AsNull(nil))
}
func (t *BoolTest) TestAsArray() {
	t.Equal(NewArray(map[Value]Value{String("scalar"): Bool(false)}), Bool(false).AsArray(nil))
	t.Equal(NewArray(map[Value]Value{String("scalar"): Bool(true)}), Bool(true).AsArray(nil))
}
func (t *BoolTest) TestAsObject()  {}
func (t *BoolTest) TestDebugInfo() { t.EqualValues("bool(false)", Bool(false).DebugInfo(nil)) }

type StringTest struct{ suite.Suite }

func (t *StringTest) TestIsRef() { t.False(String("").IsRef()) }
func (t *StringTest) TestType()  { t.Equal(StringType, String("").Type().shape) }
func (t *StringTest) TestAsInt() {
	t.Equal(Int(0), String("").AsInt(nil))
	t.Equal(Int(0), String("0").AsInt(nil))
	t.Equal(Int(1), String("1").AsInt(nil))
}
func (t *StringTest) TestAsFloat() {
	t.Equal(Float(0), String("").AsFloat(nil))
	t.Equal(Float(0), String("0").AsFloat(nil))
	t.Equal(Float(1), String("1").AsFloat(nil))
}
func (t *StringTest) TestAsBool() {
	t.Equal(Bool(true), String("true").AsBool(nil))
	t.Equal(Bool(true), String("false").AsBool(nil))
	t.Equal(Bool(false), String("").AsBool(nil))
}
func (t *StringTest) TestAsString() { t.Equal(String(""), String("").AsString(nil)) }
func (t *StringTest) TestAsNull()   { t.Equal(Null{}, String("").AsNull(nil)) }
func (t *StringTest) TestAsArray() {
	t.Equal(NewArray(map[Value]Value{String("scalar"): String("")}), String("").AsArray(nil))
}
func (t *StringTest) TestAsObject()  {}
func (t *StringTest) TestDebugInfo() { t.EqualValues("string(\"\")", String("").DebugInfo(nil)) }

type NullTest struct{ suite.Suite }

func (t *NullTest) TestIsRef()     { t.False(Null{}.IsRef()) }
func (t *NullTest) TestType()      { t.Equal(NullType, Null{}.Type().shape) }
func (t *NullTest) TestAsInt()     { t.Equal(Int(0), Null{}.AsInt(nil)) }
func (t *NullTest) TestAsFloat()   { t.Equal(Float(0), Null{}.AsFloat(nil)) }
func (t *NullTest) TestAsBool()    { t.Equal(Bool(false), Null{}.AsBool(nil)) }
func (t *NullTest) TestAsString()  { t.Equal(String(""), Null{}.AsString(nil)) }
func (t *NullTest) TestAsNull()    { t.Equal(Null{}, Null{}.AsNull(nil)) }
func (t *NullTest) TestAsArray()   { t.Equal(NewArray(nil), Null{}.AsArray(nil)) }
func (t *NullTest) TestAsObject()  {}
func (t *NullTest) TestDebugInfo() { t.EqualValues("NULL", Null{}.DebugInfo(nil)) }

type ArrayTest struct{ suite.Suite }

func (t *ArrayTest) TestCount() {
	hash := map[Value]Value{String("test"): Int(1)}
	t.EqualValues(len(hash), NewArray(hash).Count(nil))
}
func (t *ArrayTest) TestCopy() {
	hash := map[Value]Value{String("test"): Int(1)}

	t.EqualValues(len(hash), NewArray(hash).Count(nil))
}
func (t *ArrayTest) Test_access() {}
func (t *ArrayTest) Test_assign() {}
func (t *ArrayTest) Test_delete() {}

func (t *ArrayTest) TestOffsetGet() {
	arr := NewArray(map[Value]Value{String("test"): Int(1)})
	t.EqualValues(1, arr.OffsetGet(nil, String("test")))
}
func (t *ArrayTest) TestOffsetSet() {
	arr := NewArray(nil)
	arr.OffsetSet(nil, String("test"), Int(1))
	t.Contains(arr.hash.internal, String("test"))
	t.EqualValues(1, arr.hash.internal[String("test")].v)
}
func (t *ArrayTest) TestOffsetIsSet() {
	t.True(bool(NewArray(map[Value]Value{String("test"): Int(1)}).OffsetIsSet(nil, String("test"))))
	t.False(bool(NewArray(map[Value]Value{String("test1"): Int(1)}).OffsetIsSet(nil, String("test"))))
}
func (t *ArrayTest) TestOffsetUnset() {
	arr := NewArray(map[Value]Value{String("test"): Int(1)})
	arr.OffsetUnset(nil, String("test"))
	t.NotContains(arr.hash.internal, String("test"))
}
func (t *ArrayTest) TestIsRef() { t.False(NewArray(nil).IsRef()) }
func (t *ArrayTest) TestType()  { t.Equal(ArrayType, NewArray(nil).Type().shape) }
func (t *ArrayTest) TestAsInt() {
	t.Equal(Int(0), NewArray(nil).AsInt(nil))
	t.Equal(Int(1), NewArray(map[Value]Value{Int(0): Int(1)}).AsInt(nil))
	t.Equal(Int(1), NewArray(map[Value]Value{Int(0): Int(1), Int(1): Int(2)}).AsInt(nil))
}
func (t *ArrayTest) TestAsFloat() {
	t.Equal(Float(0), NewArray(nil).AsFloat(nil))
	t.Equal(Float(1), NewArray(map[Value]Value{Int(0): Int(1)}).AsFloat(nil))
	t.Equal(Float(1), NewArray(map[Value]Value{Int(0): Int(1), Int(1): Int(2)}).AsFloat(nil))
}
func (t *ArrayTest) TestAsBool() {
	t.Equal(Bool(false), NewArray(nil).AsBool(nil))
	t.Equal(Bool(true), NewArray(map[Value]Value{Int(0): Int(1)}).AsBool(nil))
}

type contextMock struct {
	Context
	mock.Mock
}

func (c *contextMock) Throw(t Throwable) { c.Mock.Called(t) }

func (t *ArrayTest) TestAsString() {
	m := new(contextMock)
	m.On("Throw", mock.MatchedBy(func(t Throwable) bool { return t.Level() == EWarning }))
	NewArray(nil).AsString(m)
}
func (t *ArrayTest) TestAsNull() { t.Equal(Null{}, NewArray(nil).AsNull(nil)) }
func (t *ArrayTest) TestAsArray() {
	arr := NewArray(nil)
	t.Equal(arr, arr.AsArray(nil))
}
func (t *ArrayTest) TestAsObject() {}
func (t *ArrayTest) TestNextKey() {
	arr := NewArray(nil)
	t.EqualValues(0, arr.NextKey())
	arr.OffsetSet(nil, Int(0), String("test"))
	t.EqualValues(1, arr.NextKey())
}
func (t *ArrayTest) TestDebugInfo() {
	arr := NewArray(map[Value]Value{String("test"): Int(1)})
	t.EqualValues("array(1) {\n  [\"test\"]=>\n  int(1)\n}", arr.DebugInfo(nil))
}

type RefTest struct{ suite.Suite }

func (t *RefTest) TestIsRef() { t.True(Ref{}.IsRef()) }
func (t *RefTest) TestDeref() {
	i := Value(Int(0))
	t.Equal(&i, Ref{&i}.Deref())
}
func (t *RefTest) TestType() {
	i := Value(Int(0))
	t.Equal(IntType, Ref{&i}.Type().shape)
}
func (t *RefTest) TestAsInt() {
	i := Value(Int(0))
	t.Equal(Int(0), Ref{&i}.AsInt(nil))
}
func (t *RefTest) TestAsFloat() {
	i := Value(Float(0))
	t.Equal(Float(0), Ref{&i}.AsFloat(nil))
}
func (t *RefTest) TestAsBool() {
	i := Value(Bool(false))
	t.Equal(Bool(false), Ref{&i}.AsBool(nil))
}
func (t *RefTest) TestAsString() {
	i := Value(String(""))
	t.Equal(String(""), Ref{&i}.AsString(nil))
}
func (t *RefTest) TestAsNull() { t.Equal(Null{}, NewRef(nil).AsNull(nil)) }
func (t *RefTest) TestAsArray() {
	arr := Value(NewArray(nil))
	t.Equal(arr, NewRef(&arr).AsArray(nil))
}
func (t *RefTest) TestAsObject() {}
func (t *RefTest) TestDebugInfo() {
	i := Value(Int(0))
	t.EqualValues("&int(0)", Ref{&i}.DebugInfo(nil))
}

//type ObjectTest struct{ suite.Suite }

func TestInt(t *testing.T)    { suite.Run(t, new(IntTest)) }
func TestFloat(t *testing.T)  { suite.Run(t, new(FloatTest)) }
func TestBool(t *testing.T)   { suite.Run(t, new(BoolTest)) }
func TestString(t *testing.T) { suite.Run(t, new(StringTest)) }
func TestNull(t *testing.T)   { suite.Run(t, new(NullTest)) }
func TestArray(t *testing.T)  { suite.Run(t, new(ArrayTest)) }
func TestRef(t *testing.T)    { suite.Run(t, new(RefTest)) }

//func TestObject(t *testing.T) { suite.Run(t, new(ObjectTest)) }
