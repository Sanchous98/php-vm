package vm

// Оптимизирующий проход над линейным байткодом чанка. Все переписывания
// выполняются «на месте» (in-place): удалённые инструкции заменяются на
// OpNop, поэтому позиции PC не сдвигаются до SSA-аллокатора регистров.
// Аллокатор не эмитит OpNop в финальный байткод (см. emitVO в regalloc.go),
// благодаря чему реальный размер программы уменьшается уже после раскраски,
// а таблицы переходов (jumps) и TryInfo ремапятся штатным механизмом
// аллокатора.
//
// Проходы не меняют наблюдаемую семантику: не трогают вызовы, магию,
// свойства, порядок побочных эффектов. В чанках с try/catch/finally
// dead-store-анализ отключается (исключение может быть перехвачено в
// произвольной точке защищённого кода).
func (c *Chunk) Optimize() {
	if len(c.code) == 0 {
		return
	}
	c.eliminateUnreachable()
	c.foldConstBranches()
	c.eliminateUnreachable()
	c.eliminateSelfMoves()
	c.eliminateDeadStores()
}

// noFallOps — опкоды, после которых линейное исполнение не продолжается.
func noFallOps(op Opcode) bool {
	switch op {
	case OpJump, OpRet, OpHalt, OpExit, OpThrow, OpFatal, OpParseFatal:
		return true
	}
	return false
}

// isComplexOp помечает опкоды со скрытыми (window) чтениями регистров или
// дополнительными точками, которые требуют консервативного отношения.
func isComplexOp(op Opcode) bool {
	switch op {
	case OpCall, OpCallDyn, OpCallArr, OpNew, OpNewDyn,
		OpMethodCall, OpMethodCallDyn, OpStaticCall, OpStaticCallDyn,
		OpClosure, OpYield, OpPushTry, OpDeclareClass,
		// OpConcatN читает окно регистров, не видимое по операндам.
		OpConcatN:
		return true
	}
	return false
}

// jumpTargets собирает все адреса, на которые может быть передано управление.
func (c *Chunk) jumpTargets() []bool {
	n := len(c.code)
	target := make([]bool, n+1)
	mark := func(t int32) {
		if t >= 0 && int(t) <= n {
			target[t] = true
		}
	}
	for i := range c.code {
		in := c.code[i]
		switch in.Op {
		case OpJump, OpJumpIfFalse, OpJumpIfTrue:
			mark(in.C)
		case OpJumpIfLessIntK, OpJumpIfLessIntImm:
			if int(in.A) < len(c.jumps) {
				mark(c.jumps[in.A])
			}
		}
	}
	for _, t := range c.tries {
		for _, ce := range t.Catches {
			mark(ce.PC)
		}
		if t.FinallyPC >= 0 {
			mark(t.FinallyPC)
		}
		if t.EndPC >= 0 {
			mark(t.EndPC)
		}
	}
	target[0] = true
	return target
}

// eliminateUnreachable заменяет на OpNop инструкции, недостижимые линейно:
// после безусловного перехода/возврата/выброса до следующей метки перехода.
func (c *Chunk) eliminateUnreachable() {
	n := len(c.code)
	target := c.jumpTargets()
	reachable := true
	nop := Instruction{Op: OpNop}
	for i := 0; i < n; i++ {
		if target[i] {
			reachable = true
		}
		if !reachable {
			c.code[i] = nop
			continue
		}
		if noFallOps(c.code[i].Op) {
			reachable = false
		}
	}
}

// eliminateSelfMoves убирает самоприсваивания MOVE r, r.
func (c *Chunk) eliminateSelfMoves() {
	for i := range c.code {
		in := c.code[i]
		if in.Op == OpMove && in.A == in.B {
			c.code[i] = Instruction{Op: OpNop}
		}
	}
}

// constBool возвращает значение булевой константы пула.
func (c *Chunk) constBool(idx int32) (bool, bool) {
	if idx < 0 || int(idx) >= len(c.constants) {
		return false, false
	}
	k := c.constants[idx]
	if k.typ != BoolType {
		return false, false
	}
	return k.num != 0, true
}

// opReadsReg сообщает, читает ли инструкция регистр r (как операнд).
func opReadsReg(in Instruction, r uint8) bool {
	if int(in.Op) >= numOpcodes {
		return false
	}
	if (opArole[in.Op] == rUse || opArole[in.Op] == rUseDef) && in.A == r {
		return true
	}
	if (opBrole[in.Op] == rUse || opBrole[in.Op] == rUseDef) && in.B == r {
		return true
	}
	if (opCrole[in.Op] == rUse || opCrole[in.Op] == rUseDef) && uint8(in.C) == r {
		return true
	}
	// Окно OpConcatN: B..B+C-1.
	if in.Op == OpConcatN && in.C > 0 && int(r) >= int(in.B) && int(r) < int(in.B)+int(in.C) {
		return true
	}
	return false
}

// opDefsReg сообщает, записывает ли инструкция регистр r.
func opDefsReg(in Instruction, r uint8) bool {
	if int(in.Op) >= numOpcodes {
		return false
	}
	if (opArole[in.Op] == rDef || opArole[in.Op] == rUseDef) && in.A == r {
		return true
	}
	if (opBrole[in.Op] == rDef || opBrole[in.Op] == rUseDef) && in.B == r {
		return true
	}
	if (opCrole[in.Op] == rDef || opCrole[in.Op] == rUseDef) && uint8(in.C) == r {
		return true
	}
	return false
}

// simpleDefReg возвращает регистр-назначение для «чистых» записей, удаление
// которых не имеет побочных эффектов.
func simpleDefReg(in Instruction) (uint8, bool) {
	switch in.Op {
	case OpLoadK, OpMove, OpNull, OpTrue, OpFalse:
		return in.A, true
	}
	return 0, false
}

// regReadElsewhere консервативно проверяет, читается ли регистр r где-либо,
// кроме указанных позиций. Любой сложный опкод (вызов и т.п.) блокирует
// свёртку: его window-чтения не видны по операндам.
func (c *Chunk) regReadElsewhere(skip1, skip2 int, r uint8) bool {
	for i := range c.code {
		if i == skip1 || i == skip2 {
			continue
		}
		if isComplexOp(c.code[i].Op) {
			return true
		}
		if opReadsReg(c.code[i], r) {
			return true
		}
	}
	return false
}

// foldConstBranches сворачивает переход по константному булеву условию:
//
//	LOADK r, <bool>            LOADK/NOP
//	JUMP_IF_* r, T        ->   JUMP T          (взятие)
//	                      или  NOP            (не взятие)
//
// Свёртка выполняется, только если регистр условия больше нигде не читается.
func (c *Chunk) foldConstBranches() {
	n := len(c.code)
	target := c.jumpTargets()
	for i := 0; i+1 < n; i++ {
		// Свёртка допустима только внутри одного базового блока: иначе на
		// i+1 может приходить другой путь, где регистр условия не константа.
		if target[i+1] {
			continue
		}
		in := c.code[i]
		var val bool
		var known bool
		switch in.Op {
		case OpLoadK:
			val, known = c.constBool(in.C)
		case OpTrue:
			val, known = true, true
		case OpFalse:
			val, known = false, true
		default:
			continue
		}
		if !known {
			continue
		}
		br := c.code[i+1]
		if (br.Op != OpJumpIfFalse && br.Op != OpJumpIfTrue) || br.A != in.A {
			continue
		}
		if c.regReadElsewhere(i, i+1, in.A) {
			continue
		}
		taken := val
		if br.Op == OpJumpIfFalse {
			taken = !val
		}
		c.code[i] = Instruction{Op: OpNop}
		if taken {
			c.code[i+1] = Instruction{Op: OpJump, C: br.C}
		} else {
			c.code[i+1] = Instruction{Op: OpNop}
		}
	}
}

// eliminateDeadStores убирает записи LoadK/Move/Null/True/False в регистр,
// который перезаписывается до первого чтения внутри того же базового блока.
// Анализ строго локальный, поэтому безопасен: значение не может быть
// использовано вне блока, а исключения в чанках с try отключают проход.
func (c *Chunk) eliminateDeadStores() {
	if len(c.tries) > 0 {
		return
	}
	n := len(c.code)
	starts := make([]bool, n)
	starts[0] = true
	markStart := func(t int) {
		if t >= 0 && t < n {
			starts[t] = true
		}
	}
	for i := range c.code {
		in := c.code[i]
		switch in.Op {
		case OpJump, OpJumpIfFalse, OpJumpIfTrue:
			markStart(int(in.C))
		case OpJumpIfLessIntK, OpJumpIfLessIntImm:
			if int(in.A) < len(c.jumps) {
				markStart(int(c.jumps[in.A]))
			}
		}
		if isTerminator(in.Op) {
			markStart(i + 1)
		}
	}
	for lo := 0; lo < n; {
		hi := lo + 1
		for hi < n && !starts[hi] {
			hi++
		}
		c.dseBlock(lo, hi)
		lo = hi
	}
}

// dseBlock выполняет dead-store-устранение внутри одного базового блока
// [lo, hi). Блок с window-опкодами (вызовы и т.п.) пропускается.
func (c *Chunk) dseBlock(lo, hi int) {
	for k := lo; k < hi; k++ {
		if isComplexOp(c.code[k].Op) {
			return
		}
	}
	for k := lo; k < hi; k++ {
		r, ok := simpleDefReg(c.code[k])
		if !ok {
			continue
		}
		dead := false
		for j := k + 1; j < hi; j++ {
			if opReadsReg(c.code[j], r) {
				break
			}
			if opDefsReg(c.code[j], r) {
				dead = true
				break
			}
		}
		if dead {
			c.code[k] = Instruction{Op: OpNop}
		}
	}
}
