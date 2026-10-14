package vm

import (
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// --- Буфер строк: базовые свойства ---

func TestStrBufAppendInPlace(t *testing.T) {
	s := appendStringSlot(MakeNull(), "", "abc")
	if !StringSlotIsBuf(s) {
		t.Fatal("результат дописывания должен быть буфером")
	}
	if got := s.StringVal(); got != "abc" {
		t.Fatalf("got %q", got)
	}
	ptr := s.ptr
	s2 := appendStringSlot(s, s.StringVal(), "def")
	if s2.ptr != ptr {
		t.Fatal("дописывание в пределах ёмкости должно идти на месте (тот же буфер)")
	}
	if got := s2.StringVal(); got != "abcdef" {
		t.Fatalf("got %q", got)
	}
	// Старый слот видит только свой префикс — хвост ему не принадлежит.
	if got := s.StringVal(); got != "abc" {
		t.Fatalf("старый слот изменился: %q", got)
	}
	if s.num&bufBit == 0 {
		t.Fatal("bufBit потерян")
	}
	// Обычная строка, сделанная из содержимого буфера, — независимый слот:
	// дописывание в буфер не меняет её видимый префикс (пишем за used).
	plain := MakeString(s2.StringVal())
	if StringSlotIsBuf(plain) {
		t.Fatal("MakeString не должен помечать слот буфером")
	}
	s3 := appendStringSlot(s2, s2.StringVal(), "XYZ")
	if got := plain.StringVal(); got != "abcdef" {
		t.Fatalf("plain-строка изменилась: %q", got)
	}
	if got := s3.StringVal(); got != "abcdefXYZ" {
		t.Fatalf("got %q", got)
	}
}

func TestStrBufGrowthAndCOW(t *testing.T) {
	s := appendStringSlot(MakeNull(), "", "0123456789")
	// Копия слота (как передача по значению).
	c := s
	// Первый владелец дописывает: копия не должна это увидеть.
	s = appendStringSlot(s, s.StringVal(), "A")
	if got := c.StringVal(); got != "0123456789" {
		t.Fatalf("копия изменилась: %q", got)
	}
	if got := s.StringVal(); got != "0123456789A" {
		t.Fatalf("владелец: %q", got)
	}
	// Теперь дописывает копия: её длина меньше used — должна получиться
	// независимая строка (COW по фактическому разделению).
	c = appendStringSlot(c, c.StringVal(), "B")
	if got := c.StringVal(); got != "0123456789B" {
		t.Fatalf("COW-копия: %q", got)
	}
	if got := s.StringVal(); got != "0123456789A" {
		t.Fatalf("владелец после COW: %q", got)
	}
	// Рост: длинное дописывание переводит слот на новый буфер.
	old := s.ptr
	big := make([]byte, 4096)
	for i := range big {
		big[i] = 'z'
	}
	s = appendStringSlot(s, s.StringVal(), string(big))
	if s.ptr == old {
		t.Fatal("после переполнения ёмкости буфер обязан переехать")
	}
	if got := len(s.StringVal()); got != 11+4096 {
		t.Fatalf("длина после роста: %d", got)
	}
	if got := s.StringVal()[11:]; got != string(big) {
		t.Fatal("данные после роста повреждены")
	}
}

func TestStrBufEmptyAndNil(t *testing.T) {
	s := appendStringSlot(MakeNull(), "", "")
	if got := s.StringVal(); got != "" {
		t.Fatalf("пустой результат: %q", got)
	}
	if StringSlotIsBuf(s) {
		t.Fatal("пустая строка не должна становиться буфером")
	}
	// Пустая строка + непустой хвост.
	s = appendStringSlot(MakeString(""), "", "x")
	if got := s.StringVal(); got != "x" {
		t.Fatalf("got %q", got)
	}
	// Дописывание пустого хвоста к буферу: слот не портится.
	s = appendStringSlot(s, s.StringVal(), "")
	if got := s.StringVal(); got != "x" {
		t.Fatalf("got %q", got)
	}
	// Не-строка (null) с непустым хвостом.
	n := appendStringSlot(MakeNull(), "", "42")
	if got := n.StringVal(); got != "42" {
		t.Fatalf("null+42: %q", got)
	}
}

func TestStrBufLarge(t *testing.T) {
	const n = 1 << 20
	s := appendStringSlot(MakeNull(), "", "seed")
	for len(s.StringVal()) < n {
		s = appendStringSlot(s, s.StringVal(), s.StringVal())
	}
	if got := len(s.StringVal()); got < n {
		t.Fatalf("длина %d < %d", got, n)
	}
	// Верхние байты не повреждены.
	v := s.StringVal()
	if v[:4] != "seed" {
		t.Fatalf("начало повреждено: %q", v[:4])
	}
	if v[len(v)-4:] != v[:4] {
		t.Fatalf("конец повреждён: %q", v[len(v)-4:])
	}
}

func TestStrBufSelfAppend(t *testing.T) {
	s := appendStringSlot(MakeNull(), "", "ab")
	for i := 0; i < 5; i++ {
		ls := s.StringVal()
		s = appendStringSlot(s, ls, ls)
	}
	want := ""
	for i := 0; i < 32; i++ {
		want += "ab"
	}
	if got := s.StringVal(); got != want {
		t.Fatalf("самоприписывание: len=%d want=%d", len(got), len(want))
	}
}

// Буфер держится интерьерным указателем: GC не должен его освобождать, пока
// жив слот, даже если наружу выдан только указатель на середину объекта.
func TestStrBufParallelGC(t *testing.T) {
	const workers = 4
	const perWorker = 4000
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	stop := make(chan struct{})
	go func() {
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
				s := appendStringSlot(MakeNull(), "", strconv.Itoa(w)+":"+strconv.Itoa(i)+":")
				for j := 0; j < 8; j++ {
					s = appendStringSlot(s, s.StringVal(), strconv.Itoa(j))
				}
				slots[i] = s
			}
			runtime.GC()
			for i := range slots {
				want := strconv.Itoa(w) + ":" + strconv.Itoa(i) + ":01234567"
				if got := slots[i].StringVal(); got != want {
					errs <- "worker " + strconv.Itoa(w) + " слот " + strconv.Itoa(i) + ": " + got
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
