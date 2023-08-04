// Code generated by "stringer -type=Type -linecomment"; DO NOT EDIT.

package vm

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[NullType-0]
	_ = x[IntType-1]
	_ = x[FloatType-2]
	_ = x[StringType-3]
	_ = x[ArrayType-4]
	_ = x[ObjectType-5]
	_ = x[BoolType-6]
}

const _Type_name = "nullintegerfloatstringarrayobjectboolean"

var _Type_index = [...]uint8{0, 4, 11, 16, 22, 27, 33, 40}

func (i Type) String() string {
	if i >= Type(len(_Type_index)-1) {
		return "Type(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Type_name[_Type_index[i]:_Type_index[i+1]]
}
