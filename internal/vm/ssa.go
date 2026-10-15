package vm

// SSA-IR for the register bytecode: basic blocks, lowered SSA instructions,
// SSA values/phis, exception edges, liveness and the interference graph.
//
// This module is deliberately independent of register coloring and code
// emission (those live in regalloc.go). Optimization passes can consume or
// rewrite the IR through the OptimizeSSA hook below without touching the
// allocator.
//
// Construction order:
//   1. CFG of basic blocks (jumps/branches/fallthrough).      buildBlocks
//   2. Exception edges from protected code to handlers.       buildExceptionEdges
//   3. SSA construction (Braun et al.), phi nodes at joins.   buildSSA
//   4. Liveness over SSA values.                              computeLiveness
//   5. Interference graph.                                    buildInterference

import (
	"sort"
)

// ---------------------------------------------------------------------------
// Operand roles
// ---------------------------------------------------------------------------

type raRole uint8

const (
	rNone raRole = iota
	rUse
	rDef
	rUseDef // read and written in place (e.g. INDEX_SET)
)

var opArole, opBrole, opCrole [numOpcodes]raRole

func init() {
	s := func(o Opcode, a, b, c raRole) {
		opArole[o], opBrole[o], opCrole[o] = a, b, c
	}
	s(OpHalt, rNone, rNone, rNone)
	s(OpLoadK, rDef, rNone, rNone)
	s(OpMove, rDef, rUse, rNone)
	s(OpStoreVar, rDef, rUse, rNone)
	s(OpNull, rDef, rNone, rNone)
	s(OpTrue, rDef, rNone, rNone)
	s(OpFalse, rDef, rNone, rNone)
	s(OpNeg, rDef, rUse, rNone)
	s(OpNot, rDef, rUse, rNone)
	s(OpAdd, rDef, rUse, rUse)
	s(OpSub, rDef, rUse, rUse)
	s(OpMul, rDef, rUse, rUse)
	s(OpDiv, rDef, rUse, rUse)
	s(OpMod, rDef, rUse, rUse)
	s(OpPow, rDef, rUse, rUse)
	s(OpAddK, rDef, rUse, rNone)
	s(OpSubK, rDef, rUse, rNone)
	s(OpMulK, rDef, rUse, rNone)
	s(OpDivK, rDef, rUse, rNone)
	s(OpConcat, rDef, rUse, rUse)
	// OpConcatN читает окно регистров B..B+C-1 (см. window) и пишет A.
	s(OpConcatN, rDef, rNone, rNone)
	s(OpConcatApp, rDef, rUse, rUse)
	s(OpEqual, rDef, rUse, rUse)
	s(OpNotEqual, rDef, rUse, rUse)
	s(OpIdentical, rDef, rUse, rUse)
	s(OpNotIdentical, rDef, rUse, rUse)
	s(OpLess, rDef, rUse, rUse)
	s(OpLessOrEqual, rDef, rUse, rUse)
	s(OpGreater, rDef, rUse, rUse)
	s(OpGreaterEqual, rDef, rUse, rUse)
	s(OpLessK, rDef, rUse, rNone)
	s(OpLessOrEqualK, rDef, rUse, rNone)
	s(OpGreaterK, rDef, rUse, rNone)
	s(OpGreaterEqualK, rDef, rUse, rNone)
	s(OpEqualK, rDef, rUse, rNone)
	s(OpAddInt, rDef, rUse, rUse)
	s(OpAddFloat, rDef, rUse, rUse)
	s(OpSubInt, rDef, rUse, rUse)
	s(OpSubFloat, rDef, rUse, rUse)
	s(OpMulInt, rDef, rUse, rUse)
	s(OpMulFloat, rDef, rUse, rUse)
	s(OpDivInt, rDef, rUse, rUse)
	s(OpDivFloat, rDef, rUse, rUse)
	s(OpAddIntK, rDef, rUse, rNone)
	s(OpAddFloatK, rDef, rUse, rNone)
	s(OpSubIntK, rDef, rUse, rNone)
	s(OpMulIntK, rDef, rUse, rNone)
	s(OpLessInt, rDef, rUse, rUse)
	s(OpLessFloat, rDef, rUse, rUse)
	s(OpLessOrEqualInt, rDef, rUse, rUse)
	s(OpLessOrEqualFloat, rDef, rUse, rUse)
	s(OpGreaterInt, rDef, rUse, rUse)
	s(OpGreaterFloat, rDef, rUse, rUse)
	s(OpGreaterEqualInt, rDef, rUse, rUse)
	s(OpGreaterEqualFloat, rDef, rUse, rUse)
	s(OpEqualInt, rDef, rUse, rUse)
	s(OpEqualFloat, rDef, rUse, rUse)
	s(OpIdenticalInt, rDef, rUse, rUse)
	s(OpIdenticalFloat, rDef, rUse, rUse)
	s(OpLessIntK, rDef, rUse, rNone)
	s(OpLessFloatK, rDef, rUse, rNone)
	s(OpLessOrEqualIntK, rDef, rUse, rNone)
	s(OpGreaterIntK, rDef, rUse, rNone)
	s(OpGreaterEqualIntK, rDef, rUse, rNone)
	s(OpEqualIntK, rDef, rUse, rNone)
	s(OpEcho, rUse, rNone, rNone)
	s(OpJump, rNone, rNone, rNone)
	s(OpJumpIfFalse, rUse, rNone, rNone)
	s(OpJumpIfTrue, rUse, rNone, rNone)
	s(OpCall, rDef, rNone, rNone)
	s(OpRet, rUse, rNone, rNone)
	s(OpArray, rDef, rNone, rNone)
	s(OpIndex, rDef, rUse, rUse)
	s(OpIndexSet, rUseDef, rUse, rUse)
	s(OpIndexSetOverload, rUseDef, rUse, rUse)
	s(OpAppend, rUseDef, rUse, rNone)
	s(OpAppendCompound, rUseDef, rUseDef, rNone)
	s(OpLen, rDef, rUse, rNone)
	s(OpUnset, rUseDef, rUse, rNone)
	s(OpNew, rDef, rNone, rNone)
	s(OpPropGet, rDef, rUse, rNone)
	s(OpPropGetRW, rDef, rUse, rNone)
	s(OpPropGetRWRead, rDef, rUse, rNone)
	s(OpPropGetRWInterm, rDef, rUse, rNone)
	s(OpPropGetRWArr, rDef, rUse, rNone)
	s(OpPropGetRWArrDyn, rDef, rUse, rUse)
	s(OpPropGetSafe, rDef, rUse, rNone)
	s(OpPropGetSafeDyn, rDef, rUse, rUse)
	s(OpPropGetRWDyn, rDef, rUse, rUse)
	s(OpPropSet, rUse, rUse, rNone)
	s(OpMethodCall, rDef, rNone, rNone)
	s(OpThis, rDef, rNone, rNone)
	s(OpInstanceOf, rDef, rUse, rNone)
	s(OpClassOf, rDef, rUse, rNone)
	s(OpClone, rDef, rUse, rNone)
	s(OpStaticGet, rDef, rNone, rNone)
	s(OpStaticGetRW, rDef, rNone, rNone)
	s(OpStaticSet, rUse, rNone, rNone)
	s(OpConstGet, rDef, rNone, rNone)
	s(OpClassName, rDef, rNone, rNone)
	s(OpLogicalAnd, rDef, rUse, rUse)
	s(OpGlobalGet, rDef, rNone, rNone)
	s(OpGlobalSet, rUseDef, rNone, rNone)
	s(OpGlobalIsset, rDef, rNone, rNone)
	s(OpStaticCall, rDef, rNone, rNone)
	s(OpPushTry, rNone, rNone, rNone)
	s(OpPopTry, rNone, rNone, rNone)
	s(OpThrow, rUse, rNone, rNone)
	s(OpEnterFinally, rNone, rNone, rNone)
	s(OpFinallyEnd, rNone, rNone, rNone)
	s(OpNewRef, rDef, rUse, rNone)
	s(OpRefGet, rDef, rUse, rNone)
	s(OpRefSet, rUseDef, rUse, rNone)
	s(OpRefBind, rDef, rUse, rNone)
	s(OpRefEnsure, rUseDef, rNone, rNone)
	s(OpGlobalRefGet, rDef, rNone, rNone)
	s(OpGlobalRefSet, rUse, rNone, rNone)
	s(OpGlobalRefAddr, rDef, rNone, rNone)
	s(OpGlobalRefBind, rUse, rNone, rNone)
	s(OpCast, rDef, rUse, rNone)
	s(OpCallDyn, rDef, rNone, rNone)
	s(OpNewDyn, rDef, rNone, rNone)
	s(OpClosure, rDef, rNone, rNone)
	s(OpGlobalAppend, rUse, rNone, rNone)
	s(OpGlobalIndexSet, rUse, rUse, rNone)
	s(OpCallArr, rDef, rUse, rNone)
	s(OpGlobalGetDyn, rDef, rNone, rUse)
	s(OpGlobalSetDyn, rUseDef, rNone, rUse)
	s(OpPropGetDyn, rDef, rUse, rUse)
	s(OpPropSetDyn, rUse, rUse, rUse)
	s(OpPropIsset, rDef, rUse, rNone)
	s(OpPropIssetDyn, rDef, rUse, rUse)
	s(OpPropUnset, rUse, rNone, rNone)
	s(OpPropUnsetDyn, rUse, rNone, rUse)
	s(OpMethodCallDyn, rDef, rNone, rNone)
	s(OpStaticGetDyn, rDef, rNone, rUse)
	s(OpStaticGetRWDyn, rDef, rNone, rUse)
	s(OpStaticSetDyn, rUse, rNone, rUse)
	s(OpStaticCallDyn, rDef, rNone, rNone)
	s(OpBitAnd, rDef, rUse, rUse)
	s(OpBitOr, rDef, rUse, rUse)
	s(OpBitXor, rDef, rUse, rUse)
	s(OpBitNot, rDef, rUse, rNone)
	s(OpShl, rDef, rUse, rUse)
	s(OpShr, rDef, rUse, rUse)
	s(OpIsObject, rDef, rUse, rNone)
	s(OpIterInit, rDef, rUse, rNone)
	s(OpIterValid, rDef, rUse, rNone)
	s(OpIterKey, rDef, rUse, rNone)
	s(OpIterCurrent, rDef, rUse, rNone)
	s(OpIterNext, rNone, rUseDef, rNone)
	s(OpIndexIsset, rDef, rUse, rUse)
	s(OpIndexSafe, rDef, rUse, rUse)
	s(OpIndexSafeWarn, rDef, rUse, rUse)
	s(OpIndexSetRes, rUseDef, rUse, rUseDef)
	s(OpIndexRW, rDef, rUse, rUse)
	s(OpIndexAppendRaw, rDef, rUse, rUse)
	s(OpSpaceship, rDef, rUse, rUse)
	s(OpExit, rUse, rNone, rNone)
	s(OpSuppressOn, rNone, rNone, rNone)
	s(OpSuppressOff, rNone, rNone, rNone)
	s(OpGlobalUnset, rNone, rNone, rNone)
	s(OpYield, rUse, rUse, rDef)
	s(OpJumpIfLessIntK, rNone, rUse, rNone)
	s(OpAddIntImm, rDef, rUse, rNone)
	s(OpSubIntImm, rDef, rUse, rNone)
	s(OpMulIntImm, rDef, rUse, rNone)
	s(OpLessIntImm, rDef, rUse, rNone)
	s(OpLessOrEqualIntImm, rDef, rUse, rNone)
	s(OpGreaterIntImm, rDef, rUse, rNone)
	s(OpGreaterEqualIntImm, rDef, rUse, rNone)
	s(OpEqualIntImm, rDef, rUse, rNone)
	s(OpJumpIfLessIntImm, rNone, rUse, rNone)
	s(OpIndexRef, rDef, rUseDef, rUse)
	s(OpPropRef, rDef, rUse, rNone)
	s(OpPropSetOverload, rUse, rUse, rNone)
	s(OpPropSetOverloadDyn, rUse, rUse, rUse)
	s(OpPropRefDyn, rDef, rUse, rUse)
	s(OpPropUnrefDyn, rUse, rUse, rNone)
	s(OpIterCurrentRef, rDef, rUse, rNone)
	s(OpPropSetRef, rUse, rUseDef, rNone)
	s(OpPropSetRefDyn, rUse, rUseDef, rUse)
	s(OpStaticSetRef, rUseDef, rNone, rNone)
	s(OpStaticSetRefDyn, rUseDef, rNone, rUse)
	s(OpStaticRef, rDef, rNone, rNone)
	s(OpStaticRefDyn, rDef, rNone, rUse)
	s(OpStaticIsset, rDef, rNone, rNone)
	s(OpDeclareClass, rNone, rNone, rNone)
	s(OpIncDec, rDef, rUse, rNone)
	// Базы записи через ячейку-ссылку: без пометки разделённости.
	s(OpRefGetRaw, rDef, rUse, rNone)
	s(OpRefSetRaw, rUseDef, rUse, rNone)
	s(OpIndexRaw, rDef, rUse, rUse)
	s(OpIndexUnsetRaw, rDef, rUse, rUse)
	s(OpPropGetIncDec, rDef, rUse, rNone)
	s(OpPropGetIncDecDyn, rDef, rUse, rUse)
	s(OpPropGetCompound, rDef, rUse, rNone)
	s(OpPropGetCompoundDyn, rDef, rUse, rUse)
	s(OpIndexRawWrite, rDef, rUse, rUse)
	s(OpIndexSetRaw, rUseDef, rUse, rUse)
	s(OpIndexSetOverloadRaw, rUseDef, rUse, rUse)
	s(OpListFetchRef, rUseDef, rUse, rUse)
}

// isTerminator reports whether op ends a basic block.
func isTerminator(op Opcode) bool {
	switch op {
	case OpJump, OpRet, OpHalt, OpExit, OpJumpIfFalse, OpJumpIfTrue, OpJumpIfLessIntK, OpJumpIfLessIntImm:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Basic blocks
// ---------------------------------------------------------------------------

type ssaBlock struct {
	start, end int
	succs      []int
	preds      []int
	phis       []*ssaPhi
}

type ssaPhi struct {
	res  int
	vreg uint8
	// operands indexed by pred block
	ops map[int]int
}

// ---------------------------------------------------------------------------
// Lowered (SSA) instruction
// ---------------------------------------------------------------------------

type ssaOp struct {
	op   Opcode
	a, b int // input value ids for A/B register operands (-1 = none)
	c    int // input value id for C register operand (-1 = none)
	da   int // output value id for A (-1 = none)
	db   int
	dc   int
	// raw (non-value) encodings, carried through to codegen
	aRaw, bRaw uint8
	cRaw       int32

	// window: consecutive registers starting at winBaseSlot
	win     []int
	winSlot byte // 'B' or 'A' (base operand field)

	// for method/static dynamic calls: the name register value
	nameVal int
}

type ssaValue struct {
	phi      bool
	block    int
	inst     int
	reg      uint8 // original register (entry values)
	precolor int
}

// excSeed — запись о catch-ветке, читающей объект исключения в начале блока.
type excSeed struct {
	reg uint8
	ti  int
	ci  int
}

// ---------------------------------------------------------------------------
// SSA graph
// ---------------------------------------------------------------------------

// ssaGraph holds the SSA-IR of a single bytecode function together with the
// metadata needed to build it (input code, jumps, callee env) and the results
// of liveness/interference analysis. Coloring state lives in regAlloc, which
// embeds a *ssaGraph.
type ssaGraph struct {
	code  []Instruction
	lines []int32
	jumps []int32

	pinned int
	env    *RegAllocEnv

	// liveParams — число ведущих регистров, которые обязаны оставаться
	// «живыми» на всём протяжении функции (параметры при keepParamsLive).
	liveParams int

	// excSeed[block] — записи catch-обработчиков, для которых в начале блока
	// нужно создать значение под объект исключения. seedVal сопоставляет
	// (try, catch) значению; excObjVal[handlerBlock] — SSA-значения, в
	// регистры которых VM кладёт объект исключения перед прыжком.
	excSeed   map[int][]excSeed
	seedVal   map[[2]int]int
	excObjVal map[int][]int

	// tries — метаданные try-регионов исходного чанка.
	tries []TryInfo
	// excSuccs/excPreds/excEdge — рёбра исключений между блоками.
	excSuccs [][]int
	excPreds [][]int
	excEdge  map[[2]int]bool

	nBlocks int
	blocks  []*ssaBlock
	blockAt map[int]int // block start instruction -> block id
	blockOf []int       // instruction -> block id
	order   []int       // RPO block order

	vops []*ssaOp

	vals    []ssaValue
	cur     []map[uint8]int
	sealed  []bool
	alias   []int
	phiByID map[int]*ssaPhi

	liveIn  []bitset
	liveOut []bitset
	edges   [][2]int

	// entryNullMask — регистры (цвета), к которым привязаны «входные» SSA-
	// значения без определения: неинициализированные чтения и входные
	// значения pinned-регистров. К началу исполнения чанка такие регистры
	// обязаны содержать Null; AllocateRegisters превращает маску в список
	// Chunk.entryNull (без параметров/захватов), зануляемый прологом кадра.
	entryNullMask [liveMaskStride]uint64
}

// optimizeSSAHook — необязательный хук для SSA-проходов оптимизации. nil по
// умолчанию. Вызывается после построения SSA и до анализа живости, поэтому
// проход обязан сам пересобрать значение/phi-таблицы, если он их меняет.
var optimizeSSAHook func(*ssaGraph)

// OptimizeSSA применяет зарегистрированный SSA-проход оптимизации (если он
// есть). Точка расширения: будущие проходы (copy-propagation, удаление
// мёртвых значений и т.п.) работают по IR, не задевая аллокатор.
func (g *ssaGraph) OptimizeSSA() {
	if optimizeSSAHook != nil {
		optimizeSSAHook(g)
	}
}

// ---------------------------------------------------------------------------
// CFG
// ---------------------------------------------------------------------------

func (ra *ssaGraph) target(c int32) (int, bool) {
	t := int(c)
	if t < 0 || t > len(ra.code) {
		return 0, false
	}
	return t, true
}

func (ra *ssaGraph) buildBlocks() bool {
	n := len(ra.code)
	if n == 0 {
		return false
	}
	isStart := make([]bool, n+1)
	isStart[0] = true
	addStart := func(t int) bool {
		if t < 0 || t > n {
			return false
		}
		if t < n {
			isStart[t] = true
		}
		return true
	}
	for i, in := range ra.code {
		switch in.Op {
		case OpJump:
			t, ok := ra.target(in.C)
			if !ok || !addStart(t) {
				return false
			}
		case OpJumpIfFalse, OpJumpIfTrue:
			t, ok := ra.target(in.C)
			if !ok || !addStart(t) {
				return false
			}
		case OpJumpIfLessIntK, OpJumpIfLessIntImm:
			if int(in.A) >= len(ra.jumps) {
				return false
			}
			t, ok := ra.target(ra.jumps[in.A])
			if !ok || !addStart(t) {
				return false
			}
		}
		if isTerminator(in.Op) && i+1 < n {
			isStart[i+1] = true
		}
	}

	// build blocks
	ra.blockOf = make([]int, n)
	var starts []int
	for i := 0; i < n; i++ {
		if isStart[i] {
			starts = append(starts, i)
		}
	}
	for bi, s := range starts {
		ra.blockAt[s] = bi
	}
	ra.nBlocks = len(starts)
	ra.blocks = make([]*ssaBlock, ra.nBlocks)
	for bi, s := range starts {
		e := n
		if bi+1 < len(starts) {
			e = starts[bi+1]
		}
		ra.blocks[bi] = &ssaBlock{start: s, end: e}
		for i := s; i < e; i++ {
			ra.blockOf[i] = bi
		}
	}

	succ := func(bi int) ([]int, bool) {
		b := ra.blocks[bi]
		last := ra.code[b.end-1]
		var out []int
		add := func(t int) bool {
			id, ok := ra.blockAt[t]
			if !ok {
				return false
			}
			out = append(out, id)
			return true
		}
		switch last.Op {
		case OpJump:
			t, ok := ra.target(last.C)
			if !ok || !add(t) {
				return nil, false
			}
		case OpJumpIfFalse, OpJumpIfTrue:
			t, ok := ra.target(last.C)
			if !ok || !add(t) {
				return nil, false
			}
			if b.end < n {
				if !add(b.end) {
					return nil, false
				}
			}
		case OpJumpIfLessIntK, OpJumpIfLessIntImm:
			if int(last.A) >= len(ra.jumps) {
				return nil, false
			}
			t, ok := ra.target(ra.jumps[last.A])
			if !ok || !add(t) {
				return nil, false
			}
			if b.end < n {
				if !add(b.end) {
					return nil, false
				}
			}
		case OpRet, OpHalt, OpExit:
			// no successors
		default:
			if b.end < n {
				if !add(b.end) {
					return nil, false
				}
			}
		}
		return out, true
	}

	for bi := 0; bi < ra.nBlocks; bi++ {
		s, ok := succ(bi)
		if !ok {
			return false
		}
		ra.blocks[bi].succs = s
		for _, sid := range s {
			ra.blocks[sid].preds = append(ra.blocks[sid].preds, bi)
		}
	}

	// RPO
	visited := make([]bool, ra.nBlocks)
	var dfs func(int)
	dfs = func(bi int) {
		visited[bi] = true
		for _, s := range ra.blocks[bi].succs {
			if !visited[s] {
				dfs(s)
			}
		}
		ra.order = append(ra.order, bi)
	}
	dfs(0)
	// reverse
	for i, j := 0, len(ra.order)-1; i < j; i, j = i+1, j-1 {
		ra.order[i], ra.order[j] = ra.order[j], ra.order[i]
	}
	// unreachable blocks appended
	for bi := 0; bi < ra.nBlocks; bi++ {
		if !visited[bi] {
			ra.order = append(ra.order, bi)
			visited[bi] = true
		}
	}
	return true
}

// blocksInRange возвращает блоки, пересекающиеся с диапазоном инструкций
// [lo, hi). Блоки разбивают [0,n) непрерывно, поэтому достаточно шагать от
// начала lo к концу каждого блока.
func (ra *ssaGraph) blocksInRange(lo, hi int) []int {
	if lo < 0 {
		lo = 0
	}
	if hi > len(ra.code) {
		hi = len(ra.code)
	}
	var res []int
	for lo < hi {
		bi := ra.blockOf[lo]
		res = append(res, bi)
		lo = ra.blocks[bi].end
	}
	return res
}

// buildExceptionEdges добавляет в CFG (в preds, но не в succs — их
// обрабатывает только liveness) рёбра исключений: из защищённого кода
// try-региона в начала catch-веток и finally. Это делает liveness корректным
// (значения, читаемые обработчиком, живы на всём протяжении try), а phi в
// началах обработчиков — связанными с попытками.
func (ra *ssaGraph) buildExceptionEdges() bool {
	ra.excSuccs = make([][]int, ra.nBlocks)
	ra.excPreds = make([][]int, ra.nBlocks)
	ra.excEdge = map[[2]int]bool{}
	ra.excSeed = map[int][]excSeed{}
	ra.seedVal = map[[2]int]int{}
	ra.excObjVal = map[int][]int{}
	if len(ra.tries) == 0 {
		return true
	}
	n := len(ra.code)

	pushPC := make([]int, len(ra.tries))
	for i := range pushPC {
		pushPC[i] = -1
	}
	for i, in := range ra.code {
		if in.Op != OpPushTry {
			continue
		}
		idx := int(in.C)
		if idx < 0 || idx >= len(ra.tries) || pushPC[idx] != -1 {
			return false
		}
		pushPC[idx] = i
	}

	addExc := func(src, handler int) {
		if src < 0 || handler < 0 || src == handler {
			return
		}
		key := [2]int{src, handler}
		if ra.excEdge[key] {
			return
		}
		ra.excEdge[key] = true
		ra.excSuccs[src] = append(ra.excSuccs[src], handler)
		ra.excPreds[handler] = append(ra.excPreds[handler], src)
		ra.blocks[handler].preds = append(ra.blocks[handler].preds, src)
	}

	blockAtPC := func(pc int32) (int, bool) {
		p := int(pc)
		if p < 0 || p >= n {
			return 0, false
		}
		b, ok := ra.blockAt[p]
		return b, ok
	}

	for ti, t := range ra.tries {
		p := pushPC[ti]
		if p < 0 {
			return false
		}
		var handlers []int
		minPC := n
		for ci, ce := range t.Catches {
			hb, ok := blockAtPC(ce.PC)
			if !ok {
				return false
			}
			handlers = append(handlers, hb)
			if int(ce.PC) < minPC {
				minPC = int(ce.PC)
			}
			if ce.Var != 0xFF {
				dup := false
				for _, s := range ra.excSeed[hb] {
					if s.reg == ce.Var {
						dup = true
						break
					}
				}
				if !dup {
					ra.excSeed[hb] = append(ra.excSeed[hb], excSeed{reg: ce.Var, ti: ti, ci: ci})
				}
			}
		}
		finB := -1
		if t.FinallyPC >= 0 {
			hb, ok := blockAtPC(t.FinallyPC)
			if !ok {
				return false
			}
			finB = hb
			handlers = append(handlers, hb)
			if int(t.FinallyPC) < minPC {
				minPC = int(t.FinallyPC)
			}
		}
		if len(handlers) == 0 {
			return false
		}
		// Тело try — от инструкции после PUSH_TRY до первого обработчика.
		// Консервативно: из всех его блоков исключение может уйти в любой
		// обработчик (catch или finally).
		for _, sb := range ra.blocksInRange(p+1, minPC) {
			for _, hb := range handlers {
				addExc(sb, hb)
			}
		}
		// Тела catch-веток также могут выбросить исключение — оно уйдёт в
		// finally (если он есть).
		if finB >= 0 {
			for ci, ce := range t.Catches {
				lo := int(ce.PC)
				hi := n
				if ci+1 < len(t.Catches) {
					hi = int(t.Catches[ci+1].PC)
				} else if t.FinallyPC >= 0 {
					hi = int(t.FinallyPC)
				} else if t.EndPC >= 0 {
					hi = int(t.EndPC)
				}
				for _, cb := range ra.blocksInRange(lo, hi) {
					addExc(cb, finB)
				}
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// SSA construction (Braun et al.)
// ---------------------------------------------------------------------------

func (ra *ssaGraph) resolve(id int) int {
	for ra.alias[id] != id {
		ra.alias[id] = ra.alias[ra.alias[id]]
		id = ra.alias[id]
	}
	return id
}

func (ra *ssaGraph) newVal(phi bool, block, inst int, reg uint8, precolor int) int {
	// Значение без определения, привязанное к конкретному регистру
	// (неинициализированное чтение, входное значение pinned-регистра, phi с
	// закреплённым цветом): регистр обязан быть Null на входе в кадр.
	if inst < 0 && precolor >= 0 {
		ra.entryNullMask[precolor>>6] |= 1 << uint(precolor&63)
	}
	ra.vals = append(ra.vals, ssaValue{phi: phi, block: block, inst: inst, reg: reg, precolor: precolor})
	ra.alias = append(ra.alias, len(ra.vals)-1)
	return len(ra.vals) - 1
}

func (ra *ssaGraph) newPhi(block int, vreg uint8) int {
	// Phi-результат переменной, чьи определения закреплены за регистром
	// (named/параметрические регистры при keepParamsLive), тоже обязан
	// остаться в этом регистре: иначе out-of-SSA вставит копии на входе в
	// цикл и на обратном ребре (loop-carried значения), хотя все операнды
	// phi уже предокрашены в тот же цвет. Для переименовываемых регистров
	// defPrecolor даёт -1, и phi остаётся свободным.
	id := ra.newVal(true, block, -1, vreg, ra.defPrecolor(vreg))
	ph := &ssaPhi{res: id, vreg: vreg, ops: map[int]int{}}
	ra.blocks[block].phis = append(ra.blocks[block].phis, ph)
	if ra.phiByID == nil {
		ra.phiByID = map[int]*ssaPhi{}
	}
	ra.phiByID[id] = ph
	return id
}

func (ra *ssaGraph) addPhiOperand(ph int, pred int, op int) {
	op = ra.resolve(op)
	if p := ra.phiByID[ph]; p != nil {
		p.ops[pred] = op
	}
}

func (ra *ssaGraph) phiByRes(id int) *ssaPhi {
	return ra.phiByID[id]
}

func (ra *ssaGraph) tryRemoveTrivialPhi(id int) int {
	id = ra.resolve(id)
	ph := ra.phiByRes(id)
	if ph == nil {
		return id
	}
	same := -1
	for _, o := range ph.ops {
		o = ra.resolve(o)
		if o == id {
			continue
		}
		if same == -1 {
			same = o
		} else if same != o {
			return id
		}
	}
	if same == -1 {
		return id
	}
	ra.alias[id] = same
	return ra.resolve(same)
}

func (ra *ssaGraph) readVar(vreg uint8, block int) int {
	if v, ok := ra.cur[block][vreg]; ok {
		return ra.resolve(v)
	}
	b := ra.blocks[block]
	// Блок входа функции (block 0) имеет неявного предшественника — точку
	// входа. Если в него ведёт обратное ребро (цикл начинается первым
	// оператором), то обычный разбор построил бы phi только из реальных
	// предшественников и тривиально свёл бы его к значению с обратного ребра,
	// потеряв входное значение pinned-регистра (параметра или by-ref ячейки).
	// Для pinned-регистров добавляем входное значение отдельным операндом phi
	// (ключ -1), который обязан делить цвет с результатом (перемещение на
	// неявное ребро вставить некуда).
	if block == 0 && len(b.preds) > 0 && ra.entryPrecolor(vreg) >= 0 {
		return ra.readEntryVar(vreg, block)
	}
	if ra.sealed[block] {
		if len(b.preds) == 0 {
			pc := ra.entryPrecolor(vreg)
			if pc < 0 {
				// Чтение регистра, которому нигде не было определения
				// (неинициализированная переменная): значение обязано
				// остаться в своём регистре — при входе в кадр он занулён в
				// NULL. Иначе аллокатор может слить его с живым регистром
				// (например, параметром), и чтение вернёт чужое значение.
				pc = int(vreg)
			}
			v := ra.newVal(false, block, -1, vreg, pc)
			ra.cur[block][vreg] = v
			return v
		}
		if len(b.preds) == 1 {
			v := ra.readVar(vreg, b.preds[0])
			ra.cur[block][vreg] = v
			return v
		}
		ph := ra.newPhi(block, vreg)
		ra.cur[block][vreg] = ph
		for _, p := range b.preds {
			ra.addPhiOperand(ph, p, ra.readVar(vreg, p))
		}
		v := ra.tryRemoveTrivialPhi(ph)
		ra.cur[block][vreg] = v
		return v
	}
	ph := ra.newPhi(block, vreg)
	ra.cur[block][vreg] = ph
	return ph
}

// readEntryVar строит значение pinned-регистра vreg в блоке входа функции.
// Помимо phi по реальным предшественникам добавляется входное значение
// (ключ -1): при входе в функцию параметр/ячейка уже лежит в своём регистре,
// но кода на неявном ребре нет, поэтому значение должно делить цвет с phi.
func (ra *ssaGraph) readEntryVar(vreg uint8, block int) int {
	b := ra.blocks[block]
	ph := ra.newPhi(block, vreg)
	ra.cur[block][vreg] = ph
	ev := ra.newVal(false, block, -1, vreg, ra.entryPrecolor(vreg))
	ra.addPhiOperand(ph, -1, ev)
	if ra.sealed[block] {
		for _, p := range b.preds {
			ra.addPhiOperand(ph, p, ra.readVar(vreg, p))
		}
		v := ra.tryRemoveTrivialPhi(ph)
		ra.cur[block][vreg] = v
		return v
	}
	return ph
}

func (ra *ssaGraph) sealBlock(block int) {
	if ra.sealed[block] {
		return
	}
	ra.sealed[block] = true
	b := ra.blocks[block]
	for _, ph := range b.phis {
		for _, p := range b.preds {
			if _, ok := ph.ops[p]; !ok {
				ph.ops[p] = ra.resolve(ra.readVar(ph.vreg, p))
			}
		}
		v := ra.tryRemoveTrivialPhi(ph.res)
		ra.cur[block][ph.vreg] = v
	}
}

func (ra *ssaGraph) newValue(block, inst int, reg uint8) int {
	return ra.newVal(false, block, inst, reg, ra.defPrecolor(reg))
}

// entryPrecolor возвращает цвет-регистр, к которому надо привязать «входное»
// значение регистра reg (нет определения в байткоде): параметры и захваты.
// -1 — обычный регистр, значение которого можно переименовать.
func (ra *ssaGraph) entryPrecolor(reg uint8) int {
	if int(reg) < ra.pinned {
		return int(reg)
	}
	return -1
}

// defPrecolor — то же для определения значения. Регистры параметров при
// keepParamsLive не переименовываются.
func (ra *ssaGraph) defPrecolor(reg uint8) int {
	if int(reg) < ra.liveParams {
		return int(reg)
	}
	return -1
}

func (ra *ssaGraph) buildSSA() bool {
	ra.vops = make([]*ssaOp, len(ra.code))
	ra.cur = make([]map[uint8]int, ra.nBlocks)
	for i := range ra.cur {
		ra.cur[i] = map[uint8]int{}
	}
	// Значения ExcReg в началах catch-блоков: VM кладёт объект исключения в
	// регистр, который выберет аллокатор; соответствующий TryCatch.Var будет
	// обновлён после раскраски. Значение создаётся заранее, чтобы чтение в
	// теле catch не успело породить phi.
	//
	// Блоки обходятся в отсортированном порядке: id SSA-значений определяются
	// порядком создания, а он не должен зависеть от итерации map (иначе
	// раскладка регистров недетерминирована).
	seedBlocks := make([]int, 0, len(ra.excSeed))
	for bi := range ra.excSeed {
		seedBlocks = append(seedBlocks, bi)
	}
	sort.Ints(seedBlocks)
	for _, bi := range seedBlocks {
		seeds := ra.excSeed[bi]
		for _, s := range seeds {
			if _, ok := ra.cur[bi][s.reg]; ok {
				continue
			}
			v := ra.newVal(false, bi, -1, s.reg, -1)
			ra.cur[bi][s.reg] = v
			ra.seedVal[[2]int{s.ti, s.ci}] = v
			ra.excObjVal[bi] = append(ra.excObjVal[bi], v)
		}
	}
	ra.sealed = make([]bool, ra.nBlocks)

	allPredsProcessed := func(bi int, processed []bool) bool {
		for _, p := range ra.blocks[bi].preds {
			if !processed[p] {
				return false
			}
		}
		return true
	}

	processed := make([]bool, ra.nBlocks)
	for _, bi := range ra.order {
		if allPredsProcessed(bi, processed) {
			ra.sealBlock(bi)
		}
		if !ra.lowerBlock(bi) {
			return false
		}
		processed[bi] = true
		for _, s := range ra.blocks[bi].succs {
			if !processed[s] && allPredsProcessed(s, processed) {
				ra.sealBlock(s)
			}
		}
	}
	for bi := 0; bi < ra.nBlocks; bi++ {
		if !ra.sealed[bi] {
			ra.sealBlock(bi)
		}
	}
	return true
}

func (ra *ssaGraph) lowerBlock(bi int) bool {
	b := ra.blocks[bi]
	for i := b.start; i < b.end; i++ {
		vo, ok := ra.lower(i, bi)
		if !ok {
			return false
		}
		ra.vops[i] = vo
	}
	return true
}

func (ra *ssaGraph) lower(i, bi int) (*ssaOp, bool) {
	in := ra.code[i]
	vo := &ssaOp{op: in.Op, a: -1, b: -1, c: -1, da: -1, db: -1, dc: -1,
		aRaw: in.A, bRaw: in.B, cRaw: in.C, nameVal: -1}
	read := func(r uint8) int { return ra.readVar(r, bi) }

	// 1. reads (all uses, including call windows, before any def is applied)
	switch opArole[in.Op] {
	case rUse, rUseDef:
		vo.a = read(in.A)
	}
	switch opBrole[in.Op] {
	case rUse, rUseDef:
		if !(in.Op == OpYield && in.B == YieldNoKey) {
			vo.b = read(in.B)
		}
	}
	switch opCrole[in.Op] {
	case rUse, rUseDef:
		vo.c = read(uint8(in.C))
	}

	base, width, slot := ra.window(in)
	if width < 0 || base < 0 || base+width > 256 {
		return nil, false
	}
	if width > 0 {
		vo.winSlot = slot
		for k := 0; k < width; k++ {
			vo.win = append(vo.win, read(uint8(base+k)))
		}
	} else if in.Op == OpNew {
		// VM пишет результат в A ДО чтения ctor-аргументов из B; фиксируем B
		// как use, чтобы результат не совпал с ним.
		vo.b = read(in.B)
	}
	switch in.Op {
	case OpMethodCallDyn, OpStaticCallDyn:
		vo.nameVal = read(uint8(in.C & 0xFF))
	}

	// 2. defs (register the new value as the current definition)
	setCur := func(r uint8, v int) { ra.cur[bi][r] = v }
	switch opArole[in.Op] {
	case rDef:
		vo.da = ra.newValue(bi, i, in.A)
		setCur(in.A, vo.da)
	case rUseDef:
		vo.da = ra.newValue(bi, i, in.A)
		setCur(in.A, vo.da)
	}
	switch opBrole[in.Op] {
	case rDef:
		vo.db = ra.newValue(bi, i, in.B)
		setCur(in.B, vo.db)
	case rUseDef:
		vo.db = ra.newValue(bi, i, in.B)
		setCur(in.B, vo.db)
	}
	switch opCrole[in.Op] {
	case rDef:
		r := uint8(in.C & 0xFF)
		vo.dc = ra.newValue(bi, i, r)
		setCur(r, vo.dc)
	case rUseDef:
		r := uint8(in.C)
		vo.dc = ra.newValue(bi, i, r)
		setCur(r, vo.dc)
	}
	return vo, true
}

func (ra *ssaGraph) window(in Instruction) (base, width int, slot byte) {
	switch in.Op {
	case OpCall:
		packed := uint32(in.C)
		raw := int(packed >> 24)
		if raw > 0 {
			// Точное число фактически переданных аргументов (компилятор
			// упаковывает len(args)+1). Окно вызова должно покрывать именно
			// их, а не объявленную арность: при вызове с лишними аргументами
			// они всё равно передаются в VM (для func_get_args()).
			width = raw - 1
		} else {
			fi := int(packed & 0x7FFFFF)
			na := ra.env.FuncNumArgs(fi)
			if na >= 0 {
				width = na
			} else {
				width = -1 // legacy "pass all" - unsupported
			}
		}
		return int(in.B), width, 'B'
	case OpNew:
		raw := int(in.C >> 16)
		if raw == 0 {
			return int(in.B), 0, 'B' // легаси: арность берётся у конструктора
		}
		return int(in.B), raw - 1, 'B'
	case OpNewDyn, OpCallDyn:
		return int(in.B), int(in.C) + 1, 'B'
	case OpMethodCall, OpMethodCallDyn:
		return int(in.B), int(in.C>>16) + 1, 'B'
	case OpStaticCall, OpStaticCallDyn:
		return int(in.A) + 1, int(in.C >> 16), 'A'
	case OpClosure:
		return int(in.B), ra.env.FuncNumCaptures(int(in.C)), 'B'
	case OpConcatN:
		// Части лежат подряд: B..B+C-1 (C — число частей).
		if in.C <= 0 {
			return 0, 0, 0
		}
		return int(in.B), int(in.C), 'B'
	}
	return 0, 0, 0
}

// ---------------------------------------------------------------------------
// Bitset
// ---------------------------------------------------------------------------

type bitset []uint64

func newBits(n int) bitset       { return make(bitset, (n+63)/64) }
func (b bitset) set(i int)       { b[i>>6] |= 1 << uint(i&63) }
func (b bitset) clear(i int)     { b[i>>6] &^= 1 << uint(i&63) }
func (b bitset) test(i int) bool { return b[i>>6]&(1<<uint(i&63)) != 0 }
func (b bitset) or(o bitset) {
	for i := range b {
		b[i] |= o[i]
	}
}
func (b bitset) andNot(o bitset) {
	for i := range b {
		b[i] &^= o[i]
	}
}
func (b bitset) clone() bitset { c := make(bitset, len(b)); copy(c, b); return c }

// ---------------------------------------------------------------------------
// Liveness over SSA values
// ---------------------------------------------------------------------------

func voUses(vo *ssaOp) []int {
	var u []int
	if vo.a >= 0 {
		u = append(u, vo.a)
	}
	if vo.b >= 0 {
		u = append(u, vo.b)
	}
	if vo.c >= 0 {
		u = append(u, vo.c)
	}
	u = append(u, vo.win...)
	if vo.nameVal >= 0 {
		u = append(u, vo.nameVal)
	}
	return u
}

func voDefs(vo *ssaOp) []int {
	var d []int
	if vo.da >= 0 {
		d = append(d, vo.da)
	}
	if vo.db >= 0 {
		d = append(d, vo.db)
	}
	if vo.dc >= 0 {
		d = append(d, vo.dc)
	}
	return d
}

func (ra *ssaGraph) computeLiveness() bool {
	V := len(ra.vals)
	defs := make([]bitset, ra.nBlocks)
	uses := make([]bitset, ra.nBlocks)
	phiDefs := make([]bitset, ra.nBlocks)
	// edgeOps[p][s]
	edgeOps := map[[2]int]bitset{}
	for bi := 0; bi < ra.nBlocks; bi++ {
		defs[bi] = newBits(V)
		uses[bi] = newBits(V)
		phiDefs[bi] = newBits(V)
	}
	for bi, b := range ra.blocks {
		seen := newBits(V)
		for _, ph := range b.phis {
			r := ra.resolve(ph.res)
			defs[bi].set(r)
			phiDefs[bi].set(r)
			seen.set(r)
		}
		for i := b.start; i < b.end; i++ {
			vo := ra.vops[i]
			for _, u0 := range voUses(vo) {
				u := ra.resolve(u0)
				if !seen.test(u) {
					uses[bi].set(u)
				}
			}
			for _, d0 := range voDefs(vo) {
				d := ra.resolve(d0)
				defs[bi].set(d)
				seen.set(d)
			}
		}
		// phi operands used on incoming edges (обычных и исключительных)
		tgts := b.succs
		if len(ra.excSuccs[bi]) > 0 {
			tgts = make([]int, 0, len(b.succs)+len(ra.excSuccs[bi]))
			tgts = append(tgts, b.succs...)
			tgts = append(tgts, ra.excSuccs[bi]...)
		}
		for _, s := range tgts {
			sb := ra.blocks[s]
			for _, p := range sb.preds {
				if p != bi {
					continue
				}
				key := [2]int{bi, s}
				bs := edgeOps[key]
				if bs == nil {
					bs = newBits(V)
					edgeOps[key] = bs
				}
				for _, ph := range sb.phis {
					if op, ok := ph.ops[p]; ok {
						bs.set(ra.resolve(op))
					}
				}
			}
		}
	}

	liveIn := make([]bitset, ra.nBlocks)
	liveOut := make([]bitset, ra.nBlocks)
	for i := range liveIn {
		liveIn[i] = newBits(V)
		liveOut[i] = newBits(V)
	}
	for changed := true; changed; {
		changed = false
		for bi := 0; bi < ra.nBlocks; bi++ {
			out := newBits(V)
			tgts := ra.blocks[bi].succs
			if len(ra.excSuccs[bi]) > 0 {
				tgts = make([]int, 0, len(ra.blocks[bi].succs)+len(ra.excSuccs[bi]))
				tgts = append(tgts, ra.blocks[bi].succs...)
				tgts = append(tgts, ra.excSuccs[bi]...)
			}
			for _, s := range tgts {
				tmp := liveIn[s].clone()
				tmp.andNot(phiDefs[s])
				if eo := edgeOps[[2]int{bi, s}]; eo != nil {
					tmp.or(eo)
				}
				out.or(tmp)
			}
			if !bitsEq(out, liveOut[bi]) {
				changed = true
				liveOut[bi] = out
			}
			in := out.clone()
			in.andNot(defs[bi])
			in.or(uses[bi])
			if !bitsEq(in, liveIn[bi]) {
				changed = true
				liveIn[bi] = in
			}
		}
	}
	ra.liveIn, ra.liveOut = liveIn, liveOut
	return true
}

func bitsEq(a, b bitset) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Interference
// ---------------------------------------------------------------------------

func (ra *ssaGraph) buildInterference() bool {
	ra.edges = nil
	for bi, b := range ra.blocks {
		live := ra.liveOut[bi].clone()
		// Значения-объекты исключений, которые VM может записать во время
		// исполнения этого блока: запись затрёт любой живой регистр, поэтому
		// они должны мешать всему, что живо в защищённом коде.
		var objs []int
		for _, h := range ra.excSuccs[bi] {
			for _, v := range ra.excObjVal[h] {
				objs = append(objs, ra.resolve(v))
			}
		}
		for i := b.end - 1; i >= b.start; i-- {
			vo := ra.vops[i]
			for _, d0 := range voDefs(vo) {
				d := ra.resolve(d0)
				for it := live.iter(); it.next(); {
					x := it.val()
					if x != d {
						ra.addEdge(d, x)
					}
				}
			}
			if len(objs) > 0 {
				for _, o := range objs {
					for it := live.iter(); it.next(); {
						ra.addEdge(o, it.val())
					}
					for _, d0 := range voDefs(vo) {
						ra.addEdge(o, ra.resolve(d0))
					}
				}
			}
			// NEW пишет результат в A до чтения ctor-аргументов из B, поэтому
			// результат не должен делить регистр с окном аргументов.
			if vo.op == OpNew {
				for _, d0 := range voDefs(vo) {
					d := ra.resolve(d0)
					for _, u0 := range voUses(vo) {
						u := ra.resolve(u0)
						if u != d {
							ra.addEdge(d, u)
						}
					}
				}
			}
			// rUseDef-регистр читается и перезаписывается на месте: его новое
			// значение не может делить регистр с ДРУГИМИ источниками той же
			// инструкции (VM читает операнды до записи; иначе, например, для
			// INDEX_SET_RES регистр C совпал бы с B-ключом и значением для
			// записи оказался бы ключ).
			if opArole[vo.op] == rUseDef && vo.a >= 0 && vo.da >= 0 {
				d := ra.resolve(vo.da)
				skip := ra.resolve(vo.a)
				for _, u0 := range voUses(vo) {
					u := ra.resolve(u0)
					if u != skip && u != d {
						ra.addEdge(d, u)
					}
				}
			}
			if opCrole[vo.op] == rUseDef && vo.c >= 0 && vo.dc >= 0 {
				d := ra.resolve(vo.dc)
				skip := ra.resolve(vo.c)
				for _, u0 := range voUses(vo) {
					u := ra.resolve(u0)
					if u != skip && u != d {
						ra.addEdge(d, u)
					}
				}
			}
			for _, d0 := range voDefs(vo) {
				live.clear(ra.resolve(d0))
			}
			for _, u0 := range voUses(vo) {
				live.set(ra.resolve(u0))
			}
		}
		for _, ph := range b.phis {
			p := ra.resolve(ph.res)
			for it := live.iter(); it.next(); {
				x := it.val()
				if x != p {
					ra.addEdge(p, x)
				}
			}
		}
	}
	return true
}
func (ra *ssaGraph) addEdge(a, b int) {
	if a < 0 || b < 0 || a == b {
		return
	}
	ra.edges = append(ra.edges, [2]int{a, b})
}

type bitIter struct {
	b bitset
	i int
}

func (b bitset) iter() bitIter { return bitIter{b: b} }
func (it *bitIter) next() bool {
	for it.i < len(it.b)*64 {
		w := it.i >> 6
		bit := uint(it.i & 63)
		if it.b[w]&(1<<bit) != 0 {
			return true
		}
		it.i++
	}
	return false
}
func (it *bitIter) val() int { id := it.i; it.i++; return id }
