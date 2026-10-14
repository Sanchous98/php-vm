package php

import (
	"testing"

	"phpvm/internal/vm"
)

// TestAccumulatorLoopNoPhiMoves проверяет, что SSA-аллокатор не вставляет
// копии для loop-carried значений именованных переменных: phi-результат
// предокрашивается в регистр переменной, поэтому out-of-SSA не должен
// генерировать MOVE на входе в цикл и на обратном ребре.
func TestAccumulatorLoopNoPhiMoves(t *testing.T) {
	c, err := Compile(`<?php $s=0; for($i=0;$i<100;$i++){ $s=$s+$i; } echo $s;`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < c.Main.Len(); i++ {
		if in := c.Main.At(i); in.Op == vm.OpMove {
			t.Fatalf("unexpected MOVE at %d: A=%d B=%d", i, in.A, in.B)
		}
	}
	got, err := runCompiled(t, `<?php $s=0; for($i=0;$i<100;$i++){ $s=$s+$i; } echo $s;`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "4950" {
		t.Fatalf("got %q, want %q", got, "4950")
	}
}
