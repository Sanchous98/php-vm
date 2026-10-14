package vm

import (
	"math"
	"testing"
)

// --- Выбор и смена типизированного представления ---

func TestTypedKindSelection(t *testing.T) {
	a := newArray()
	if a.kind != packedGeneric {
		t.Fatalf("пустой массив: kind=%d, want packedGeneric", a.kind)
	}
	if _, err := a.Append(MakeInt(1)); err != nil {
		t.Fatal(err)
	}
	if a.kind != packedInt || len(a.ints) != 1 || len(a.values) != 0 {
		t.Fatalf("после int: kind=%d ints=%d values=%d", a.kind, len(a.ints), len(a.values))
	}
	if _, err := a.Append(MakeFloat(1.5)); err != nil {
		t.Fatal(err)
	}
	if a.kind != packedGeneric || len(a.values) != 2 {
		t.Fatalf("int+float: kind=%d values=%d", a.kind, len(a.values))
	}
	if v, _ := a.Get(intKey(0)); v.Type() != IntType || v.Int() != 1 {
		t.Fatalf("после конверсии [0]=%v", v.ToString())
	}
	if v, _ := a.Get(intKey(1)); v.Type() != FloatType || v.Float() != 1.5 {
		t.Fatalf("после конверсии [1]=%v", v.ToString())
	}

	b := newArray()
	b.Append(MakeFloat(2.5))
	if b.kind != packedFloat {
		t.Fatalf("float: kind=%d", b.kind)
	}
	b.Append(MakeInt(7))
	if b.kind != packedGeneric {
		t.Fatalf("float+int: kind=%d", b.kind)
	}

	c := newArray()
	c.Append(MakeString("x"))
	if c.kind != packedStr {
		t.Fatalf("string: kind=%d", c.kind)
	}
	c.Append(MakeNull())
	if c.kind != packedGeneric {
		t.Fatalf("string+null: kind=%d", c.kind)
	}
}

// Set по последовательному ключу в пустой массив тоже выбирает тип.
func TestTypedSetSelectsKind(t *testing.T) {
	a := newArray()
	if err := a.SetInt(0, MakeInt(5)); err != nil {
		t.Fatal(err)
	}
	if err := a.SetInt(1, MakeInt(6)); err != nil {
		t.Fatal(err)
	}
	if a.kind != packedInt || a.Len() != 2 {
		t.Fatalf("kind=%d len=%d", a.kind, a.Len())
	}
	// Непоследовательный ключ переводит в mixed (не в generic).
	if err := a.SetInt(5, MakeInt(9)); err != nil {
		t.Fatal(err)
	}
	if a.packed || a.Len() != 3 {
		t.Fatalf("после ключа 5: packed=%v len=%d", a.packed, a.Len())
	}
	if v, ok := a.Get(intKey(0)); !ok || v.Int() != 5 {
		t.Fatalf("mixed[0] = %v", v.ToString())
	}
	if a.NextFree() != 6 {
		t.Fatalf("nextFree=%d, want 6", a.NextFree())
	}
}

func TestTypedValueRoundTrip(t *testing.T) {
	a := newArray()
	wantInts := []int64{0, 1, -1, math.MaxInt64, math.MinInt64}
	for _, x := range wantInts {
		if _, err := a.Append(MakeInt(x)); err != nil {
			t.Fatal(err)
		}
	}
	if a.kind != packedInt {
		t.Fatalf("kind=%d", a.kind)
	}
	for i, x := range wantInts {
		v, ok := a.Get(intKey(int64(i)))
		if !ok || v.Type() != IntType || v.Int() != x {
			t.Fatalf("int[%d]=%v, want %d", i, v.ToString(), x)
		}
	}
	if got := a.Values(); len(got) != len(wantInts) {
		t.Fatalf("Values len=%d", len(got))
	}

	f := newArray()
	f.Append(MakeFloat(math.Copysign(0, -1)))
	f.Append(MakeFloat(math.Inf(1)))
	if f.kind != packedFloat {
		t.Fatalf("float kind=%d", f.kind)
	}
	if v, _ := f.Get(intKey(0)); math.Signbit(v.Float()) != true {
		t.Fatalf("-0.0 потерян: %v", v.Float())
	}
	if v, _ := f.Get(intKey(1)); !math.IsInf(v.Float(), 1) {
		t.Fatalf("+Inf потерян")
	}

	s := newArray()
	s.Append(MakeString("hello"))
	s.Append(MakeInternedString("lit"))
	if s.kind != packedStr {
		t.Fatalf("str kind=%d", s.kind)
	}
	if v, _ := s.Get(intKey(0)); v.StringVal() != "hello" {
		t.Fatalf("str[0]=%q", v.StringVal())
	}
	// Бит interned сохраняется (pstr несёт те же ptr/num).
	if v, _ := s.Get(intKey(1)); !StringSlotInterned(v) {
		t.Fatalf("interned-бит потерян")
	}
	if got, ok := s.StrAt(0); !ok || got != "hello" {
		t.Fatalf("StrAt=%q ok=%v", got, ok)
	}
}

// --- COW ---

func TestTypedCOWUnshare(t *testing.T) {
	a := newArray()
	for i := 0; i < 100; i++ {
		a.Append(MakeInt(int64(i)))
	}
	a.MarkShared()
	b := a.Unshare()
	if b == a {
		t.Fatal("Unshare вернул тот же массив")
	}
	if b.kind != packedInt || len(b.ints) != 100 {
		t.Fatalf("копия: kind=%d len=%d", b.kind, len(b.ints))
	}
	// Запись в копию не меняет оригинал (общий буфер не должен использоваться).
	b.ints[0] = 999
	if a.ints[0] != 0 {
		t.Fatalf("запись в копию протекла в оригинал: %d", a.ints[0])
	}
	// После Unshare оригинал остаётся разделённым.
	if !a.Shared() {
		t.Fatal("оригинал потерял shared")
	}
}

// COW на уровне массива: копия после Unshare пишет в своё хранилище.
func TestTypedCopyOnWriteValue(t *testing.T) {
	a := newArray()
	a.Append(MakeInt(1))
	a.Append(MakeInt(2))
	a.MarkShared()
	b := a.Unshare()
	if err := b.SetInt(0, MakeInt(10)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Append(MakeInt(3)); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.Get(intKey(0)); v.Int() != 1 || a.Len() != 2 {
		t.Fatalf("оригинал изменён: %v len=%d", v.ToString(), a.Len())
	}
	if v, _ := b.Get(intKey(0)); v.Int() != 10 || b.Len() != 3 {
		t.Fatalf("копия не изменена: %v len=%d", v.ToString(), b.Len())
	}
}

// --- Ссылки ---

// Взятие ссылки на элемент переводит массив в generic (RefType не хранится
// типизированно), значение и семантика ссылки сохраняются.
func TestTypedRefForcesGeneric(t *testing.T) {
	a := newArray()
	a.Append(MakeInt(1))
	a.Append(MakeInt(2))
	cell := MakeRef(&Ref{v: MakeInt(7)})
	if err := a.SetInt(0, cell); err != nil {
		t.Fatal(err)
	}
	if a.kind != packedGeneric {
		t.Fatalf("после записи ссылки kind=%d", a.kind)
	}
	v, ok := a.Get(intKey(1))
	if !ok || v.Int() != 2 {
		t.Fatalf("соседний элемент потерян: %v", v.ToString())
	}
	got, ok := a.Get(intKey(0))
	if !ok || got.typ != RefType || got.Ref().Value().Int() != 7 {
		t.Fatalf("ссылка не сохранена: %v", got.Type())
	}
}

// --- unset / дыры / nextFree ---

func TestTypedUnsetGoesMixed(t *testing.T) {
	a := newArray()
	for i := 0; i < 5; i++ {
		a.Append(MakeInt(int64(i * 10)))
	}
	a.UnsetInt(1)
	if a.packed {
		t.Fatal("unset не перевёл в mixed")
	}
	if a.Len() != 4 {
		t.Fatalf("len=%d", a.Len())
	}
	if _, ok := a.Get(intKey(1)); ok {
		t.Fatal("удалённый элемент найден")
	}
	if k, _ := a.Key(); k.IntVal() != 0 {
		t.Fatalf("key=%v", k.IntVal())
	}
	// Порядок после дыры: 0,2,3,4.
	var keys []int64
	for k := range a.All() {
		keys = append(keys, k.IntVal())
	}
	want := []int64{0, 2, 3, 4}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys=%v, want %v", keys, want)
		}
	}
	if a.NextFree() != 5 {
		t.Fatalf("nextFree=%d", a.NextFree())
	}
	if k, err := a.Append(MakeInt(99)); err != nil || k != 5 {
		t.Fatalf("append после unset: k=%d err=%v", k, err)
	}
}

// --- Указатель массива (current/next/...) на типизированном хранилище ---

func TestTypedInternalPointer(t *testing.T) {
	a := newArray()
	for i := 0; i < 3; i++ {
		a.Append(MakeInt(int64(i + 1)))
	}
	// Свежий массив: внутренний указатель уже на первом элементе (как в PHP).
	if v, ok := a.Current(); !ok || v.Int() != 1 {
		t.Fatalf("current до reset=%v ok=%v", v.ToString(), ok)
	}
	a.Reset()
	if v, _ := a.Current(); v.Int() != 1 {
		t.Fatalf("current=%v", v.ToString())
	}
	a.Next()
	if k, _ := a.Key(); k.IntVal() != 1 {
		t.Fatalf("key=%v", k.IntVal())
	}
	a.End()
	if v, _ := a.Current(); v.Int() != 3 {
		t.Fatalf("end current=%v", v.ToString())
	}
	a.Prev()
	if v, _ := a.Current(); v.Int() != 2 {
		t.Fatalf("prev current=%v", v.ToString())
	}
}

// --- Сортировка строк на месте ---

func TestSortStrsPreservesInterned(t *testing.T) {
	a := newArray()
	a.Append(MakeInternedString("b"))
	a.Append(MakeString("a"))
	a.Append(MakeInternedString("c"))
	if !a.SortStrs(false) {
		t.Fatal("SortStrs вернул false для packedStr")
	}
	if v, _ := a.Get(intKey(0)); v.StringVal() != "a" {
		t.Fatalf("[0]=%q", v.StringVal())
	}
	if v, _ := a.Get(intKey(2)); v.StringVal() != "c" || !StringSlotInterned(v) {
		t.Fatalf("[2]=%q interned=%v", v.StringVal(), StringSlotInterned(v))
	}
	if a.SortStrs(true) {
		if v, _ := a.Get(intKey(0)); v.StringVal() != "c" {
			t.Fatalf("desc [0]=%q", v.StringVal())
		}
	}
}

// --- Рост и ReserveInts ---

func TestReserveIntsAndGrowth(t *testing.T) {
	a := newArray()
	a.ReserveInts(1000)
	if a.kind != packedInt || cap(a.ints) != 1000 {
		t.Fatalf("ReserveInts: kind=%d cap=%d", a.kind, cap(a.ints))
	}
	ptr := &a.ints[0:1][0]
	for i := 0; i < 1000; i++ {
		if err := a.AppendInt(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if cap(a.ints) != 1000 {
		t.Fatalf("ёмкость изменилась: %d", cap(a.ints))
	}
	// За границей резерва рост удвоением.
	for i := 1000; i < 2000; i++ {
		if err := a.AppendInt(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if cap(a.ints) != 2000 {
		t.Fatalf("после роста cap=%d, want 2000", cap(a.ints))
	}
	if a.Len() != 2000 {
		t.Fatalf("len=%d", a.Len())
	}
	_ = ptr
}

// --- Совместимость с mixed-представлением ---

func TestTypedToMixedKeepsValues(t *testing.T) {
	a := newArray()
	for i := 0; i < 4; i++ {
		a.Append(MakeFloat(float64(i) / 2))
	}
	a.toMixed()
	if a.packed || a.Len() != 4 || a.NextFree() != 4 {
		t.Fatalf("packed=%v len=%d nf=%d", a.packed, a.Len(), a.NextFree())
	}
	for i := 0; i < 4; i++ {
		v, ok := a.Get(intKey(int64(i)))
		if !ok || v.Float() != float64(i)/2 {
			t.Fatalf("mixed[%d]=%v", i, v.ToString())
		}
	}
	if a.entries[0].key.IsStr() {
		t.Fatal("IsStr на int-ключе")
	}
}

// Set по строковому ключу сразу переводит в mixed.
func TestTypedStringKeyGoesMixed(t *testing.T) {
	a := newArray()
	a.Append(MakeInt(1))
	if err := a.SetStr("k", MakeInt(2)); err != nil {
		t.Fatal(err)
	}
	if a.packed {
		t.Fatal("строковый ключ не перевёл в mixed")
	}
	if v, ok := a.Get(MakeStrKey("k")); !ok || v.Int() != 2 {
		t.Fatalf("mixed[k]=%v", v.ToString())
	}
	if v, ok := a.Get(intKey(0)); !ok || v.Int() != 1 {
		t.Fatalf("mixed[0]=%v", v.ToString())
	}
}
