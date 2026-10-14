//go:build dumpbc

package php

import (
	"fmt"
	"testing"

	"phpvm/internal/vm"
)

func TestDumpSumBytecode(t *testing.T) {
	vm.SetDumpPreAllocHook()
	src := `<?php $s=0; for($i=0;$i<100;$i++){ $s=$s+$i; } echo $s;`
	c, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("--- post-alloc ---")
	for i := 0; i < c.Main.Len(); i++ {
		in := c.Main.At(i)
		fmt.Printf("%3d: %-22s A=%d B=%d C=%d\n", i, in.Op.String(), in.A, in.B, in.C)
	}
}
