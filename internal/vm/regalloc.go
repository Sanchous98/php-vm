package vm

// Register allocator for the register bytecode: greedy coloring with
// coalescing of phi/copy values and "compound" groups for contiguous call
// windows, followed by out-of-SSA (phi -> parallel moves) and code generation.
//
// The SSA-IR, CFG, exception edges, liveness and interference graph live in
// ssa.go (see ssaGraph). This file owns only the coloring/coalescing and emit
// stages. Chunks containing try/catch/finally are supported through exception
// edges built in ssa.go; TryInfo PCs and the exception register (TryCatch.Var)
// are rewritten here during emit/coloring.

import (
	"math/bits"
	"sort"
)

// ---------------------------------------------------------------------------
// Allocation environment / entry point
// ---------------------------------------------------------------------------

// RegAllocEnv supplies callee metadata the allocator cannot infer from a Chunk.
type RegAllocEnv struct {
	// FuncNumArgs returns the declared arity of function index fi (>=0), or a
	// negative value for variadic functions (then the packed argc is used).
	FuncNumArgs func(fi int) int
	// FuncNumCaptures returns the number of captured variables of fi.
	FuncNumCaptures func(fi int) int
}

// ---------------------------------------------------------------------------
// Allocator state
// ---------------------------------------------------------------------------

// regAlloc embeds the SSA-IR and adds the coloring/emit state.
type regAlloc struct {
	*ssaGraph

	color map[int]uint8
	nregs int

	// outTries — try-метаданные после переписывания PC в emit.
	outTries []TryInfo

	// liveMasks — маски живых цветов по инструкциям (см. buildLiveMasks).
	liveMasks []uint64
}

// AllocateRegisters rewrites c.code with SSA-based register allocation and
// updates numRegs. It returns false (leaving the chunk untouched) when the
// chunk contains constructs the allocator does not model yet.
func (c *Chunk) AllocateRegisters(pinned int, env *RegAllocEnv) bool {
	if env == nil || env.FuncNumArgs == nil || env.FuncNumCaptures == nil {
		return false
	}
	ra := &regAlloc{ssaGraph: &ssaGraph{
		code:    c.code,
		lines:   c.lines,
		jumps:   c.jumps,
		pinned:  pinned,
		env:     env,
		blockAt: map[int]int{},
		tries:   c.tries,
	}}
	if c.keepParamsLive {
		// pinned == число параметров + захватов (см. compileBody); держим
		// эти регистры живыми на всём протяжении функции, иначе
		// func_get_arg()/func_get_args() прочитают мусор.
		ra.liveParams = pinned
	}
	newCode, newLines, newJumps, nregs, ok := ra.run()
	if !ok {
		// SSA-аллокация не применима к чанку: код остаётся как есть, но
		// маски живости всё равно нужны сборщику времени жизни (lifetime.go).
		if m := c.buildFallbackLiveMasks(env); m != nil {
			c.liveMasks = m
			c.liveStride = liveMaskStride
		}
		return false
	}
	c.code = newCode
	c.jumps = newJumps
	c.numRegs = nregs
	// Маска регистров именованных переменных относится к регистрам ДО
	// раскраски. После SSA-распределения переменная может жить в другом
	// физическом регистре — пересчитываем маску по цветам её значений, иначе
	// dropDeadRegs списывал бы переменную по SSA-живости (а Zend держит её до
	// конца области; следствие — преждевременный __destruct, см. gh9735).
	c.varRegs = ra.remapVarRegs(c.varRegs)
	if c.hasUserVarRegs {
		c.userVarRegs = ra.remapVarRegs(c.userVarRegs)
	}
	if len(ra.liveMasks) == len(newCode)*liveMaskStride {
		c.liveMasks = ra.liveMasks
		c.liveStride = liveMaskStride
		c.entryNull = buildEntryNullList(ra.entryNullMask, c.numParams, c.numRegs)
	}
	if len(c.tries) > 0 {
		// Регистр, куда VM положит объект исключения, выбирает аллокатор;
		// обновляем TryCatch.Var, чтобы он совпал с регистром чтения в
		// catch-теле.
		for key, v := range ra.seedVal {
			ti, ci := key[0], key[1]
			if ti < 0 || ti >= len(ra.outTries) {
				continue
			}
			if ci < 0 || ci >= len(ra.outTries[ti].Catches) {
				continue
			}
			ra.outTries[ti].Catches[ci].Var = ra.rcolor(v)
		}
		c.tries = ra.outTries
	}
	if len(newLines) == len(newCode) {
		c.lines = newLines
	}
	return true
}

func (ra *regAlloc) run() ([]Instruction, []int32, []int32, int, bool) {
	if !ra.buildBlocks() {
		return nil, nil, nil, 0, false
	}
	if !ra.buildExceptionEdges() {
		return nil, nil, nil, 0, false
	}
	if !ra.buildSSA() {
		return nil, nil, nil, 0, false
	}
	// Точка расширения для SSA-проходов оптимизации (по умолчанию no-op).
	ra.OptimizeSSA()
	if !ra.computeLiveness() {
		return nil, nil, nil, 0, false
	}
	if !ra.buildInterference() {
		return nil, nil, nil, 0, false
	}
	if !ra.coalesceAndColor() {
		return nil, nil, nil, 0, false
	}
	return ra.emit()
}

// ---------------------------------------------------------------------------
// Coalescing and coloring
// ---------------------------------------------------------------------------

type unionFind struct{ p []int }

func newUF(n int) *unionFind {
	u := &unionFind{p: make([]int, n)}
	for i := range u.p {
		u.p[i] = i
	}
	return u
}
func (u *unionFind) find(x int) int {
	for u.p[x] != x {
		u.p[x] = u.p[u.p[x]]
		x = u.p[x]
	}
	return x
}
func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.p[ra] = rb
	}
}

func (ra *regAlloc) coalesceAndColor() bool {
	V := len(ra.vals)
	uf := newUF(V)
	// equal constraints for in-place updates (useDef)
	for _, vo := range ra.vops {
		if vo == nil {
			continue
		}
		if opArole[vo.op] == rUseDef && vo.a >= 0 && vo.da >= 0 {
			uf.union(ra.resolve(vo.a), ra.resolve(vo.da))
		}
		if opBrole[vo.op] == rUseDef && vo.b >= 0 && vo.db >= 0 {
			uf.union(ra.resolve(vo.b), ra.resolve(vo.db))
		}
	}

	rep := func(id int) int { return uf.find(ra.resolve(id)) }

	// Phi-операнды, приходящие по рёбрам исключений, обязаны делить цвет с
	// результатом phi: перемещение на такое ребро вставить некуда, значит
	// значение должно уже лежать в нужном регистре к моменту throw. Для
	// ExcReg-переменных это не нужно: VM перезаписывает их регистр
	// непосредственно перед прыжком в обработчик.
	for h, preds := range ra.excPreds {
		if ra.blocks[h] == nil {
			continue
		}
		for _, ph := range ra.blocks[h].phis {
			rres := ra.resolve(ph.res)
			for _, p := range preds {
				if op, ok := ph.ops[p]; ok {
					uf.union(rres, ra.resolve(op))
				}
			}
		}
	}

	// Входное значение phi в блоке входа (ключ -1): перемещение на неявное
	// ребро от точки входа вставить некуда, поэтому результат phi обязан
	// делить цвет со входным значением (регистром параметра/ячейки).
	for h := range ra.blocks {
		for _, ph := range ra.blocks[h].phis {
			if op, ok := ph.ops[-1]; ok {
				uf.union(ra.resolve(ph.res), ra.resolve(op))
			}
		}
	}

	// consecutive groups for call windows
	groupOf := map[int]int{}
	var groupMembers [][]int
	for _, vo := range ra.vops {
		if vo == nil || len(vo.win) == 0 {
			continue
		}
		var members []int
		if vo.winSlot == 'A' && vo.da >= 0 {
			members = append(members, rep(vo.da))
		}
		seenHere := map[int]bool{}
		dup := false
		for _, w := range vo.win {
			r := rep(w)
			if seenHere[r] {
				dup = true
				break
			}
			seenHere[r] = true
			members = append(members, r)
		}
		if dup {
			return false
		}
		if len(members) <= 1 {
			continue
		}
		gid := len(groupMembers)
		for _, r := range members {
			if _, ok := groupOf[r]; ok {
				return false
			}
			groupOf[r] = gid
		}
		groupMembers = append(groupMembers, members)
	}

	nodeOf := func(r int) int {
		if g, ok := groupOf[r]; ok {
			return V + g
		}
		return r
	}
	widthOf := func(node int) int {
		if node >= V && node < V+len(groupMembers) {
			return len(groupMembers[node-V])
		}
		return 1
	}

	// adjacency between nodes
	adj := map[int]map[int]bool{}
	addAdj := func(a, b int) {
		if a == b {
			return
		}
		if adj[a] == nil {
			adj[a] = map[int]bool{}
		}
		if adj[b] == nil {
			adj[b] = map[int]bool{}
		}
		adj[a][b] = true
		adj[b][a] = true
	}
	for _, e := range ra.edges {
		na, nb := nodeOf(rep(e[0])), nodeOf(rep(e[1]))
		if na != nb {
			addAdj(na, nb)
		}
	}

	// collect nodes
	nodeSet := map[int]bool{}
	for i := 0; i < V; i++ {
		res := ra.resolve(i)
		r := rep(res)
		nodeSet[nodeOf(r)] = true
	}
	for g := range groupMembers {
		nodeSet[V+g] = true
	}
	// Зарезервированные цвета [0, liveParams): регистры параметров
	// (keepParamsLive) не должны достаться временным, иначе func_get_args()
	// прочитает мусор. Вводим синтетические узлы, смежные со всеми, и
	// предокрашиваем их в соответствующие цвета.
	synBase := V + len(groupMembers)
	var synNodes []int
	for k := 0; k < ra.liveParams; k++ {
		id := synBase + k
		nodeSet[id] = true
		synNodes = append(synNodes, id)
	}
	nodes := make([]int, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	for _, s := range synNodes {
		for n := range nodeSet {
			addAdj(s, n)
		}
	}

	precolor := map[int]int{}
	for i := 0; i < V; i++ {
		if ra.vals[i].precolor >= 0 {
			r := rep(i)
			n := nodeOf(r)
			if p, ok := precolor[n]; ok {
				if p != ra.vals[i].precolor {
					return false
				}
			} else {
				precolor[n] = ra.vals[i].precolor
			}
		}
	}
	for k, s := range synNodes {
		precolor[s] = k
	}

	sort.Slice(nodes, func(i, j int) bool {
		_, pi := precolor[nodes[i]]
		_, pj := precolor[nodes[j]]
		if pi != pj {
			return pi
		}
		di, dj := len(adj[nodes[i]]), len(adj[nodes[j]])
		if di != dj {
			return di > dj
		}
		// Детерминированный тай-брейк: sort.Slice неустойчив, а nodes
		// собирается из map, поэтому без сравнения по id порядок (и, значит,
		// раскраска) зависел бы от итерации map.
		return nodes[i] < nodes[j]
	})

	assigned := map[int]int{}
	width := map[int]int{}
	for _, n := range nodes {
		w := widthOf(n)
		if p, ok := precolor[n]; ok {
			assigned[n] = p
			width[n] = w
			continue
		}
		base := 0
		for {
			ok := true
			for c := base; c < base+w && ok; c++ {
				for nb := range adj[n] {
					if ab, done := assigned[nb]; done {
						bw := width[nb]
						if c >= ab && c < ab+bw {
							ok = false
							break
						}
					}
				}
			}
			if ok {
				break
			}
			base++
		}
		assigned[n] = base
		width[n] = w
	}

	ra.color = map[int]uint8{}
	maxc := -1
	for i := 0; i < V; i++ {
		res := ra.resolve(i)
		r := rep(res)
		n := nodeOf(r)
		c := assigned[n]
		if g, ok := groupOf[r]; ok {
			for off, m := range groupMembers[g] {
				if m == r {
					c += off
					break
				}
			}
		}
		ra.color[res] = uint8(c)
		if c > maxc {
			maxc = c
		}
	}
	nregs := maxc + 1
	if nregs < ra.pinned {
		nregs = ra.pinned
	}
	if nregs > 255 {
		return false
	}
	ra.nregs = nregs
	return true
}

func (ra *regAlloc) rcolor(id int) uint8 { return ra.color[ra.resolve(id)] }

// remapVarRegs переносит биты регистров именованных переменных (заданные до
// раскраски) на физические регистры их значений после SSA-распределения.
func (ra *regAlloc) remapVarRegs(old [liveMaskStride]uint64) [liveMaskStride]uint64 {
	var out [liveMaskStride]uint64
	for i := range ra.vals {
		reg := ra.vals[i].reg
		if int(reg) >= maxRegsHard || old[reg>>6]&(1<<uint(reg&63)) == 0 {
			continue
		}
		r := int(ra.rcolor(i))
		if r < maxRegsHard {
			out[r>>6] |= 1 << uint(r&63)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Out-of-SSA and code generation
// ---------------------------------------------------------------------------

type raMove struct{ dst, src uint8 }

type raTramp struct {
	moves  []raMove
	target int
}

func (ra *regAlloc) emit() ([]Instruction, []int32, []int32, int, bool) {
	code := ra.code
	n := len(code)

	// phi copies per edge
	copies := map[[2]int][]raMove{}
	for s, b := range ra.blocks {
		for _, ph := range b.phis {
			rres := ra.rcolor(ph.res)
			for _, p := range b.preds {
				if ra.excEdge[[2]int{p, s}] {
					// На ребро исключения перемещение вставить некуда —
					// операнд обязан делить цвет с результатом phi (см.
					// coalesceAndColor).
					continue
				}
				op, ok := ph.ops[p]
				if !ok {
					continue
				}
				rop := ra.rcolor(op)
				if rop != rres {
					copies[[2]int{p, s}] = append(copies[[2]int{p, s}], raMove{dst: rres, src: rop})
				}
			}
		}
	}

	edgeTarget := map[[2]int]int{}
	copiesEnd := map[int][]raMove{}
	var tramps []raTramp
	// Рёбра обходятся в отсортированном порядке: от него зависят id трампов
	// (позиции в конце кода) и порядок вставки phi-копий. Итерация map сделала
	// бы байткод недетерминированным.
	copyKeys := make([][2]int, 0, len(copies))
	for key := range copies {
		copyKeys = append(copyKeys, key)
	}
	sort.Slice(copyKeys, func(i, j int) bool {
		if copyKeys[i][0] != copyKeys[j][0] {
			return copyKeys[i][0] < copyKeys[j][0]
		}
		return copyKeys[i][1] < copyKeys[j][1]
	})
	for _, key := range copyKeys {
		mv := copies[key]
		p, s := key[0], key[1]
		if len(ra.blocks[p].succs) == 1 {
			copiesEnd[p] = append(copiesEnd[p], mv...)
			edgeTarget[key] = s
		} else {
			t := ra.nBlocks + len(tramps)
			tramps = append(tramps, raTramp{moves: mv, target: s})
			edgeTarget[key] = t
		}
	}

	newJumps := append([]int32(nil), ra.jumps...)
	scratch := uint8(ra.nregs)
	scratchUsed := false

	var out []Instruction
	var outLines []int32
	// outSrc — исходный PC для каждой выходной инструкции (для масок
	// живости); -1 у вставленных пересылок/трампов.
	var outSrc []int32
	curLine := int32(0)
	push := func(o Instruction, ln int32, src int32) {
		out = append(out, o)
		outLines = append(outLines, ln)
		outSrc = append(outSrc, src)
	}
	lineOf := func(i int) int32 {
		if i >= 0 && i < len(ra.lines) {
			return ra.lines[i]
		}
		return 0
	}
	newStart := map[int]int{}
	origToNew := make([]int32, n)
	for i := range origToNew {
		origToNew[i] = -1
	}
	type fix struct {
		pos    int
		target int
		jump   int // -1: out[pos].C ; >=0: newJumps[jump]
	}
	var fixes []fix
	fixJumps := func(idx, target int) { fixes = append(fixes, fix{pos: -1, target: target, jump: idx}) }

	targetID := func(p, s int) int {
		if t, ok := edgeTarget[[2]int{p, s}]; ok {
			return t
		}
		return s
	}

	emitParallel := func(mvs []raMove) {
		insts, used := parallelMove(mvs, scratch)
		if used {
			scratchUsed = true
		}
		for _, in := range insts {
			push(in, curLine, -1)
		}
	}

	emitVO := func(i int) {
		vo := ra.vops[i]
		if vo.op == OpNop {
			// Инструкции, удалённые оптимизатором (заменённые на NOP), в
			// финальный байткод не попадают. Позицию фиксируем в origToNew:
			// на неё могут ссылаться таблицы переходов/TryInfo.
			curLine = lineOf(i)
			origToNew[i] = int32(len(out))
			return
		}
		o := Instruction{Op: vo.op, A: vo.aRaw, B: vo.bRaw, C: vo.cRaw}
		if vo.da >= 0 {
			o.A = ra.rcolor(vo.da)
		} else if vo.a >= 0 {
			o.A = ra.rcolor(vo.a)
		}
		if vo.db >= 0 {
			o.B = ra.rcolor(vo.db)
		} else if vo.b >= 0 {
			o.B = ra.rcolor(vo.b)
		}
		if vo.dc >= 0 {
			if vo.op == OpYield {
				o.C = int32(ra.rcolor(vo.dc)) | (vo.cRaw & YieldFromBit)
			} else {
				o.C = int32(ra.rcolor(vo.dc))
			}
		} else if vo.c >= 0 {
			o.C = int32(ra.rcolor(vo.c))
		}
		if len(vo.win) > 0 && vo.winSlot == 'B' {
			o.B = ra.rcolor(vo.win[0])
		}
		if vo.nameVal >= 0 {
			o.C = int32(ra.rcolor(vo.nameVal)) | (vo.cRaw &^ 0xFF)
		}
		curLine = lineOf(i)
		origToNew[i] = int32(len(out))
		push(o, curLine, int32(i))
	}

	emitCopiesEnd := func(b int) {
		if mv := copiesEnd[b]; len(mv) > 0 {
			emitParallel(mv)
		}
	}

	for b := 0; b < ra.nBlocks; b++ {
		newStart[b] = len(out)
		blk := ra.blocks[b]
		lastIdx := blk.end - 1
		last := code[lastIdx]
		term := isTerminator(last.Op)
		if term {
			for i := blk.start; i < lastIdx; i++ {
				emitVO(i)
			}
			emitCopiesEnd(b)
			tl := lineOf(lastIdx)
			curLine = tl
			switch last.Op {
			case OpJump:
				push(Instruction{Op: OpJump}, tl, int32(lastIdx))
				origToNew[lastIdx] = int32(len(out) - 1)
				fixes = append(fixes, fix{pos: len(out) - 1, target: targetID(b, blk.succs[0]), jump: -1})
			case OpJumpIfFalse, OpJumpIfTrue:
				o := Instruction{Op: last.Op, A: ra.rcolor(ra.vops[lastIdx].a)}
				push(o, tl, int32(lastIdx))
				origToNew[lastIdx] = int32(len(out) - 1)
				fixes = append(fixes, fix{pos: len(out) - 1, target: targetID(b, blk.succs[0]), jump: -1})
				if t := targetID(b, blk.succs[1]); t != blk.succs[1] {
					push(Instruction{Op: OpJump}, tl, int32(lastIdx))
					fixes = append(fixes, fix{pos: len(out) - 1, target: t, jump: -1})
				}
			case OpJumpIfLessIntK, OpJumpIfLessIntImm:
				o := Instruction{Op: last.Op, A: last.A, B: ra.rcolor(ra.vops[lastIdx].b), C: last.C}
				push(o, tl, int32(lastIdx))
				origToNew[lastIdx] = int32(len(out) - 1)
				fixJumps(int(last.A), targetID(b, blk.succs[0]))
				if t := targetID(b, blk.succs[1]); t != blk.succs[1] {
					push(Instruction{Op: OpJump}, tl, int32(lastIdx))
					fixes = append(fixes, fix{pos: len(out) - 1, target: t, jump: -1})
				}
			default: // Ret / Halt / Exit
				emitVO(lastIdx)
			}
		} else {
			for i := blk.start; i < blk.end; i++ {
				emitVO(i)
			}
			emitCopiesEnd(b)
			if blk.end >= n {
				if len(tramps) > 0 {
					push(Instruction{Op: OpHalt}, curLine, -1)
				}
			}
		}
	}

	for ti := range tramps {
		id := ra.nBlocks + ti
		newStart[id] = len(out)
		emitParallel(tramps[ti].moves)
		p := len(out)
		push(Instruction{Op: OpJump}, curLine, -1)
		fixes = append(fixes, fix{pos: p, target: tramps[ti].target, jump: -1})
	}

	for _, fx := range fixes {
		t := newStart[fx.target]
		if fx.jump >= 0 {
			newJumps[fx.jump] = int32(t)
		} else {
			out[fx.pos].C = int32(t)
		}
	}

	// Пересчитываем адреса try-регионов через карту старый-PC→новый.
	// Адреса обработчиков — начала блоков; EndPC может указывать в середину
	// блока, поэтому используем попозиционную карту origToNew.
	if len(ra.tries) > 0 {
		mapPC := func(pc int32) (int32, bool) {
			if pc < 0 {
				return -1, true
			}
			p := int(pc)
			if p < n {
				if v := origToNew[p]; v >= 0 {
					return v, true
				}
				bi := ra.blockOf[p]
				return int32(newStart[bi]), true
			}
			if p == n {
				return int32(len(out)), true
			}
			return 0, false
		}
		newTries := make([]TryInfo, len(ra.tries))
		for i, t := range ra.tries {
			nt := t
			for j := range nt.Catches {
				v, ok := mapPC(nt.Catches[j].PC)
				if !ok {
					return nil, nil, nil, 0, false
				}
				nt.Catches[j].PC = v
			}
			if nt.FinallyPC >= 0 {
				v, ok := mapPC(nt.FinallyPC)
				if !ok {
					return nil, nil, nil, 0, false
				}
				nt.FinallyPC = v
			}
			if nt.EndPC >= 0 {
				v, ok := mapPC(nt.EndPC)
				if !ok {
					return nil, nil, nil, 0, false
				}
				nt.EndPC = v
			}
			newTries[i] = nt
		}
		ra.outTries = newTries
	}

	nregs := ra.nregs
	if scratchUsed {
		nregs = int(scratch) + 1
	}
	ra.buildLiveMasks(outSrc)
	return out, outLines, newJumps, nregs, true
}

// buildEntryNullList превращает маску «входных» регистров в список для
// пролога кадра. Параметры и захваты (регистры < numParams) в список не
// попадают: их определяет привязка аргументов, а непереданные зануляются
// отдельным циклом. Регистры вне numRegs отбрасываются.
func buildEntryNullList(mask [liveMaskStride]uint64, numParams, numRegs int) []uint8 {
	var out []uint8
	for w := range mask {
		m := mask[w]
		for m != 0 {
			r := w*64 + bits.TrailingZeros64(m)
			m &= m - 1
			if r >= numParams && r < numRegs {
				out = append(out, uint8(r))
			}
		}
	}
	return out
}

// liveMaskStride — число слов uint64 на один PC в Chunk.liveMasks
// (maxRegsHard = 256 бит).
const liveMaskStride = maxRegsHard / 64

// liveBefore — множество SSA-значений, живых перед каждой инструкцией
// исходного чанка. Заполняется buildLiveMasks после раскраски.
func (ra *regAlloc) buildLiveMasks(outSrc []int32) {
	if ra.liveIn == nil {
		return
	}
	liveBefore := make([]bitset, len(ra.code))
	for bi, b := range ra.blocks {
		if b == nil || b.end > len(liveBefore) {
			continue
		}
		live := ra.liveOut[bi].clone()
		// Объект исключения «определён» в начале catch-блока: VM кладёт его
		// в регистр при передаче управления. Без явного определения значение
		// seed остаётся живым на всех путях до catch, и регистр временного
		// выражения, слитый с ним, удерживал бы объект от __destruct до
		// конца функции (см. lifetime.go, dropDeadRegs). Чистим и в защищённых
		// блоках: их исключительные рёбра ведут в хендлеры, чьи seed-значения
		// рождаются только в момент исключения (иначе маска «держит» чужие
		// временные объекты и #N выдаётся позже Zend).
		for _, v := range ra.excObjVal[bi] {
			live.clear(ra.resolve(v))
		}
		for _, h := range ra.excSuccs[bi] {
			for _, v := range ra.excObjVal[h] {
				live.clear(ra.resolve(v))
			}
		}
		for i := b.end - 1; i >= b.start; i-- {
			vo := ra.vops[i]
			if vo == nil {
				continue
			}
			for _, d := range voDefs(vo) {
				live.clear(ra.resolve(d))
			}
			for _, u := range voUses(vo) {
				live.set(ra.resolve(u))
			}
			liveBefore[i] = live.clone()
		}
	}
	masks := make([]uint64, len(outSrc)*liveMaskStride)
	for k, pc := range outSrc {
		if pc < 0 {
			// Вставленные пересылки (phi/трампы): OpSweep там не эмитится,
			// маска не нужна.
			continue
		}
		bits := liveBefore[pc]
		base := k * liveMaskStride
		for it := bits.iter(); it.next(); {
			r := int(ra.rcolor(ra.resolve(it.val())))
			if r < maxRegsHard {
				masks[base+r>>6] |= 1 << uint(r&63)
			}
		}
		// Закреплённые регистры (именованные переменные main, параметры и
		// захваты функций) — корни всегда: Zend держит переменную до
		// перезаписи/unset, даже если значение больше не читается.
		for r := 0; r < ra.pinned && r < maxRegsHard; r++ {
			masks[base+r>>6] |= 1 << uint(r&63)
		}
	}
	ra.liveMasks = masks
}

// parallelMove lowers a set of parallel copies, breaking cycles with a scratch
// register. It returns the move instructions and whether scratch was used.
func parallelMove(pairs []raMove, scratch uint8) ([]Instruction, bool) {
	// prune self-moves and duplicates
	var rem []raMove
	for _, m := range pairs {
		if m.dst == m.src {
			continue
		}
		dup := false
		for _, r := range rem {
			if r == m {
				dup = true
				break
			}
		}
		if !dup {
			rem = append(rem, m)
		}
	}
	var out []Instruction
	used := false
	for len(rem) > 0 {
		progress := false
		var next []raMove
		for _, c := range rem {
			blocked := false
			for _, d := range rem {
				if d.src == c.dst && !(d.dst == c.dst && d.src == c.src) {
					blocked = true
					break
				}
			}
			if blocked {
				next = append(next, c)
			} else {
				out = append(out, Instruction{Op: OpMove, A: c.dst, B: c.src})
				progress = true
			}
		}
		rem = next
		if !progress && len(rem) > 0 {
			// cycle: park rem[0].dst in scratch and redirect its readers.
			c := rem[0]
			out = append(out, Instruction{Op: OpMove, A: scratch, B: c.dst})
			used = true
			for i := range rem {
				if rem[i].src == c.dst {
					rem[i].src = scratch
				}
			}
		}
	}
	return out, used
}

// buildFallbackLiveMasks строит маски живых регистров напрямую по байткоду
// (без SSA) для чанков, которые SSA-аллокатор не смог обработать. Нужны
// сборщику времени жизни (lifetime.go): мёртвые регистры не должны удерживать
// объекты. Консервативно: чанки с try-регионами получают «все живы» — на них
// маски не дают выигрыша, но и не искажают живость.
func (c *Chunk) buildFallbackLiveMasks(env *RegAllocEnv) []uint64 {
	n := len(c.code)
	if n == 0 || env == nil {
		return nil
	}
	allLive := func() []uint64 {
		m := make([]uint64, n*liveMaskStride)
		for i := range m {
			m[i] = ^uint64(0)
		}
		// Неизвестная форма окна: консервативно зануляем весь кадр, кроме
		// параметров/захватов (их определяет привязка аргументов).
		np := 0
		if c.numParamsSet {
			np = c.numParams
		}
		c.entryNull = c.entryNull[:0]
		for r := np; r < c.numRegs; r++ {
			c.entryNull = append(c.entryNull, uint8(r))
		}
		return m
	}
	// Обработчики исключений/finally: статически отслеживаем активные
	// try-регионы (PUSH_TRY C=i ... POP_TRY C=i) и добавляем их catch/finally
	// как достижимые из инструкций внутри региона. Так маски остаются
	// точными для обычного кода (без «все живы»).
	activeHandlers := make([][]int, n)
	if len(c.tries) > 0 {
		tryHandlers := func(i int) []int {
			if i < 0 || i >= len(c.tries) {
				return nil
			}
			t := c.tries[i]
			var hs []int
			for _, ct := range t.Catches {
				if ct.PC >= 0 && int(ct.PC) <= n {
					hs = append(hs, int(ct.PC))
				}
			}
			if t.FinallyPC >= 0 && int(t.FinallyPC) <= n {
				hs = append(hs, int(t.FinallyPC))
			}
			return hs
		}
		var stack []int
		for pc := 0; pc < n; pc++ {
			in := c.code[pc]
			switch in.Op {
			case OpPopTry:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
			for _, ti := range stack {
				activeHandlers[pc] = append(activeHandlers[pc], tryHandlers(ti)...)
			}
			if in.Op == OpPushTry {
				stack = append(stack, int(in.C))
			}
		}
	}
	type mask [liveMaskStride]uint64
	uses := make([]mask, n)
	defs := make([]mask, n)
	setWin := func(m *mask, base, width int) {
		if width < 0 || base < 0 || base+width > maxRegsHard {
			return
		}
		for k := 0; k < width; k++ {
			r := base + k
			m[r>>6] |= 1 << uint(r&63)
		}
	}
	addOp := func(m *mask, role raRole, r int) {
		switch role {
		case rUse, rUseDef:
			m[r>>6] |= 1 << uint(r&63)
		case rDef:
			// def ниже
		}
	}
	for pc := 0; pc < n; pc++ {
		in := c.code[pc]
		u, d := &uses[pc], &defs[pc]
		aRole := opArole[in.Op]
		if in.Op == OpNew && int(in.C>>16) == NewCtorOnlySentinel {
			// Фаза вызова конструктора: объект только читается из A.
			aRole = rUse
		}
		addOp(u, aRole, int(in.A))
		addOp(u, opBrole[in.Op], int(in.B))
		addOp(u, opCrole[in.Op], int(in.C&0xFF))
		if aRole == rDef || aRole == rUseDef {
			d[in.A>>6] |= 1 << uint(in.A&63)
		}
		if opBrole[in.Op] == rDef || opBrole[in.Op] == rUseDef {
			d[in.B>>6] |= 1 << uint(in.B&63)
		}
		if opCrole[in.Op] == rDef || opCrole[in.Op] == rUseDef {
			r := uint8(in.C & 0xFF)
			d[r>>6] |= 1 << uint(r&63)
			if in.Op != OpMethodCallDyn && in.Op != OpStaticCallDyn {
				// C — регистр только если не индекс константы (у K-опкодов
				// роль C = rNone, поэтому сюда не попадаем).
			}
		}
		if opCrole[in.Op] == rDef || opCrole[in.Op] == rUseDef {
			u[uint8(in.C&0xFF)>>6] |= 1 << uint(uint8(in.C&0xFF)&63)
		}
		base, width, slot := windowFallback(in, env)
		if width < 0 {
			return allLive()
		}
		if width > 0 {
			if slot == 'A' && opArole[in.Op] == rDef {
				setWin(d, base, width)
			} else {
				setWin(u, base, width)
			}
		}
		switch in.Op {
		case OpNew:
			if width == 0 && int(in.C>>16) == 0 {
				u[in.B>>6] |= 1 << uint(in.B&63)
			}
		case OpMethodCallDyn, OpStaticCallDyn:
			r := uint8(in.C & 0xFF)
			u[r>>6] |= 1 << uint(r&63)
		case OpYield:
			if in.B != YieldNoKey {
				u[in.B>>6] |= 1 << uint(in.B&63)
			}
		}
	}
	// Объект исключения: VM записывает его в регистр catch перед прыжком в
	// обработчик, поэтому чтение регистра в первой инструкции обработчика —
	// не «использование» значения с предшествующего пути. Убираем это
	// чтение из uses и помечаем регистр определённым в точке обработчика:
	// иначе значение временного выражения, оставшееся в том же регистре,
	// считалось бы живым на всём try-теле и удерживало объект от __destruct
	// (см. lifetime.go, dropDeadRegs).
	for _, t := range c.tries {
		for _, ct := range t.Catches {
			if ct.Var == 0xFF || ct.PC < 0 || int(ct.PC) >= n {
				continue
			}
			r := uint8(ct.Var)
			bit := uint64(1) << uint(r&63)
			defs[ct.PC][r>>6] |= bit
			uses[ct.PC][r>>6] &^= bit
		}
	}
	masks := make([]uint64, n*liveMaskStride)
	pinnedMask := c.varRegs
	succs := func(pc int) []int {
		in := c.code[pc]
		switch in.Op {
		case OpHalt:
			return nil
		case OpRet, OpExit, OpFatal, OpParseFatal:
			return nil
		case OpJump:
			var s []int
			if in.C >= 0 && int(in.C) <= n {
				s = append(s, int(in.C))
			}
			if hs := activeHandlers[pc]; len(hs) > 0 {
				s = append(s, hs...)
			}
			if len(s) == 0 {
				return nil
			}
			return s
		case OpJumpIfFalse, OpJumpIfTrue:
			s := []int{}
			if in.C >= 0 && int(in.C) <= n {
				s = append(s, int(in.C))
			}
			if pc+1 < n {
				s = append(s, pc+1)
			}
			s = append(s, activeHandlers[pc]...)
			return s
		case OpJumpIfLessIntK, OpJumpIfLessIntImm:
			s := []int{}
			if int(in.A) >= 0 && int(in.A) < len(c.jumps) {
				if t := int(c.jumps[int(in.A)]); t >= 0 && t <= n {
					s = append(s, t)
				}
			}
			if pc+1 < n {
				s = append(s, pc+1)
			}
			if hs := activeHandlers[pc]; len(hs) > 0 {
				s = append(s, hs...)
			}
			return s
		}
		var s []int
		if pc+1 < n {
			s = append(s, pc+1)
		}
		if hs := activeHandlers[pc]; len(hs) > 0 && in.Op != OpHalt {
			s = append(s, hs...)
		}
		return s
	}
	live := make([]mask, n+1)
	for changed := true; changed; {
		changed = false
		for pc := n - 1; pc >= 0; pc-- {
			var out mask
			for _, s := range succs(pc) {
				if s >= 0 && s <= n {
					for i := range out {
						out[i] |= live[s][i]
					}
				}
			}
			in := out
			for i := range in {
				in[i] &^= defs[pc][i]
				in[i] |= uses[pc][i]
			}
			if in != live[pc] {
				changed = true
				live[pc] = in
			}
		}
	}
	for pc := 0; pc < n; pc++ {
		base := pc * liveMaskStride
		for i := 0; i < liveMaskStride; i++ {
			masks[base+i] = live[pc][i] | pinnedMask[i]
		}
	}
	// Регистры, которые могут быть прочитаны до первой записи: их слоты
	// могли сохранить значение прошлого вызова, поэтому пролог зануляет их
	// (параметры/захваты привязываются/зануляются отдельно и перекрывают
	// зануление).
	np := 0
	if c.numParamsSet {
		np = c.numParams
	}
	c.entryNull = buildEntryNullList(live[0], np, c.numRegs)
	return masks
}

// windowFallback — аналог ssaGraph.window для нераспределённого байткода.
func windowFallback(in Instruction, env *RegAllocEnv) (base, width int, slot byte) {
	switch in.Op {
	case OpCall:
		packed := uint32(in.C)
		raw := int(packed >> 24)
		if raw > 0 {
			return int(in.B), raw - 1, 'B'
		}
		fi := int(packed & 0x7FFFFF)
		na := env.FuncNumArgs(fi)
		if na < 0 {
			return 0, -1, 0
		}
		return int(in.B), na, 'B'
	case OpNew:
		raw := int(in.C >> 16)
		if raw == NewAllocOnlySentinel {
			return int(in.B), 0, 'B'
		}
		if raw == NewCtorOnlySentinel {
			return int(in.B), int(in.C) & 0xFFFF, 'B'
		}
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
		return int(in.B), env.FuncNumCaptures(int(in.C)), 'B'
	case OpConcatN:
		if in.C <= 0 {
			return 0, 0, 0
		}
		return int(in.B), int(in.C), 'B'
	}
	return 0, 0, 0
}
