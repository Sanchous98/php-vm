//go:build vmdebug

package vm

import "fmt"

// checkConstKinds проверяет инвариант материализации по типам: IntK-опкоды
// ссылаются на int-константы (constInts), FloatK — на float (constFloats).
// Imm-формы несут значение прямо в C и пул не читают. Включается сборкой с
// тегом `vmdebug`; в обычной сборке — no-op (см. kassert_off.go).
func (c *Chunk) checkConstKinds() {
	for i, in := range c.code {
		var want Type
		switch in.Op {
		case OpAddIntK, OpSubIntK, OpMulIntK, OpModIntK,
			OpLessIntK, OpLessOrEqualIntK, OpGreaterIntK,
			OpGreaterEqualIntK, OpEqualIntK, OpJumpIfLessIntK:
			want = IntType
		case OpAddFloatK, OpLessFloatK:
			want = FloatType
		default:
			continue
		}
		if in.C < 0 || int(in.C) >= len(c.constants) {
			panic(fmt.Sprintf("vmdebug: %s at %d: const index %d out of range (%d)",
				in.Op, i, in.C, len(c.constants)))
		}
		if c.constants[in.C].typ != want {
			panic(fmt.Sprintf("vmdebug: %s at %d: const#%d has type %d, want %d",
				in.Op, i, in.C, c.constants[in.C].typ, want))
		}
	}
}
