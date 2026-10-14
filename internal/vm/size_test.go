package vm

import (
	"testing"
	"unsafe"
)

func TestSizes(t *testing.T) {
	t.Logf("sizeof(Slot)        = %d", unsafe.Sizeof(Slot{}))
	t.Logf("sizeof(Instruction) = %d", unsafe.Sizeof(Instruction{}))
}
