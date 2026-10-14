package vm

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func liveHeapObjects() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapObjects
}

// allocCyclePairs создаёт 50k пар объектов в цикле (a↔b через Props),
// удерживаемых слотами, и возвращает их. noinline — чтобы слоты не были
// оптимизированы как мёртвые до возврата.
//
//go:noinline
func allocCyclePairs() []Slot {
	slots := make([]Slot, 0, 100000)
	for i := 0; i < 50000; i++ {
		a := &Object{}
		b := &Object{}
		a.props = map[string]Value{"b": MakeObject(b)}
		b.props = map[string]Value{"a": MakeObject(a)}
		slots = append(slots, MakeObject(a), MakeObject(b))
	}
	runtime.KeepAlive(slots)
	return slots
}

//go:noinline
func allocStrings() []Slot {
	slots := make([]Slot, 100000)
	for i := range slots {
		// Данные строки обязаны лежать в heap: литерал живёт в .rodata, и слот
		// на него не «удерживает» ни одного heap-объекта (после инлайнинга строк
		// это ровно так — поэтому конкатенация в рантайме).
		slots[i] = MakeString("hello, world " + strconv.Itoa(i))
	}
	runtime.KeepAlive(slots)
	return slots
}

// Слоты держат heap-значения указателем (Slot.ptr), поэтому владелец памяти —
// Go GC: значение живёт, пока на него ссылается живой слот, и собирается —
// включая циклы — когда ссылок не остаётся. Своего heap/refcount нет.
func TestSlotOwnsHeapValues(t *testing.T) {
	runtime.GC()
	runtime.GC()
	baseline := liveHeapObjects()

	slots := allocCyclePairs()
	runtime.GC()
	held := liveHeapObjects()
	runtime.KeepAlive(slots)
	if held <= baseline {
		t.Fatalf("слоты не удержали heap-значения: %d <= %d", held, baseline)
	}

	// Убираем все слоты -> циклы должны быть собраны Go GC.
	slots = nil
	_ = slots
	runtime.GC()
	runtime.GC()
	if got := liveHeapObjects(); got > baseline+1000 {
		t.Fatalf("Go GC не собрал heap-значения с циклами: %d > %d", got, baseline)
	}
}

// Строки тоже живут за указателем и собираются после сброса слотов.
func TestSlotOwnsStrings(t *testing.T) {
	runtime.GC()
	runtime.GC()
	baseline := liveHeapObjects()

	slots := allocStrings()
	runtime.GC()
	held := liveHeapObjects()
	runtime.KeepAlive(slots)
	if held <= baseline {
		t.Fatal("слоты не удержали строки")
	}

	slots = nil
	_ = slots
	runtime.GC()
	runtime.GC()
	if got := liveHeapObjects(); got > baseline+1000 {
		t.Fatalf("строки не собраны: %d > %d", got, baseline)
	}
}

// allocHeapStrings создаёт слоты со строками, чьи данные лежат в heap (не
// литерал): результат конкатенации в цикле.
//
//go:noinline
func allocHeapStrings(n int) []Slot {
	slots := make([]Slot, n)
	for i := range slots {
		slots[i] = MakeString(strconv.Itoa(i) + ":payload-" + strconv.Itoa(i*7))
	}
	runtime.KeepAlive(slots)
	return slots
}

// TestSlotStringDataSurvivesGC: значение строки хранится прямо в слоте
// (ptr — интерьерный указатель на heap-данные, num — длина). GC обязан держать
// массив байт живым, пока жив слот, и не портить его: после серии GC все
// строки читаются без изменений.
func TestSlotStringDataSurvivesGC(t *testing.T) {
	const n = 50000
	slots := allocHeapStrings(n)
	// Давление на GC: аллокации между сборками не должны «съесть» данные.
	other := allocHeapStrings(1024)
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(other)
	for i := range slots {
		want := strconv.Itoa(i) + ":payload-" + strconv.Itoa(i*7)
		if got := slots[i].StringVal(); got != want {
			t.Fatalf("слот %d: данные строки повреждены: %q != %q", i, got, want)
		}
		if StringSlotInterned(slots[i]) {
			t.Fatalf("слот %d: неинтернированная строка помечена interned", i)
		}
	}
	// Пустая строка и интернированная строка (литерал) — тоже переживают GC.
	slots[0] = MakeString("")
	slots[1] = MakeInternedString("interned-literal")
	runtime.GC()
	if got := slots[0].StringVal(); got != "" {
		t.Fatalf("пустая строка не пережила GC: %q", got)
	}
	if got := slots[1].StringVal(); got != "interned-literal" || !StringSlotInterned(slots[1]) {
		t.Fatalf("interned-строка не пережила GC: %q interned=%v", got, StringSlotInterned(slots[1]))
	}
	slots = nil
	_ = slots
	runtime.GC()
	runtime.GC()
}

// TestSlotStringDataParallelGC: параллельная аллокация строковых слотов при
// постоянно работающем GC. Каждая горутина пишет данные в свои слоты, читает
// их обратно и сверяет содержимое — интерьерные указатели на данные строк не
// должны теряться или повреждаться сборщиком.
func TestSlotStringDataParallelGC(t *testing.T) {
	const workers = 4
	const perWorker = 20000
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	stop := make(chan struct{})
	go func() {
		// Гоним GC, пока воркеры аллоцируют и читают.
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
				time.Sleep(50 * time.Microsecond)
			}
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			slots := make([]Slot, perWorker)
			for i := range slots {
				slots[i] = MakeString(strconv.Itoa(w) + ":" + strconv.Itoa(i) + ":payload")
			}
			runtime.GC()
			for i := range slots {
				want := strconv.Itoa(w) + ":" + strconv.Itoa(i) + ":payload"
				if got := slots[i].StringVal(); got != want {
					errs <- "worker " + strconv.Itoa(w) + " слот " + strconv.Itoa(i) + ": данные повреждены"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

// TestSlotStringLengthMask: длина, бит interned и маска длины не пересекаются
// для больших строк (проверяем на границе, где старый бокс точно не при чём).
func TestSlotStringLengthMask(t *testing.T) {
	if lenMask|bufBit|internedBit != ^uint64(0) {
		t.Fatalf("lenMask|bufBit|internedBit != все биты: %x %x %x", lenMask, bufBit, internedBit)
	}
	big := strings.Repeat("x", 1<<20)
	s := MakeString(big)
	if s.Raw() != 1<<20 {
		t.Fatalf("длина в num: %d", s.Raw())
	}
	if got := s.StringVal(); got != big {
		t.Fatalf("большая строка прочитана неверно: len=%d", len(got))
	}
	is := MakeInternedString(big)
	if is.Raw() != uint64(1<<20)|internedBit || !StringSlotInterned(is) {
		t.Fatalf("interned-бит: raw=%x", is.Raw())
	}
	if got := is.StringVal(); got != big {
		t.Fatalf("interned-большая строка прочитана неверно: len=%d", len(got))
	}
}

// Context.This[T] восстанавливает типизированный указатель на данные класса
// (шапка Object лежит первым полем -> адрес объекта == адрес данных).
type gcThisData struct {
	Object
	v int
}

func TestContextThis(t *testing.T) {
	var ctx Context
	if ctx.This[gcThisData]() != nil {
		t.Fatal("this должен быть нулевым при пустом слоте")
	}
	o := &gcThisData{v: 7}
	ctx.this = MakeObject(&o.Object)
	if got := ctx.This[gcThisData](); got == nil || got.v != 7 {
		t.Fatalf("This вернул не данные: %+v", got)
	}
}
