package vm

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Тест арифметики против живого php. Сравниваем числовые операнды (int/float),
// bool и null. Строки в арифметике PHP 8 -> TypeError, потому исключены.

func phpFloatLit(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "NAN"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func phpIntLit(i int64) string {
	if i == math.MinInt64 {
		return "(-9223372036854775807 - 1)"
	}
	return strconv.FormatInt(i, 10)
}

func TestArithmeticVsPHPLive(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}

	type operand struct {
		lit  string
		slot Slot
	}
	var ops []operand
	addI := func(i int64) { ops = append(ops, operand{phpIntLit(i), MakeInt(i)}) }
	addF := func(f float64) { ops = append(ops, operand{phpFloatLit(f), MakeFloat(f)}) }

	addI(0)
	addI(1)
	addI(-1)
	addI(7)
	addI(-7)
	addI(2)
	addI(3)
	addI(math.MaxInt64)
	addI(math.MinInt64)
	addF(1.5)
	addF(-1.5)
	addF(0.0)
	addF(math.Copysign(0, -1))
	addF(2.5)
	addF(1e20)
	addF(0.1)
	ops = append(ops,
		operand{"true", MakeBool(true)},
		operand{"false", MakeBool(false)},
		operand{"null", MakeNull()},
	)

	type arithOp struct {
		name string
		slot func(a, b Slot) (Slot, bool)
	}
	arithOps := []arithOp{
		{"+", func(a, b Slot) (Slot, bool) { return Add(a, b), true }},
		{"-", func(a, b Slot) (Slot, bool) { return Sub(a, b), true }},
		{"*", func(a, b Slot) (Slot, bool) { return Mul(a, b), true }},
		{"**", func(a, b Slot) (Slot, bool) { return Pow(a, b), true }},
		{"/", func(a, b Slot) (Slot, bool) { r, e := Div(a, b); return r, e == nil }},
		{"%", func(a, b Slot) (Slot, bool) { r, e := Mod(a, b); return r, e == nil }},
	}

	var sb strings.Builder
	sb.WriteString("<?php\n")
	type pair struct {
		op   arithOp
		a, b operand
	}
	var pairs []pair
	for _, op := range arithOps {
		for _, a := range ops {
			if a.lit == "null" {
				continue
			}
			for _, b := range ops {
				if b.lit == "null" {
					continue
				}
				pairs = append(pairs, pair{op, a, b})
				fmt.Fprintf(&sb, "try { print((string)((%s) %s (%s))); } catch (\\Throwable $e) { print(\"\\x01ERR\"); }\nprint(\"\\n\");\n", a.lit, op.name, b.lit)
			}
		}
	}
	dir := t.TempDir()
	path := dir + "/arith.php"
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(php, path).Output()
	if err != nil {
		t.Fatalf("php: %v", err)
	}
	want := strings.Split(string(out), "\n")

	mismatch := 0
	for i, p := range pairs {
		got, ok := p.op.slot(p.a.slot, p.b.slot)
		gotStr := got.ToString()
		if !ok {
			gotStr = "\x01ERR"
		}
		if want[i] != gotStr {
			mismatch++
			if mismatch <= 30 {
				t.Errorf("%s %s %s: got %q, php %q", p.a.lit, p.op.name, p.b.lit, gotStr, want[i])
			}
		}
	}
	if mismatch > 0 {
		t.Fatalf("%d mismatches of %d", mismatch, len(pairs))
	}
}
