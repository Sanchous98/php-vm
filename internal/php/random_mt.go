package php

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"phpvm/internal/vm"
)

// Реализация Mersenne Twister (MT19937) в точности как PHP.
//
// mt_srand()/srand() сидируют общий генератор; mt_rand()/rand() берут из него.
// Алгоритм повторяет ext/random/engine_mt19937.c (режим MT_RAND_MT19937):
// нестандартный порядок перезагрузки состояния, из-за которого
// mt_srand(42); mt_rand() даёт 804318771.
const (
	mtN = 624
	mtM = 397
)

type mtState struct {
	state [mtN]uint32
	count int
}

var (
	mtMu  sync.Mutex
	mtGen mtState
	// mtSeeded — был ли генератор явно засеян; иначе сидируем по умолчанию
	// при первом обращении.
	mtSeeded bool
)

func (m *mtState) reload() {
	p := 0
	for i := mtN - mtM; i > 0; i-- {
		u := m.state[p]
		v := m.state[(p+1)%mtN]
		mix := (u & 0x80000000) | (v & 0x7fffffff)
		x := m.state[p+mtM] ^ (mix >> 1)
		if v&1 != 0 {
			x ^= 0x9908b0df
		}
		m.state[p] = x
		p++
	}
	for i := mtM; i > 1; i-- {
		u := m.state[p]
		v := m.state[(p+1)%mtN]
		mix := (u & 0x80000000) | (v & 0x7fffffff)
		x := m.state[p+mtM-mtN] ^ (mix >> 1)
		if v&1 != 0 {
			x ^= 0x9908b0df
		}
		m.state[p] = x
		p++
	}
	u := m.state[p]
	v := m.state[0]
	mix := (u & 0x80000000) | (v & 0x7fffffff)
	x := m.state[p+mtM-mtN] ^ (mix >> 1)
	if v&1 != 0 {
		x ^= 0x9908b0df
	}
	m.state[p] = x
	m.count = 0
}

func (m *mtState) seed(s uint32) {
	m.state[0] = s
	for i := 1; i < mtN; i++ {
		prev := m.state[i-1]
		m.state[i] = 1812433253*(prev^(prev>>30)) + uint32(i)
	}
	m.count = mtN
	m.reload()
}

func (m *mtState) generate() uint32 {
	if m.count >= mtN {
		m.reload()
	}
	s1 := m.state[m.count]
	m.count++
	s1 ^= s1 >> 11
	s1 ^= (s1 << 7) & 0x9d2c5680
	s1 ^= (s1 << 15) & 0xefc60000
	return s1 ^ (s1 >> 18)
}

// range32 повторяет php_random_range32.
func (m *mtState) range32(umax uint32) uint32 {
	result := m.generate()
	if umax == 0xffffffff {
		return result
	}
	umax++
	if umax&(umax-1) == 0 {
		return result & (umax - 1)
	}
	limit := uint32(0xffffffff) - (uint32(0xffffffff) % umax) - 1
	for result > limit {
		result = m.generate()
	}
	return result % umax
}

// mtRandRange повторяет php_mt_rand_range (min/max включительно).
func (m *mtState) mtRandRange(min, max int64) int64 {
	umax := uint64(max) - uint64(min)
	if umax > 0xffffffff {
		// range64: склеиваем два 32-битных значения.
		var result uint64
		result = uint64(m.generate()) | uint64(m.generate())<<32
		if umax == 0xffffffffffffffff {
			return int64(result) + min
		}
		umax++
		if umax&(umax-1) == 0 {
			return int64(result&(umax-1)) + min
		}
		limit := uint64(0xffffffffffffffff) - (uint64(0xffffffffffffffff) % umax) - 1
		for result > limit {
			result = uint64(m.generate()) | uint64(m.generate())<<32
		}
		return int64(result%umax) + min
	}
	return int64(m.range32(uint32(umax))) + min
}

// mtInit сидирует генератор случайным значением (если ещё не засеян).
func mtEnsureSeeded() {
	if mtSeeded {
		return
	}
	mtGen.seed(randomSeed())
	mtSeeded = true
}

func randomSeed() uint32 {
	var b [4]byte
	if _, err := crand.Read(b[:]); err == nil {
		return binary.LittleEndian.Uint32(b[:])
	}
	return uint32(time.Now().UnixNano())
}

// mtReset сбрасывает состояние генератора между программами.
func mtReset() {
	mtMu.Lock()
	mtSeeded = false
	mtGen = mtState{}
	mtMu.Unlock()
}

func mtRandRaw() uint32 {
	mtEnsureSeeded()
	return mtGen.generate()
}

// --- встроенные функции ---------------------------------------------------

func mtSrandFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	mtMu.Lock()
	defer mtMu.Unlock()
	if len(args) == 0 || args[0].Type() == vm.NullType {
		mtGen.seed(randomSeed())
	} else {
		mtGen.seed(uint32(args[0].ToInt()))
	}
	mtSeeded = true
	return vm.MakeNull()
}

func srandFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	mtMu.Lock()
	defer mtMu.Unlock()
	if len(args) == 0 || args[0].Type() == vm.NullType {
		mtGen.seed(randomSeed())
	} else {
		mtGen.seed(uint32(args[0].ToInt()))
	}
	mtSeeded = true
	return vm.MakeNull()
}

func mtRandFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	mtMu.Lock()
	defer mtMu.Unlock()
	if len(args) == 0 {
		return vm.Int(int64(mtRandRaw() >> 1))
	}
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "mt_rand() expects exactly 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	min := args[0].ToInt()
	max := args[1].ToInt()
	if max < min {
		ctx.Throw(vm.NewThrowable("ValueError",
			"mt_rand(): Argument #2 ($max) must be greater than or equal to argument #1 ($min)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Int(mtGen.mtRandRange(min, max))
}

func randFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	mtMu.Lock()
	defer mtMu.Unlock()
	if len(args) == 0 {
		return vm.Int(int64(mtRandRaw() >> 1))
	}
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "rand() expects exactly 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	min := args[0].ToInt()
	max := args[1].ToInt()
	if max < min {
		return vm.Int(mtGen.mtRandRange(max, min))
	}
	return vm.Int(mtGen.mtRandRange(min, max))
}

func mtGetrandmaxFn(ctx *vm.Context) vm.Value { return vm.Int(2147483647) }

func randomIntFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fmt.Sprintf("random_int() expects exactly 2 arguments, %d given", len(ctx.Args()))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	min := arg(ctx, 0).ToInt()
	max := arg(ctx, 1).ToInt()
	if min > max {
		ctx.Throw(vm.NewThrowable("ValueError",
			"random_int(): Argument #1 ($min) must be less than or equal to argument #2 ($max)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Int(secureRandomInt(min, max))
}

func randomBytesFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "random_bytes() expects exactly 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	n := arg(ctx, 0).ToInt()
	if n < 1 {
		ctx.Throw(vm.NewThrowable("ValueError", "random_bytes(): Argument #1 ($length) must be greater than 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		ctx.Throw(vm.NewThrowable("Random\\RandomException", "Failed to generate random bytes: "+err.Error()))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Str(string(b))
}

// secureRandomInt равномерно выбирает int в [min,max] без смещения.
func secureRandomInt(min, max int64) int64 {
	umax := uint64(max) - uint64(min)
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return min
	}
	r := binary.LittleEndian.Uint64(b[:])
	if umax == ^uint64(0) {
		return int64(r) + min
	}
	span := umax + 1
	limit := ^uint64(0) - (^uint64(0) % span) - 1
	for r > limit {
		if _, err := crand.Read(b[:]); err != nil {
			return min
		}
		r = binary.LittleEndian.Uint64(b[:])
	}
	return int64(r%span) + min
}

// lcg_value — комбинированный ЛКГ PHP. Значения совместимы не побитово,
// но возвращают double в [0,1).
func lcgValueFn(ctx *vm.Context) vm.Value {
	var b [8]byte
	crand.Read(b[:])
	r := binary.LittleEndian.Uint64(b[:])
	return vm.Float(float64(r>>11) / float64(uint64(1)<<53))
}

// mtRangeLocked — обёртка для shuffle/str_shuffle/array_rand с захватом мьютекса.
func mtRangeLocked(min, max int64) int64 {
	mtMu.Lock()
	defer mtMu.Unlock()
	mtEnsureSeeded()
	return mtGen.mtRandRange(min, max)
}
