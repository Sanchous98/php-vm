package vm

import (
	"fmt"
	"math"
	"strings"
)

// Const — компиляционная константа. В отличие от Slot, строку здесь можно
// держать значением: константы не лежат в горячем пути исполнения напрямую.
// При Init они материализуются в слоты (строка боксится в heap).
type Const struct {
	typ Type
	num uint64
	str string
}

func NullConst() Const           { return Const{typ: NullType} }
func BoolConst(b bool) Const     { return Const{typ: BoolType, num: b2i(b)} }
func IntConst(i int64) Const     { return Const{typ: IntType, num: uint64(i)} }
func FloatConst(f float64) Const { return Const{typ: FloatType, num: math.Float64bits(f)} }
func StrConst(s string) Const    { return Const{typ: StringType, str: s} }

// Slot материализует константу в слот, бокся строку в heap.
func (c Const) Slot() Slot {
	if c.typ == StringType {
		return MakeInternedString(c.str)
	}
	return Slot{typ: c.typ, num: c.num}
}

// Type возвращает тип константы (для выбора K-формы компилятором).
func (c Const) Type() Type { return c.typ }

func (c Const) String() string {
	switch c.typ {
	case NullType:
		return "null"
	case BoolType:
		return b2s(c.num != 0)
	case IntType:
		return intToString(int64(c.num))
	case FloatType:
		return formatFloat(math.Float64frombits(c.num))
	case StringType:
		return c.str
	}
	return "?"
}

// Chunk — скомпилированная программа: плоский список инструкций и пул
// констант. Константы адресуются индексом C в регистровых инструкциях.
type Chunk struct {
	code []Instruction
	// lines — служебная сайд-таблица: номер исходной строки на каждую
	// инструкцию (параллельно code). Не хранится в Instruction. Нужна только
	// для диагностик/трейсов (читается лениво в момент ошибки), поэтому не
	// тратит диспетчеризацию на исполнении.
	lines []int32
	// emitLine — текущая строка эмиссии; Emit записывает её в lines. Компилятор
	// выставляет через SetLine вместо эмиссии служебного опкода.
	emitLine int32
	// strictFlags — параллельно code: режим strict_types в точке эмиссии
	// инструкции. Инлайненные include/eval смешивают в одном чанке код из
	// файлов с разным strict_types, поэтому вызов наследует режим файла, в
	// котором он текстуально находится, а не единый режим чанка.
	strictFlags []bool
	// emitStrict — текущий режим для последующих Emit.
	emitStrict bool
	// inlineRegions — участки кода, вклеенного из include/require на этапе
	// компиляции (см. inlineRegion). Пусто почти всегда: аллокация и обход —
	// только в файлах с литеральными include. Нужны для атрибуции файла
	// инструкциям (трейсы, диагностики) — строки уже есть в lines.
	inlineRegions []inlineRegion
	constants     []Const
	consts        []Slot // материализованные константы (строки — в heap)
	// constInts/constFloats — «сырые» по типам значения констант, параллельные
	// consts (индексируются тем же C). Позволяют hot K-опкодам читать int64/
	// float64 напрямую, без загрузки 24-байтового Slot и проверки тега. Запись
	// имеет смысл только для элементов, чей Const соответствующего типа;
	// остальные равны нулю (но они и не адресуются типизированными K-опкодами).
	constInts   []int64
	constFloats []float64
	// numRegs — сколько регистров нужно функции (аргументы + локалы +
	// временные).
	numRegs int

	// File — путь к исходному файлу (для диагностик).
	File string
	// Strict — код в этом чанке объявлен в файле с declare(strict_types=1).
	// Влияет на приведение типов при вызовах и записи в типизированные
	// свойства.
	Strict bool

	// tries — метаданные try/catch/finally регионов (для OpPushTry/OpFinallyEnd).
	tries []TryInfo

	// jumps — адреса переходов для fused-операций, у которых C занят индексом
	// константы (например OpJumpIfLessIntK). Индексируется полем A.
	jumps []int32

	// ic — side-table inline-кэшей диспетчеризации методов и динамических
	// функций. Индексируется позицией инструкции (vm.ip-1), чтобы не
	// увеличивать Instruction. Выделяется лениво при первом exec. Классы и
	// функции неизменяемы после сборки, поэтому инвалидация не нужна.
	ic []icEntry

	// liveMasks — маски живых регистров по PC: для инструкции pc биты
	// [pc*liveStride, (pc+1)*liveStride) — регистры, живые ПЕРЕД её
	// исполнением (SSA-анализ живости, regalloc.go). Нужны lifetime.go,
	// чтобы мёртвые регистры не удерживали объекты от __destruct. nil, если
	// SSA-аллокация не выполнялась: тогда живость считается неизвестной и
	// сборщик работает консервативно (см. liveRegAt).
	liveMasks  []uint64
	liveStride int

	// entryNull — регистры, которые пролог кадра зануляет при входе в чанк:
	// неинициализированные чтения и входные значения именованных переменных,
	// читаемые (по SSA) до первой записи. Параметры/захваты исключены — их
	// определяет привязка аргументов (непереданные зануляются отдельно).
	// Слоты кадра нарезаны из общего пула, поэтому без этого чтение до записи
	// видело бы значение прошлого вызова. nil — таких регистров нет; для
	// чанков без масок (liveMasks == nil) пролог зануляет весь кадр.
	entryNull []uint8

	// mayTrack — в чанке встречались операции, способные поместить в регистр
	// учитываемое значение (объект/массив/ссылка/замыкание), либо он получает
	// такие значения извне (параметры/захваты/try). Для чисто скалярных чанков
	// точки OpSweep не эмитятся: периодическая чистка регистров и отложенные
	// проходы сборщика там не нужны, а writeback-барьеры срабатывают вхолостую.
	mayTrack bool
	// constExpr — чанк собран компилятором константного выражения (атрибуты,
	// отложенные константы): при ошибке «Undefined constant» Zend печатает
	// исходный селектор (self/static/parent), а не имя класса.
	constExpr bool
	// varRegs — маска регистров именованных переменных (main) и локальных
	// переменных функции. Для чанков без SSA-аллокации живость считается по
	// байткоду, и пиновать нужно именно эти регистры, а не весь префикс.
	varRegs [liveMaskStride]uint64
	// userVarRegs — подмножество varRegs: только настоящие пользовательские
	// переменные (без внутренних времён вида "\x00iter..."). Нужна для
	// освобождения временных операндов при раскрутке непойманного исключения
	// (bug69740): внутренние регистры foreach освобождаются, пользовательские
	// переменные живут до shutdown. hasUserVarRegs=false — маска не задана
	// (ручные чанки), использовать varRegs.
	userVarRegs    [liveMaskStride]uint64
	hasUserVarRegs bool

	// byRefArgs[i] — параметр i объявлен по ссылке. By-value параметры при
	// вызове получают разыменованное значение (PHP не проносит ссылку в
	// by-value параметр).
	byRefArgs []bool

	// numParams — число привязываемых позиций (параметры + захваченные
	// замыканием переменные). При вызове с большим числом аргументов, чем
	// объявлено (PHP это разрешает), лишние аргументы НЕ привязываются к
	// регистрам кадра (иначе затёрли бы локальные), но остаются доступны
	// через func_get_args(); см. vm.funcArgs.
	numParams int

	// numParamsSet — numParams задан компилятором; иначе (ручные чанки в
	// тестах) при вызове привязываются все переданные аргументы.
	numParamsSet bool

	// keepParamsLive — тело читает аргументы через func_get_arg()/
	// func_get_args(): SSA-распределение регистров отключается, чтобы не
	// переиспользовать регистры параметров как временные.
	keepParamsLive bool

	// startup — диагностики (Deprecated/Notice), собранные на этапе
	// компиляции и выводимые до начала выполнения (как PHP).
	startup []string
	// startupWarn — стартовые Warning-диагностики компиляции.
	startupWarn []string

	// localVars — карта имя→регистр локальных переменных этого чанка.
	// Заполняется компилятором только для функций, которым нужна локальная
	// таблица символов (compact/extract/get_defined_vars). В таких функциях
	// регистры переменных pin'ятся, поэтому карта остаётся валидной после
	// SSA-распределения.
	localVars map[string]int
	// namedRegs — карта имя→регистр ВСЕХ именованных переменных чанка,
	// заполняется всегда (для подсчёта владельцев ссылок в var_dump). Валидна
	// только если регистры переменных pin'ятся (см. compileBody/main).
	namedRegs map[string]int
	// globalVars — имена, объявленные в этом чанке через `global`/`static`,
	// отображённые в их ключ таблицы глобалов. compact()/extract() видят их
	// значения из таблицы глобалов.
	globalVars map[string]string
}

// SetLocalVars задаёт карту имя→регистр локальных переменных чанка.
func (c *Chunk) SetLocalVars(m map[string]int) { c.localVars = m }

// SetNamedRegs задаёт карту имя→регистр именованных переменных чанка.
// SetVarRegs фиксирует регистры именованных переменных (и ячеек-ссылок),
// которые обязаны считаться живыми всегда, даже если значение больше не
// читается (Zend держит переменную до перезаписи/unset).
func (c *Chunk) SetVarRegs(vars, refs map[string]uint8) {
	for _, r := range vars {
		if int(r) < maxRegsHard {
			c.varRegs[r>>6] |= 1 << uint(r&63)
		}
	}
	for _, r := range refs {
		if int(r) < maxRegsHard {
			c.varRegs[r>>6] |= 1 << uint(r&63)
		}
	}
}

func (c *Chunk) SetNamedRegs(m map[string]int) { c.namedRegs = m }

// SetUserVarRegs фиксирует регистры только пользовательских переменных (имена
// без внутреннего префикса "\x00").
func (c *Chunk) SetUserVarRegs(vars, refs map[string]uint8) {
	c.hasUserVarRegs = true
	for n, r := range vars {
		if strings.HasPrefix(n, "\x00") {
			continue
		}
		if int(r) < maxRegsHard {
			c.userVarRegs[r>>6] |= 1 << uint(r&63)
		}
	}
	for _, r := range refs {
		if int(r) < maxRegsHard {
			c.userVarRegs[r>>6] |= 1 << uint(r&63)
		}
	}
}

// NamedRegs возвращает карту имя→регистр именованных переменных чанка.
func (c *Chunk) NamedRegs() map[string]int { return c.namedRegs }

// LocalVarReg возвращает регистр локальной переменной name.
func (c *Chunk) LocalVarReg(name string) (int, bool) {
	if c.localVars == nil {
		return 0, false
	}
	r, ok := c.localVars[name]
	return r, ok
}

// SetGlobalVars задаёт карту имя→ключ глобалов для global/static переменных.
func (c *Chunk) SetGlobalVars(m map[string]string) { c.globalVars = m }

// GlobalVarKey возвращает ключ таблицы глобалов для имени, объявленного
// через global/static в этом чанке.
func (c *Chunk) GlobalVarKey(name string) (string, bool) {
	if c.globalVars == nil {
		return "", false
	}
	k, ok := c.globalVars[name]
	return k, ok
}

// SetStartup задаёт стартовые диагностики чанка.
func (c *Chunk) SetStartup(msgs []string) { c.startup = msgs }

// SetStartupWarnings задаёт стартовые Warning-диагностики чанка.
func (c *Chunk) SetStartupWarnings(msgs []string) { c.startupWarn = msgs }

// SetByRefArgs задаёт маску by-ref параметров функции.
func (c *Chunk) SetByRefArgs(m []bool) { c.byRefArgs = m }

// SetNumParams задаёт число привязываемых позиций (параметры + захваты).
func (c *Chunk) SetNumParams(n int) { c.numParams = n; c.numParamsSet = true }

// NumParams возвращает число привязываемых позиций.
func (c *Chunk) NumParams() int { return c.numParams }

// SetKeepParamsLive отключает SSA-распределение регистров для чанка, чтобы
// регистры параметров не были переиспользованы как временные (нужно функциям,
// читающим аргументы через func_get_arg()/func_get_args()).
func (c *Chunk) SetKeepParamsLive(v bool) { c.keepParamsLive = v }

// Состояния записи inline-кэша.
const (
	icEmpty    uint8 = iota // ещё не заполнена
	icMono                  // мономорфная: ключ (class/name) совпал — прямой вызов
	icDisabled              // ключ полиморфен: кэш отключён, всегда полный lookup
)

// icEntry — запись inline-кэша. Для методов ключ — класс получателя (+ имя
// для *Dyn-вариантов); для динамических функций ключ — имя функции (class=nil).
type icEntry struct {
	class ClassInfo // класс-получатель (guard); nil для кэша «имя→функция»
	name  string    // имя метода для *Dyn-вариантов (guard)
	fn    *Function
	decl  ClassInfo
	vis   Visibility
	miss  uint8
	state uint8
}

// TryCatch описывает одну catch-ветку try-региона.
type TryCatch struct {
	Classes []string // имена классов (сопоставление по иерархии)
	Var     uint8    // регистр, куда catch-ветка кладёт исключение (255 — без переменной)
	PC      int32    // адрес начала тела catch
}

// TryInfo — метаданные try-региона.
//
// FinallyPC < 0, если finally отсутствует. Catch-ветки хранятся в порядке
// объявления; VM выбирает первую подходящую.
type TryInfo struct {
	Catches   []TryCatch
	FinallyPC int32 // адрес начала finally (<0 — нет finally)
	EndPC     int32 // адрес после всего try-оператора (для отладки/переходов)
}

// AddTryInfo добавляет метаданные try-региона и возвращает его индекс.
func (c *Chunk) AddTryInfo(t TryInfo) int {
	c.tries = append(c.tries, t)
	return len(c.tries) - 1
}

// SetTryInfo заменяет метаданные try-региона по индексу (после патирования
// адресов).
func (c *Chunk) SetTryInfo(i int, t TryInfo) { c.tries[i] = t }

// TryInfo возвращает метаданные try-региона по индексу.
func (c *Chunk) TryAt(i int) TryInfo {
	if i < 0 || i >= len(c.tries) {
		return TryInfo{FinallyPC: -1}
	}
	return c.tries[i]
}

// NewChunk создаёт пустой чанк.
func NewChunk() *Chunk { return &Chunk{} }

// materialize заполняет consts, constInts и constFloats из constants.
// Идемпотентна.
func (c *Chunk) materialize() {
	n := len(c.constants)
	if cap(c.consts) < n {
		c.consts = make([]Slot, n)
	} else {
		c.consts = c.consts[:n]
	}
	if cap(c.constInts) < n {
		c.constInts = make([]int64, n)
	} else {
		c.constInts = c.constInts[:n]
	}
	if cap(c.constFloats) < n {
		c.constFloats = make([]float64, n)
	} else {
		c.constFloats = c.constFloats[:n]
	}
	for i, k := range c.constants {
		c.consts[i] = k.Slot()
		switch k.typ {
		case IntType:
			c.constInts[i] = int64(k.num)
		case FloatType:
			c.constFloats[i] = math.Float64frombits(k.num)
		}
	}
}

// Emit добавляет инструкцию и возвращает её индекс.
func (c *Chunk) Emit(in Instruction) int {
	c.code = append(c.code, in)
	c.lines = append(c.lines, c.emitLine)
	c.strictFlags = append(c.strictFlags, c.emitStrict)
	noteReg(c, in)
	if !c.mayTrack {
		c.mayTrack = c.opMayTrack(in)
	}
	return len(c.code) - 1
}

// MayTrack сообщает, что чанку нужен периодический проход времени жизни
// (OpSweep): в нём есть операции, способные создать учитываемые значения,
// либо он принимает их извне.
func (c *Chunk) MayTrack() bool { return c.mayTrack }

// SetMayTrack принудительно включает проходы времени жизни в чанке
// (функции с параметрами/захватами: кадр получает значения извне).
func (c *Chunk) SetMayTrack() { c.mayTrack = true }

// SetConstExpr помечает чанк как константное выражение (см. constExpr).
func (c *Chunk) SetConstExpr() { c.constExpr = true }

// opMayTrack сообщает, что опкод может поместить в регистр учитываемое
// значение. Белый список — только операции, чей результат всегда скаляр
// (при скалярных операндах, что гарантируется индукцией: см. mayTrack).
func (c *Chunk) opMayTrack(in Instruction) bool {
	switch in.Op {
	case OpLoadK:
		if int(in.C) >= 0 && int(in.C) < len(c.constants) {
			return trackedType(c.constants[in.C].typ)
		}
		return true
	case OpHalt, OpNop, OpMove, OpStoreVar, OpNull, OpTrue, OpFalse,
		OpNeg, OpNot, OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow,
		OpAddK, OpSubK, OpMulK, OpDivK, OpConcat, OpConcatN, OpConcatApp,
		OpEqual, OpNotEqual, OpIdentical, OpNotIdentical,
		OpLess, OpLessOrEqual, OpGreater, OpGreaterEqual,
		OpLessK, OpLessOrEqualK, OpGreaterK, OpGreaterEqualK, OpEqualK,
		OpAddInt, OpAddFloat, OpSubInt, OpSubFloat, OpMulInt, OpMulFloat,
		OpDivInt, OpDivFloat,
		OpAddIntK, OpAddFloatK, OpSubIntK, OpMulIntK,
		OpLessInt, OpLessFloat, OpLessOrEqualInt, OpLessOrEqualFloat,
		OpGreaterInt, OpGreaterFloat, OpGreaterEqualInt, OpGreaterEqualFloat,
		OpEqualInt, OpEqualFloat, OpIdenticalInt, OpIdenticalFloat,
		OpLessIntK, OpLessFloatK, OpLessOrEqualIntK, OpGreaterIntK,
		OpGreaterEqualIntK, OpEqualIntK,
		OpAddIntImm, OpSubIntImm, OpMulIntImm,
		OpLessIntImm, OpLessOrEqualIntImm, OpGreaterIntImm, OpGreaterEqualIntImm,
		OpEqualIntImm, OpJumpIfLessIntImm,
		OpBitAnd, OpBitOr, OpBitXor, OpBitNot, OpShl, OpShr,
		OpIsObject, OpLen, OpSpaceship, OpIncDec,
		OpEcho, OpExit, OpFatal, OpParseFatal, OpSuppressOn, OpSuppressOff,
		OpJump, OpJumpIfFalse, OpJumpIfTrue, OpJumpIfLessIntK,
		OpRet, OpSweep:
		return false
	}
	return true
}

// SetLine задаёт строку для последующих Emit (замена служебного опкода OpLine).
func (c *Chunk) SetLine(line int) {
	if line > 0 {
		c.emitLine = int32(line)
	}
}

// SetEmitStrict задаёт режим strict_types для последующих Emit.
func (c *Chunk) SetEmitStrict(v bool) { c.emitStrict = v }

// StrictAt возвращает режим strict_types инструкции pc. Если сайд-таблица
// отсутствует (вручную собранные чанки в тестах), возвращается режим чанка.
func (c *Chunk) StrictAt(pc int) bool {
	if pc >= 0 && pc < len(c.strictFlags) {
		return c.strictFlags[pc]
	}
	return c.Strict
}

// LineAt возвращает номер исходной строки инструкции с адресом pc.
// Безопасен для любого pc (возвращает 0).
func (c *Chunk) LineAt(pc int) int {
	if pc < 0 || pc >= len(c.lines) {
		return 0
	}
	return int(c.lines[pc])
}

// inlineRegion — участок кода, вклеенного из include/require с литеральным
// путём на этапе компиляции. Инструкции региона принадлежат файлу file, а
// сама конструкция include находилась в callFile:callLine; callName —
// include/require[_once] для синтетического кадра трейса. Регионы вложены
// (include внутри include) — parent указывает на внешний регион (-1 — нет).
type inlineRegion struct {
	startPC, endPC int32
	file           string
	callFile       string
	callLine       int
	callName       string
	parent         int32
}

// AddInlineRegion открывает регион в текущем конце чанка и возвращает его
// индекс (для CloseInlineRegion).
func (c *Chunk) AddInlineRegion(file, callFile string, callLine int, callName string, parent int32) int32 {
	c.inlineRegions = append(c.inlineRegions, inlineRegion{
		startPC: int32(len(c.code)), endPC: int32(len(c.code)),
		file: file, callFile: callFile, callLine: callLine, callName: callName, parent: parent,
	})
	return int32(len(c.inlineRegions) - 1)
}

// CloseInlineRegion закрывает регион на текущем конце чанка.
func (c *Chunk) CloseInlineRegion(idx int32) {
	if idx >= 0 && int(idx) < len(c.inlineRegions) {
		c.inlineRegions[idx].endPC = int32(len(c.code))
	}
}

// inlineRegionAt возвращает индекс самого внутреннего региона, содержащего
// инструкцию pc (-1, если pc вне вклеенного кода).
func (c *Chunk) inlineRegionAt(pc int) int32 {
	if c == nil || len(c.inlineRegions) == 0 || pc < 0 {
		return -1
	}
	p := int32(pc)
	for i := len(c.inlineRegions) - 1; i >= 0; i-- {
		r := &c.inlineRegions[i]
		if p >= r.startPC && p < r.endPC {
			return int32(i)
		}
	}
	return -1
}

// InlineFileAt — файл, которому принадлежит инструкция pc ("" — файл чанка).
func (c *Chunk) InlineFileAt(pc int) string {
	if idx := c.inlineRegionAt(pc); idx >= 0 {
		return c.inlineRegions[idx].file
	}
	return ""
}

// noteReg отслеживает максимальный использованный регистр, чтобы знать
// размер регистрового файла функции.
func noteReg(c *Chunk, in Instruction) {
	if int(in.Op) < len(opcodeArgRegs) {
		for _, r := range opcodeArgRegs[in.Op] {
			var v uint8
			switch r {
			case 'A':
				v = in.A
			case 'B':
				v = in.B
			case 'C':
				v = uint8(in.C)
			default:
				continue
			}
			if int(v)+1 > c.numRegs {
				c.numRegs = int(v) + 1
			}
		}
	}
	// OpConcatN адресует окно регистров B..B+C-1 (C — число частей, не регистр).
	if in.Op == OpConcatN && in.C > 0 {
		if n := int(in.B) + int(in.C); n > c.numRegs {
			c.numRegs = n
		}
	}
}

// opcodeArgRegs говорит, какие операнды опкода — регистры (для трекинга
// numRegs). 'A' и 'B' — регистры; C у разных опкодов разное, поэтому для
// простоты считаем только A/B (временные обычно в младших регистрах).
var opcodeArgRegs = buildArgRegs()

func buildArgRegs() [][]byte {
	m := make([][]byte, numOpcodes)
	for i := range m {
		m[i] = []byte("A")
	}
	// опкоды, где B — тоже регистр
	for _, op := range []Opcode{
		OpMove, OpNeg, OpNot,
		OpAdd, OpSub, OpMul, OpDiv, OpMod, OpPow, OpConcat, OpConcatN,
		OpBitAnd, OpBitOr, OpBitXor, OpBitNot, OpShl, OpShr,
		OpEqual, OpNotEqual, OpIdentical, OpNotIdentical,
		OpLess, OpLessOrEqual, OpGreater, OpGreaterEqual,
		OpSpaceship,
		OpAddInt, OpAddFloat, OpSubInt, OpSubFloat, OpMulInt, OpMulFloat,
		OpDivInt, OpDivFloat,
		OpLessInt, OpLessFloat, OpLessOrEqualInt, OpLessOrEqualFloat,
		OpGreaterInt, OpGreaterFloat, OpGreaterEqualInt, OpGreaterEqualFloat,
		OpEqualInt, OpEqualFloat, OpIdenticalInt, OpIdenticalFloat,
		// массивы: A — массив/приёмник, B — ключ/значение
		OpAppend, OpLen, OpUnset,
		// ООП: A — приёмник/объект, B — объект/значение
		OpPropGet, OpPropSet, OpMethodCall, OpInstanceOf, OpClassOf, OpClone,
		OpPropGetRW, OpPropGetRWRead, OpPropGetRWInterm, OpPropGetRWArr, OpPropGetRWArrDyn,
		// логика
		OpLogicalAnd,
		// ссылки
		OpNewRef, OpRefGet, OpRefSet, OpRefBind,
		// замыкания: B — базовый регистр захваченных значений
		OpClosure,
		// глобальный index-set: A — ключ, B — значение
		OpGlobalIndexSet,
		// вызов с аргументами-массивом: A — результат, B — массив
		OpCallArr,
		// обход объектов: A/B — регистры состояния/источника
		OpIsObject, OpIterInit, OpIterValid, OpIterKey, OpIterCurrent, OpIterNext,
		// ссылки на элемент/свойство
		OpIndexRef, OpPropRef, OpPropRefDyn, OpIterCurrentRef, OpPropUnrefDyn,
		OpPropSetRef,
		// ++/--: A — приёмник результата, B — источник
		OpIncDec,
		// базы записи через ячейку-ссылку: A — ячейка/массив, B — источник
		OpRefGetRaw, OpRefSetRaw,
	} {
		m[op] = []byte("AB")
	}
	// опкоды, где C — тоже регистр (ключ)
	for _, op := range []Opcode{OpIndex, OpIndexSet, OpIndexSetOverload, OpIndexIsset, OpIndexRef, OpPropRefDyn, OpPropSetRefDyn,
		OpIndexRaw, OpIndexSetRaw, OpIndexSetOverloadRaw, OpConcatApp, OpListFetchRef} {
		m[op] = []byte("ABC")
	}
	// статическое свойство по динамическому имени: A — ссылка, C — регистр-имя
	// (B — селектор класса, не регистр).
	m[OpStaticSetRefDyn] = []byte("AC")
	m[OpStaticRefDyn] = []byte("AC")
	return m
}

// EmitOp добавляет инструкцию без операндов.
func (c *Chunk) EmitOp(op Opcode) int { return c.Emit(Instruction{Op: op}) }

// AddConst добавляет константу и возвращает её индекс.
func (c *Chunk) AddConst(v Const) int {
	c.constants = append(c.constants, v)
	return len(c.constants) - 1
}

// SetJump патчит адрес перехода инструкции по индексу (для бэктрекинга).
func (c *Chunk) SetJump(at int, target int32) { c.code[at].C = target }

// At возвращает инструкцию по индексу (для патчинга fused-переходов).
func (c *Chunk) At(i int) Instruction { return c.code[i] }

// AddJump добавляет слот таблицы переходов и возвращает его индекс.
func (c *Chunk) AddJump() int {
	c.jumps = append(c.jumps, -1)
	return len(c.jumps) - 1
}

// SetJumpTable патчит адрес перехода в таблице jumps.
func (c *Chunk) SetJumpTable(i int, target int32) { c.jumps[i] = target }

// JumpTable возвращает адрес перехода из таблицы jumps.
func (c *Chunk) JumpTable(i int) int32 { return c.jumps[i] }

// Len возвращает число инструкций.
func (c *Chunk) Len() int { return len(c.code) }

// NumRegs возвращает число регистров, нужных функции.
func (c *Chunk) NumRegs() int { return c.numRegs }

// LastOpcode возвращает последнюю эмитированную инструкцию чанка.
func (c *Chunk) LastOpcode() (Opcode, bool) {
	if c == nil || len(c.code) == 0 {
		return 0, false
	}
	return c.code[len(c.code)-1].Op, true
}

// liveRegAt сообщает, что регистр r жив в точке pc (перед исполнением
// инструкции pc). Без масок (SSA-аллокация не выполнялась) возвращает true —
// консервативно: неизвестный регистр считается удерживающим значение.
func (c *Chunk) liveRegAt(pc, r int) bool {
	if c == nil || c.liveMasks == nil || c.liveStride == 0 {
		return true
	}
	if r < 0 || r >= maxRegsHard || pc < 0 {
		return true
	}
	off := pc*c.liveStride + r>>6
	if off < 0 || off >= len(c.liveMasks) {
		return true
	}
	return c.liveMasks[off]&(1<<uint(r&63)) != 0
}

// Disassemble возвращает читаемый листинг.
func (c *Chunk) Disassemble() string {
	var b strings.Builder
	for i, in := range c.code {
		fmt.Fprintf(&b, "%04d  %-18s", i, in.Op)
		switch in.Op {
		case OpLoadK:
			fmt.Fprintf(&b, " r%d = const#%d (%s)", in.A, in.C, c.constants[in.C].String())
		case OpJump:
			fmt.Fprintf(&b, " -> %04d", in.C)
		case OpJumpIfFalse, OpJumpIfTrue:
			fmt.Fprintf(&b, " r%d -> %04d", in.A, in.C)
		case OpEcho:
			fmt.Fprintf(&b, " r%d", in.A)
		default:
			// общий случай: A, B и (если опкод константный) const#C
			fmt.Fprintf(&b, " r%d", in.A)
			if int(in.Op) < len(opcodeArgRegs) && len(opcodeArgRegs[in.Op]) > 1 {
				fmt.Fprintf(&b, ", r%d, c%d", in.B, in.C)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
