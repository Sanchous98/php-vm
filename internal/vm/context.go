package vm

import (
	"math"
	"unsafe"
)

// Context — окружение вызова, доступное нативным функциям и VM.
//
// Держит scope (self/static/this) и доступ к VM.
type Context struct {
	vm *VM

	// Scope-поля:
	this   Slot      // $this (объект) как слот
	self   ClassInfo // self:: — класс объявления
	called ClassInfo // static:: — late static binding

	// args — аргументы текущего нативного вызова (для Arg).
	args []Slot

	// thrown — отложенное исключение (error-протокол). Throw только
	// выставляет его и возвращается; рантайм вызова (NativeFunction.Call)
	// и главный цикл VM проверяют его и превращают в error. nil — нет
	// исключения. Хранится первым победителем (первое исключение вперёд).
	thrown *Throwable
}

// NewContextForBench — тестовый хелпер (бенчмарки из другого пакета): контекст
// для прямого вызова нативной функции без интерпретатора.
func NewContextForBench(v *VM, args []Slot) *Context {
	v.ctx = Context{vm: v, args: args}
	return &v.ctx
}

// Arg возвращает i-й аргумент, приведённый к PHP-типу T (Int/Float/Bool/Str/
// *Array/*Object/Slot). Диспетчер — по ТЕГУ СЛОТА (конкретный uint8), поэтому
// any(Int(...)).(T) мономорфизируется и не аллоцирует (проверено: ~2.4 нс).
func (c *Context) Arg[T any](i int) T {
	var zero T
	if i < 0 || i >= len(c.args) {
		c.Throw(NewThrowable("ArgumentCountError", "too few arguments"))
		return zero
	}
	s := deref(c.args[i])
	switch s.typ {
	case IntType:
		return any(Int(s.num)).(T)
	case FloatType:
		return any(Float(math.Float64frombits(s.num))).(T)
	case BoolType:
		return any(Bool(s.num != 0)).(T)
	case StringType:
		return any(Str(s.StringVal())).(T)
	case ArrayType:
		return any(s.AsArray()).(T)
	case ObjectType:
		if c.vm == nil {
			return zero
		}
		return any(c.vm.objectOf(s)).(T)
	}
	return zero
}

// This возвращает указатель на типизированные данные нативного класса T из
// $this. T — Go-структура, встраивающая Object первым полем, поэтому адрес
// шапки совпадает с адресом данных (одно unsafe-приведение, как objectAs).
func (c *Context) This[T any]() *T {
	if c.this.ptr == nil {
		return nil
	}
	return (*T)(unsafe.Pointer(c.this.obj()))
}

// ThisObj возвращает *Object шапку $this.
func (c *Context) ThisObj() *Object {
	if c.this.ptr == nil {
		return nil
	}
	return c.this.obj()
}

// ThisSlot возвращает слот $this текущего scope (для compact('this') и т.п.).
func (c *Context) ThisSlot() Slot { return c.this }

// Self возвращает self:: (класс объявления).
func (c *Context) Self() ClassInfo { return c.self }

// Called возвращает static:: (класс вызова).
func (c *Context) Called() ClassInfo { return c.called }

// VM даёт доступ к виртуальной машине (функции, вывод).
func (c *Context) VM() *VM { return c.vm }

// Throw поднимает исключение БЕЗ panic/recover: выставляет c.thrown и
// возвращается. Вызывающий нативный код обязан немедленно вернуться (в
// каждом месте вызова Throw за этим следят), а NativeFunction.Call либо
// главный цикл VM превращают c.thrown в error. Хранится первое исключение.
func (c *Context) Throw(v *Throwable) {
	if v == nil {
		return
	}
	if c.thrown != nil {
		// Уже есть распространяющееся исключение: оно побеждает.
		return
	}
	if v.Trace == nil && c.vm != nil {
		v.Trace = c.vm.captureExceptionTrace()
		if v.LocLine == 0 && !v.LocOverridden {
			// Локация — место вызова (самый внутренний кадр трейса). Кадры,
			// вызванные из внутренних функций (напр. обработчик ошибок,
			// вызванный из нативного report-механизма), пропускаем: Zend
			// берёт место в userland-коде.
			for i := len(v.Trace) - 1; i >= 0; i-- {
				f := v.Trace[i]
				if f.callerFile != "" && f.callerFile != "[internal function]" {
					v.LocFile = f.callerFile
					v.LocLine = f.callerLine
					break
				}
			}
			if v.LocLine == 0 {
				v.LocFile = c.vm.curFile
				v.LocLine = c.vm.line()
			}
		}
	}
	c.thrown = v
}

// takeThrown извлекает отложенное исключение (и обнуляет его).
func (c *Context) takeThrown() *Throwable {
	t := c.thrown
	c.thrown = nil
	return t
}

// HasThrown сообщает, есть ли распространяющееся исключение. Используется
// нативным кодом для немедленного выхода после Throw (error-протокол).
func (c *Context) HasThrown() bool { return c.thrown != nil }

// Throwable — PHP-исключение/ошибка. Если исключение брошено из userland,
// Value хранит объект-исключение (а Class/Message дублируют его данные).
type Throwable struct {
	Class   string // имя класса исключения
	Message string
	Code    int64
	Value   Slot // объект исключения (null для нативных ошибок)
	// Trace — стек вызовов на момент создания (для PHP-подобного stack trace).
	Trace []callFrameInfo
	// Fatal — некэтчируемая фатальная ошибка: печатается как
	// "Fatal error: ... in file on line N" без "Uncaught".
	Fatal bool
	// LocFile/LocLine — явная локация ошибки (напр. строка объявления функции
	// для ошибки проверки аргументов). Если LocLine > 0, reportUncaught
	// использует их вместо текущей инструкции.
	LocFile string
	LocLine int
	// DefinedIn — сообщение должно получить суффикс "and defined in file:line"
	// (ошибки проверки типов аргументов/возврата с известной локацией функции).
	DefinedIn bool
	// LocOverridden — локация исключения переписана пользовательским
	// конструктором ($this->file/line): даже пустой файл печатается как есть.
	LocOverridden bool
	// close — служебный сигнал принудительного закрытия генератора: не
	// фиксирует трейс и не может быть пойман catch.
	close bool
}

func (t *Throwable) Error() string { return t.Message }

// NewFatal создаёт некэтчируемую фатальную ошибку (формат Zend fatal).
func NewFatal(msg string) *Throwable { return &Throwable{Class: "Error", Message: msg, Fatal: true} }

// NewThrowable создаёт исключение.
func NewThrowable(class, msg string) *Throwable {
	return &Throwable{Class: class, Message: msg}
}

// Print пишет строку в вывод VM (для встроенных функций).
func (c *Context) Print(s string) { c.vm.Write(s) }

// Args возвращает аргументы текущего вызова (для вариативных встроенных).
func (c *Context) Args() []Slot { return c.args }

// VM возвращает VM (для служебных нужд встроенных функций).
func (c *Context) VMInstance() *VM { return c.vm }
