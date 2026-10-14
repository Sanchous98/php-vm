package php

import (
	"strings"
	"testing"
)

// TestLexOctalOverflow: переполнение восьмеричной escape-последовательности в
// строке — Warning на этапе компиляции, значение берётся по модулю 256
// (Zend/tests/oct_overflow_char.phpt).
func TestLexOctalOverflow(t *testing.T) {
	out, err := runFile(`<?php
echo "\141\542\143\n";
`)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, `Warning: Octal escape sequence overflow \542 is greater than \377`) {
		t.Errorf("нет предупреждения о переполнении: %q", out)
	}
	if !strings.HasSuffix(out, "abc\n") {
		t.Errorf("неверное значение escape-последовательности: %q", out)
	}
}
