package vm

import (
	"bufio"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// referenceValues повторяет набор из testdata/casts.php.
func referenceValues() map[string]Slot {
	return map[string]Slot{
		"null":     MakeNull(),
		"false":    MakeBool(false),
		"true":     MakeBool(true),
		"int0":     MakeInt(0),
		"int1":     MakeInt(1),
		"intm1":    MakeInt(-1),
		"int42":    MakeInt(42),
		"intmax":   MakeInt(math.MaxInt64),
		"intmin":   MakeInt(math.MinInt64),
		"float0":   MakeFloat(0.0),
		"float15":  MakeFloat(1.5),
		"floatm0":  MakeFloat(math.Copysign(0, -1)),
		"floate":   MakeFloat(1.0e20),
		"floatf":   MakeFloat(sum(0.1, 0.2)),
		"emptystr": MakeString(""),
		"str0":     MakeString("0"),
		"str00":    MakeString("00"),
		"strnum":   MakeString("123"),
		"strlead":  MakeString("  12"),
		"strtail":  MakeString("12abc"),
		"strhex":   MakeString("0x1A"),
		"strex":    MakeString("1e2"),
		"strbig":   MakeString("1e20"),
		"strbig2":  MakeString("9999999999999999999"),
		"strneg":   MakeString("-7"),
		"strf":     MakeString("1.5"),
		"strfdot":  MakeString(".5"),
		"stra":     MakeString("abc"),
		"strsp":    MakeString(" "),
	}
}

func goTypeName(php string) string {
	switch php {
	case "NULL":
		return "null"
	case "boolean":
		return "bool"
	case "integer":
		return "int"
	case "double":
		return "float"
	case "string":
		return "string"
	}
	return php
}

func loadMatrix(t *testing.T) map[string]map[string]string {
	t.Helper()
	f, err := os.Open("testdata/casts.tsv")
	if err != nil {
		t.Skipf("reference matrix not found: %v", err)
	}
	defer f.Close()

	m := map[string]map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 3 {
			continue
		}
		if m[parts[1]] == nil {
			m[parts[1]] = map[string]string{}
		}
		m[parts[1]][parts[0]] = parts[2]
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return m
}

func parsePHPInt(s string) (int64, error) {
	if s == "-9223372036854775807-1" {
		return math.MinInt64, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func TestConversionsVsPHP(t *testing.T) {
	m := loadMatrix(t)
	for name, v := range referenceValues() {
		ref, ok := m[name]
		if !ok {
			t.Errorf("no reference for %q", name)
			continue
		}
		if got := v.Type().String(); got != goTypeName(ref["type"]) {
			t.Errorf("%s: type = %s, php %s", name, got, goTypeName(ref["type"]))
		}
		if got := boolString(v.ToBool()); got != ref["bool"] {
			t.Errorf("%s: bool = %s, php %s", name, got, ref["bool"])
		}
		if want, err := parsePHPInt(ref["int"]); err == nil && v.ToInt() != want {
			t.Errorf("%s: int = %d, php %d", name, v.ToInt(), want)
		}
		if want, err := strconv.ParseFloat(ref["float"], 64); err == nil {
			if got := v.ToFloat(); got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
				t.Errorf("%s: float = %v, php %v", name, got, want)
			}
		}
		if gotHex := hexString(v.ToString()); gotHex != ref["str"] {
			t.Errorf("%s: string = %q (%s), php %q (%s)", name, v.ToString(), gotHex, mustDecode(ref["str"]), ref["str"])
		}
	}
}

func TestIntConversionPaths(t *testing.T) {
	if got := MakeString("1e20").ToInt(); got != math.MaxInt64 {
		t.Errorf(`(int)"1e20" = %d, want MaxInt64`, got)
	}
	if got := MakeString("-1e20").ToInt(); got != math.MinInt64 {
		t.Errorf(`(int)"-1e20" = %d, want MinInt64`, got)
	}
	if got := MakeString("9999999999999999999").ToInt(); got != math.MaxInt64 {
		t.Errorf("big decimal string = %d, want MaxInt64", got)
	}
	if got := MakeFloat(1e20).ToInt(); got != 7766279631452241920 {
		t.Errorf("(int)1e20 = %d, want 7766279631452241920", got)
	}
	if got := MakeFloat(math.NaN()).ToInt(); got != 0 {
		t.Errorf("(int)NAN = %d, want 0", got)
	}
	if got := MakeFloat(math.Inf(1)).ToInt(); got != 0 {
		t.Errorf("(int)INF = %d, want 0", got)
	}
	if got := MakeString("9007199254740993").ToInt(); got != 9007199254740993 {
		t.Errorf("9007199254740993 = %d (float64 бы округлил)", got)
	}
	if got := MakeString("123456789012345678").ToInt(); got != 123456789012345678 {
		t.Errorf("123456789012345678 = %d", got)
	}
}

func TestIsNumeric(t *testing.T) {
	cases := map[string]bool{
		"12": true, "  12": true, "12  ": true, "12abc": false, "abc": false,
		"1e2": true, ".5": true, "0x1A": false, "": false, " ": false,
		"0": true, "00": true, "1_000": false, "INF": false, "NAN": false,
		"+5": true, "-0": true, "1e": false, "1e+": false, "+": false,
		"-": false, ".": false, "1.": true, "1.e2": true, "0b1": false,
		"0o17": false, "0X1A": false, "1E2": true, "inf": false, "nan": false,
		" 1 2": false,
	}
	for s, want := range cases {
		if got := IsNumericString(s); got != want {
			t.Errorf("IsNumericString(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestRoundTripPreservesBits(t *testing.T) {
	for _, i := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 42} {
		s := MakeInt(i)
		if back := MakeInt(s.Int()); back.Raw() != s.Raw() {
			t.Errorf("int round-trip lost bits for %d", i)
		}
	}
	for _, f := range []float64{0, 1.5, -0.0, 1e20, sum(0.1, 0.2), math.Pi} {
		s := MakeFloat(f)
		if back := MakeFloat(s.Float()); back.Raw() != s.Raw() {
			t.Errorf("float round-trip lost bits for %v", f)
		}
	}
}

// TestSlotValueBoundary проверяет, что Slot как Value и обратно не теряет
// ни тип, ни значение для примитивов.
func TestSlotValueBoundary(t *testing.T) {
	slots := []Slot{
		MakeNull(), MakeBool(true), MakeBool(false), MakeInt(-7),
		MakeFloat(1.5), MakeFloat(math.Copysign(0, -1)), MakeString("héllo"),
	}
	for _, s := range slots {
		var v Value = s
		var back Slot
		SlotOf(&back, v)
		if !reflect.DeepEqual(back, s) {
			t.Errorf("boundary lost value: %#v -> %#v", s, back)
		}
	}
}

func mustDecode(h string) string {
	b, err := hexDecode(h)
	if err != nil {
		return h
	}
	return string(b)
}
