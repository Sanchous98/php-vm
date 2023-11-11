// Code generated by "stringer -type=Operator -linecomment"; DO NOT EDIT.

package vm

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[OpNoop-0]
	_ = x[OpPop-1]
	_ = x[OpPop2-2]
	_ = x[OpReturn-3]
	_ = x[OpReturnValue-4]
	_ = x[OpAdd-5]
	_ = x[OpSub-6]
	_ = x[OpMul-7]
	_ = x[OpDiv-8]
	_ = x[OpMod-9]
	_ = x[OpPow-10]
	_ = x[OpBwAnd-11]
	_ = x[OpBwOr-12]
	_ = x[OpBwXor-13]
	_ = x[OpBwNot-14]
	_ = x[OpShiftLeft-15]
	_ = x[OpShiftRight-16]
	_ = x[OpEqual-17]
	_ = x[OpNotEqual-18]
	_ = x[OpIdentical-19]
	_ = x[OpNotIdentical-20]
	_ = x[OpNot-21]
	_ = x[OpGreater-22]
	_ = x[OpLess-23]
	_ = x[OpGreaterOrEqual-24]
	_ = x[OpLessOrEqual-25]
	_ = x[OpCompare-26]
	_ = x[OpAssignRef-27]
	_ = x[OpArrayNew-28]
	_ = x[OpArrayAccessRead-29]
	_ = x[OpArrayAccessWrite-30]
	_ = x[OpArrayAccessPush-31]
	_ = x[OpArrayUnset-32]
	_ = x[OpConcat-33]
	_ = x[OpUnset-34]
	_ = x[OpForEachInit-35]
	_ = x[OpForEachNext-36]
	_ = x[OpForEachValid-37]
	_ = x[OpForEachReset-38]
	_ = x[_opOneOperand-38]
	_ = x[OpAssertType-39]
	_ = x[OpAssign-40]
	_ = x[OpAssignAdd-41]
	_ = x[OpAssignSub-42]
	_ = x[OpAssignMul-43]
	_ = x[OpAssignDiv-44]
	_ = x[OpAssignMod-45]
	_ = x[OpAssignPow-46]
	_ = x[OpAssignBwAnd-47]
	_ = x[OpAssignBwOr-48]
	_ = x[OpAssignBwXor-49]
	_ = x[OpAssignConcat-50]
	_ = x[OpAssignShiftLeft-51]
	_ = x[OpAssignShiftRight-52]
	_ = x[OpCast-53]
	_ = x[OpPreIncrement-54]
	_ = x[OpPostIncrement-55]
	_ = x[OpPreDecrement-56]
	_ = x[OpPostDecrement-57]
	_ = x[OpLoad-58]
	_ = x[OpLoadRef-59]
	_ = x[OpConst-60]
	_ = x[OpJump-61]
	_ = x[OpJumpTrue-62]
	_ = x[OpJumpFalse-63]
	_ = x[OpCall-64]
	_ = x[OpEcho-65]
	_ = x[OpIsSet-66]
	_ = x[OpForEachKey-67]
	_ = x[OpForEachValue-68]
	_ = x[OpForEachValueRef-69]
}

const _Operator_name = "NOOPPOPPOP2RETURNRETURN_VALADDSUBMULDIVMODPOWBW_ANDBW_ORBW_XORBW_NOTLSHIFTRSHIFTEQUALNOT_EQUALIDENTICALNOT_IDENTICALNOTGTLTGTELTECOMPAREASSIGN_REFARRAY_NEWARRAY_ACCESS_READARRAY_ACCESS_WRITEARRAY_ACCESS_PUSHARRAY_UNSETCONCATUNSETFE_INITFE_NEXTFE_VALIDFE_RESETASSERT_TYPEASSIGNASSIGN_ADDASSIGN_SUBASSIGN_MULASSIGN_DIVASSIGN_MODASSIGN_POWASSIGN_BW_ANDASSIGN_BW_ORASSIGN_BW_XORASSIGN_CONCATASSIGN_LSHIFTASSIGN_RSHIFTCASTPRE_INCPOST_INCPRE_DECPOST_DECLOADLOAD_REFCONSTJUMPJUMP_TRUEJUMP_FALSECALLECHOISSETFE_KEYFE_VALUEFE_VALUE_REF"

var _Operator_index = [...]uint16{0, 4, 7, 11, 17, 27, 30, 33, 36, 39, 42, 45, 51, 56, 62, 68, 74, 80, 85, 94, 103, 116, 119, 121, 123, 126, 129, 136, 146, 155, 172, 190, 207, 218, 224, 229, 236, 243, 251, 259, 270, 276, 286, 296, 306, 316, 326, 336, 349, 361, 374, 387, 400, 413, 417, 424, 432, 439, 447, 451, 459, 464, 468, 477, 487, 491, 495, 500, 506, 514, 526}

func (i Operator) String() string {
	if i >= Operator(len(_Operator_index)-1) {
		return "Operator(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Operator_name[_Operator_index[i]:_Operator_index[i+1]]
}
