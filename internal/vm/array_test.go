package vm

import (
	"math"
	"testing"
)

// keysInOrder возвращает «живые» ключи в порядке вставки (через итератор).
func keysInOrder(a *Array) []string {
	var out []string
	for k := range a.All() {
		out = append(out, k.phpString())
	}
	return out
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Приведение ключей — значения сверены с php 8.3 (docs/array-object.md §5).
func TestArrayKeyCoercion(t *testing.T) {
	cases := []struct {
		in   Slot
		want ArrayKey
	}{
		{MakeString("10"), intKey(10)},
		{MakeString("01"), stringKey("01")},
		{MakeString("1e2"), stringKey("1e2")},
		{MakeString("+5"), stringKey("+5")},
		{MakeString("-5"), intKey(-5)},
		{MakeString(" 10"), stringKey(" 10")},
		{MakeString("010"), stringKey("010")},
		{MakeString("0"), intKey(0)},
		{MakeString("-0"), stringKey("-0")},
		{MakeString("00"), stringKey("00")},
		{MakeString(""), stringKey("")},
		{MakeString("9999999999999999999"), stringKey("9999999999999999999")},
		{MakeString("-9223372036854775808"), intKey(math.MinInt64)},
		{MakeString("1.0"), stringKey("1.0")},
		{MakeString("1."), stringKey("1.")},
		{MakeInt(7), intKey(7)},
		{MakeFloat(1.9), intKey(1)},
		{MakeBool(true), intKey(1)},
		{MakeBool(false), intKey(0)},
		{MakeNull(), stringKey("")},
	}
	for _, c := range cases {
		got, ok := keyFromSlot(c.in)
		if !ok || got != c.want {
			t.Errorf("keyFromSlot(%v) = %+v,%v; want %+v", c.in, got, ok, c.want)
		}
	}
}

// Порядок вставки + nextFree (проверено на php).
func TestArrayOrderAndNextFree(t *testing.T) {
	a := newArray()
	a.Set(stringKey("x"), MakeInt(1))
	a.Set(intKey(10), MakeInt(2))
	a.Set(stringKey("y"), MakeInt(3))
	a.Append(MakeInt(4)) // авто-ключ = nextFree = 11
	if got := keysInOrder(a); !equalKeys(got, []string{"x", "10", "y", "11"}) {
		t.Fatalf("order = %v", got)
	}

	// tombstone + nextFree не уменьшается после unset
	b := newArray()
	b.Set(intKey(0), MakeInt(1))
	b.Set(intKey(1), MakeInt(2))
	b.Set(intKey(2), MakeInt(3))
	b.Unset(intKey(1))
	b.Append(MakeInt(9)) // 4-й авто-ключ = 3
	if got := keysInOrder(b); !equalKeys(got, []string{"0", "2", "3"}) {
		t.Fatalf("order after unset = %v", got)
	}
	if b.Len() != 3 {
		t.Fatalf("Len = %d, want 3", b.Len())
	}
}

// Переход packed -> mixed при непоследовательном ключе.
func TestArrayPackedToMixed(t *testing.T) {
	a := newArray()
	a.Set(intKey(0), MakeInt(1))
	a.Set(intKey(1), MakeInt(2))
	a.Set(intKey(2), MakeInt(3))
	if !a.packed {
		t.Fatal("последовательные 0,1,2 должны остаться packed")
	}
	a.Set(intKey(5), MakeInt(6)) // разрыв -> mixed
	if a.packed {
		t.Fatal("должен стать mixed")
	}
	if got := keysInOrder(a); !equalKeys(got, []string{"0", "1", "2", "5"}) {
		t.Fatalf("order = %v", got)
	}
	a.Append(MakeInt(7)) // nextFree = 6
	if got := keysInOrder(a); !equalKeys(got, []string{"0", "1", "2", "5", "6"}) {
		t.Fatalf("order after append = %v", got)
	}
}

// Отрицательные int-ключи: правило nextFree в PHP 8.3 (сверено с php).
func TestArrayNegativeKeys(t *testing.T) {
	check := func(name string, f func() *Array, want []string) {
		t.Helper()
		if got := keysInOrder(f()); !equalKeys(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	check("[-5] []", func() *Array {
		a := newArray()
		a.Set(intKey(-5), MakeInt(1))
		a.Append(MakeInt(2))
		return a
	}, []string{"-5", "-4"})
	check("[-5][-10] []", func() *Array {
		a := newArray()
		a.Set(intKey(-5), MakeInt(1))
		a.Set(intKey(-10), MakeInt(2))
		a.Append(MakeInt(3))
		return a
	}, []string{"-5", "-10", "-4"})
	check(`["x"][-5] []`, func() *Array {
		a := newArray()
		a.Set(stringKey("x"), MakeInt(1))
		a.Set(intKey(-5), MakeInt(2))
		a.Append(MakeInt(3))
		return a
	}, []string{"x", "-5", "-4"})
	check("[-5][0] []", func() *Array {
		a := newArray()
		a.Set(intKey(-5), MakeInt(1))
		a.Set(intKey(0), MakeInt(2))
		a.Append(MakeInt(3))
		return a
	}, []string{"-5", "0", "1"})
}

// packed-массив не выделяет карту индекса вообще; карта появляется только в
// mixed. Это и есть причина, почему packed дёшев по памяти и быстр на чтение.
func TestArrayMapOnlyForMixed(t *testing.T) {
	a := newArray()
	for i := 0; i < 100; i++ {
		a.Append(MakeInt(int64(i)))
	}
	if !a.packed {
		t.Fatal("Append должен оставаться packed")
	}
	if a.mStr != nil || a.mInt != nil {
		t.Fatal("packed-массив не должен выделять карту индекса")
	}

	// строковый ключ -> mixed, появляется mStr
	a.Set(stringKey("x"), MakeInt(1))
	if a.packed {
		t.Fatal("после строкового ключа массив должен стать mixed")
	}
	if a.mStr == nil {
		t.Fatal("mixed со строковым ключом должен иметь mStr")
	}

	// int-ключ с разрывом -> mixed, появляется mInt
	b := newArray()
	b.Append(MakeInt(1))
	b.Set(intKey(10), MakeInt(2))
	if b.packed {
		t.Fatal("разрыв int-ключа должен перевести в mixed")
	}
	if b.mInt == nil {
		t.Fatal("mixed с int-ключом должен иметь mInt")
	}
}

// bool/null/float ключи приводятся к int/string при вставке (проверено на php):
// true/1, false/0, null/"", 1.9/1 — это ОДИН ключ. Отдельных bool/null-ключей нет.
func TestArrayKeyNormalization(t *testing.T) {
	a := newArray()
	set := func(s Slot, v int64) {
		k, ok := keyFromSlot(s)
		if !ok {
			t.Fatalf("недопустимый ключ %v", s)
		}
		a.Set(k, MakeInt(v))
	}
	get := func(s Slot) int64 {
		k, _ := keyFromSlot(s)
		v, _ := a.Get(k)
		return v.Int()
	}

	set(MakeBool(true), 1)
	set(MakeInt(1), 2) // тот же ключ int 1 -> перезапись
	if got := get(MakeBool(true)); got != 2 {
		t.Errorf("true и 1 должны совпадать: got %d", got)
	}

	set(MakeBool(false), 10)
	set(MakeInt(0), 20) // тот же ключ int 0
	if got := get(MakeBool(false)); got != 20 {
		t.Errorf("false и 0 должны совпадать: got %d", got)
	}

	set(MakeNull(), 100)
	set(MakeString(""), 200) // тот же ключ ""
	if got := get(MakeNull()); got != 200 {
		t.Errorf(`null и "" должны совпадать: got %d`, got)
	}

	set(MakeFloat(1.9), 7) // -> int 1
	if got := get(MakeInt(1)); got != 7 {
		t.Errorf("1.9 -> int 1: got %d", got)
	}
}

// Переполнение nextFree: ключ PHP_INT_MAX -> следующий авто-ключ переполняет
// int64, PHP бросает Error. Ключ PHP_INT_MIN -> следующий = MinInt64+1.
func TestArrayNextFreeOverflow(t *testing.T) {
	a := newArray()
	if err := a.Set(intKey(math.MaxInt64), MakeInt(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Append(MakeInt(2)); err != errNextOccupied {
		t.Fatalf("ожидали errNextOccupied, got %v", err)
	}

	b := newArray()
	if err := b.Set(intKey(math.MinInt64), MakeInt(1)); err != nil {
		t.Fatal(err)
	}
	k, err := b.Append(MakeInt(2))
	if err != nil {
		t.Fatal(err)
	}
	if k != math.MinInt64+1 {
		t.Fatalf("key = %d, want %d", k, int64(math.MinInt64+1))
	}
}

// Внутренний указатель: семантика сверена с php (reset/next/prev/end/current/key,
// копирование, unset под указателем).
func TestArrayInternalPointer(t *testing.T) {
	must := func(v Slot, ok bool) int64 {
		if !ok {
			t.Fatalf("ожидали значение, получили false")
		}
		return v.Int()
	}

	a := newArray()
	a.Append(MakeInt(10))
	a.Append(MakeInt(20))
	a.Append(MakeInt(30))
	a.Reset()
	if got := must(a.Current()); got != 10 {
		t.Fatalf("reset: %d", got)
	}
	a.Next()
	if got := must(a.Current()); got != 20 {
		t.Fatalf("next: %d", got)
	}
	if k, _ := a.Key(); k.i != 1 {
		t.Fatalf("key: %v", k)
	}
	a.End()
	if got := must(a.Current()); got != 30 {
		t.Fatalf("end: %d", got)
	}
	a.Prev()
	if got := must(a.Current()); got != 20 {
		t.Fatalf("prev: %d", got)
	}

	// за конец
	b := newArray()
	b.Append(MakeInt(1))
	b.Append(MakeInt(2))
	b.Reset()
	b.Next()
	if got := must(b.Current()); got != 2 {
		t.Fatalf("b next: %d", got)
	}
	b.Next()
	if _, ok := b.Current(); ok {
		t.Fatal("за концом должно быть false")
	}
	if _, ok := b.Key(); ok {
		t.Fatal("за концом key = null")
	}

	// свежий массив без reset: указатель на первом
	c := newArray()
	c.Append(MakeInt(7))
	c.Append(MakeInt(8))
	c.Append(MakeInt(9))
	if got := must(c.Current()); got != 7 {
		t.Fatalf("fresh current: %d", got)
	}
	if k, ok := c.Key(); !ok || k.i != 0 {
		t.Fatalf("fresh key: %v %v", k, ok)
	}

	// unset под указателем (середина) -> на следующий живой
	d := newArray()
	d.Set(stringKey("x"), MakeInt(1))
	d.Set(stringKey("y"), MakeInt(2))
	d.Set(stringKey("z"), MakeInt(3))
	d.Reset()
	d.Next() // y
	d.Unset(stringKey("y"))
	if got := must(d.Current()); got != 3 {
		t.Fatalf("unset-mid current: %d", got)
	}
	if k, ok := d.Key(); !ok || k.s != "z" {
		t.Fatalf("unset-mid key: %v %v", k, ok)
	}
	d.Prev()
	if got := must(d.Current()); got != 1 {
		t.Fatalf("unset-mid prev: %d", got)
	}

	// unset под указателем (последний) -> за конец
	e := newArray()
	e.Append(MakeInt(10))
	e.Append(MakeInt(20))
	e.Append(MakeInt(30))
	e.Reset()
	e.Next()
	e.Next() // 30
	e.Unset(intKey(2))
	if _, ok := e.Current(); ok {
		t.Fatal("unset-last должно быть за концом")
	}

	// unset до указателя не двигает указатель
	f := newArray()
	f.Set(stringKey("x"), MakeInt(1))
	f.Set(stringKey("y"), MakeInt(2))
	f.Set(stringKey("z"), MakeInt(3))
	f.Reset()
	f.Next()
	f.Next() // z
	f.Unset(stringKey("x"))
	if k, _ := f.Key(); k.s != "z" {
		t.Fatalf("unset-before не должен двигать указатель: %v", k)
	}

	// COW: next на копии не влияет на оригинал
	g := newArray()
	g.Append(MakeInt(1))
	g.Append(MakeInt(2))
	g.Append(MakeInt(3))
	g.MarkShared()
	h := g.Unshare()
	h.Next()
	h.Next()
	if got := must(h.Current()); got != 3 {
		t.Fatalf("copy current: %d", got)
	}
	if got := must(g.Current()); got != 1 {
		t.Fatalf("оригинал изменился: %d", got)
	}
}

// COW: разделённый массив клонируется при записи.
func TestArrayCOW(t *testing.T) {
	a := newArray()
	a.Set(intKey(0), MakeInt(1))

	// $b = $a
	a.MarkShared()
	b := a.Unshare()
	b.Set(intKey(0), MakeInt(99))

	if v, _ := a.Get(intKey(0)); v.Int() != 1 {
		t.Errorf("a[0] = %d, want 1 (COW нарушен)", v.Int())
	}
	if v, _ := b.Get(intKey(0)); v.Int() != 99 {
		t.Errorf("b[0] = %d, want 99", v.Int())
	}
}

// Вложенный COW: $b=$a; $b[1][0]=99 -> $a[1][0] остаётся прежним.
func TestArrayCOWNested(t *testing.T) {
	inner := newArray()
	inner.Set(intKey(0), MakeInt(2))
	inner.Set(intKey(1), MakeInt(3))

	a := newArray()
	a.Set(intKey(0), MakeInt(1))
	a.Set(intKey(1), MakeArray(inner))

	// $b = $a
	a.MarkShared()
	b := a.Unshare()

	// $b[1][0] = 99
	innerSlot, _ := b.Get(intKey(1))
	innerB := innerSlot.AsArray().Unshare()
	innerB.Set(intKey(0), MakeInt(99))
	b.Set(intKey(1), MakeArray(innerB))

	aInner, _ := a.Get(intKey(1))
	if v, _ := aInner.AsArray().Get(intKey(0)); v.Int() != 2 {
		t.Errorf("a[1][0] = %d, want 2", v.Int())
	}
	if v, _ := innerB.Get(intKey(0)); v.Int() != 99 {
		t.Errorf("b[1][0] = %d, want 99", v.Int())
	}
}
