package php

import "testing"

func TestZZLen(t *testing.T) {
	for _, src := range []string{"<?php die(\"B\\n\");", "<?php echo \"A\"; exit(3);", "<?php die();"} {
		out, err := runFile(src)
		t.Logf("src=%q out=%q len=%d err=%v", src, out, len(out), err)
	}
}
