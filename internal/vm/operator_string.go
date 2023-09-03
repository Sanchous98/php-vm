// Code generated by "stringer -type=Operator -linecomment"; DO NOT EDIT.

package vm

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[OpNoop-0]
	_ = x[OpPop-1]
	_ = x[OpReturn-2]
	_ = x[OpReturnValue-3]
	_ = x[OpAdd-4]
	_ = x[OpAddInt-5]
	_ = x[OpAddFloat-6]
	_ = x[OpAddArray-7]
	_ = x[OpAddBool-8]
	_ = x[OpSub-9]
	_ = x[OpSubInt-10]
	_ = x[OpSubFloat-11]
	_ = x[OpSubBool-12]
	_ = x[OpMul-13]
	_ = x[OpMulInt-14]
	_ = x[OpMulFloat-15]
	_ = x[OpMulBool-16]
	_ = x[OpDiv-17]
	_ = x[OpDivInt-18]
	_ = x[OpDivFloat-19]
	_ = x[OpDivBool-20]
	_ = x[OpMod-21]
	_ = x[OpModInt-22]
	_ = x[OpModFloat-23]
	_ = x[OpModBool-24]
	_ = x[OpPow-25]
	_ = x[OpPowInt-26]
	_ = x[OpPowFloat-27]
	_ = x[OpPowBool-28]
	_ = x[OpBwAnd-29]
	_ = x[OpBwOr-30]
	_ = x[OpBwXor-31]
	_ = x[OpBwNot-32]
	_ = x[OpShiftLeft-33]
	_ = x[OpShiftRight-34]
	_ = x[OpEqual-35]
	_ = x[OpNotEqual-36]
	_ = x[OpIdentical-37]
	_ = x[OpNotIdentical-38]
	_ = x[OpGreater-39]
	_ = x[OpLess-40]
	_ = x[OpGreaterOrEqual-41]
	_ = x[OpLessOrEqual-42]
	_ = x[OpCompare-43]
	_ = x[OpArrayFetch-44]
	_ = x[OpConcat-45]
	_ = x[_opOneOperand-45]
	_ = x[OpAssertType-46]
	_ = x[OpAssign-47]
	_ = x[OpAssignAdd-48]
	_ = x[OpAssignSub-49]
	_ = x[OpAssignMul-50]
	_ = x[OpAssignDiv-51]
	_ = x[OpAssignMod-52]
	_ = x[OpAssignPow-53]
	_ = x[OpAssignBwAnd-54]
	_ = x[OpAssignBwOr-55]
	_ = x[OpAssignBwXor-56]
	_ = x[OpAssignConcat-57]
	_ = x[OpAssignShiftLeft-58]
	_ = x[OpAssignShiftRight-59]
	_ = x[OpArrayPut-60]
	_ = x[OpArrayPush-61]
	_ = x[OpCast-62]
	_ = x[OpPreIncrement-63]
	_ = x[OpPostIncrement-64]
	_ = x[OpPreDecrement-65]
	_ = x[OpPostDecrement-66]
	_ = x[OpLoad-67]
	_ = x[OpConst-68]
	_ = x[OpJump-69]
	_ = x[OpJumpTrue-70]
	_ = x[OpJumpFalse-71]
	_ = x[OpCall-72]
	_ = x[OpEcho-73]
}

const _Operator_name = "NOOPPOPRETURNRETURN_VALADDADD_INTADD_FLOATADD_ARRAYADD_BOOLSUBSUB_INTSUB_FLOATSUB_BOOLMULMUL_INTMUL_FLOATMUL_BOOLDIVDIV_INTDIV_FLOATDIV_BOOLMODMOD_INTMOD_FLOATMOD_BOOLPOWPOW_INTPOW_FLOATPOW_BOOLBW_ANDBW_ORBW_XORBW_NOTLSHIFTRSHIFTEQUALNOT_EQUALIDENTICALNOT_IDENTICALGTLTGTELTECOMPAREARRAY_FETCHCONCATASSERT_TYPEASSIGNASSIGN_ADDASSIGN_SUBASSIGN_MULASSIGN_DIVASSIGN_MODASSIGN_POWASSIGN_BW_ANDASSIGN_BW_ORASSIGN_BW_XORASSIGN_CONCATASSIGN_LSHIFTASSIGN_RSHIFTARRAY_PUTARRAY_PUSHCASTPRE_INCPOST_INCPRE_DECPOST_DECLOADCONSTJUMPJUMP_TRUEJUMP_FALSECALLECHO"

var _Operator_index = [...]uint16{0, 4, 7, 13, 23, 26, 33, 42, 51, 59, 62, 69, 78, 86, 89, 96, 105, 113, 116, 123, 132, 140, 143, 150, 159, 167, 170, 177, 186, 194, 200, 205, 211, 217, 223, 229, 234, 243, 252, 265, 267, 269, 272, 275, 282, 293, 299, 310, 316, 326, 336, 346, 356, 366, 376, 389, 401, 414, 427, 440, 453, 462, 472, 476, 483, 491, 498, 506, 510, 515, 519, 528, 538, 542, 546}

func (i Operator) String() string {
	if i >= Operator(len(_Operator_index)-1) {
		return "Operator(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Operator_name[_Operator_index[i]:_Operator_index[i+1]]
}