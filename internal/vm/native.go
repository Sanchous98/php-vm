package vm

import "fmt"

// ABI нативных функций (решённый).
//
// Функция — func(ctx *Context) K, K — PHP-тип (Int/Float/Bool/Str/…), который
// умеет упаковаться в слот. Slot в сигнатуру автора не протекает.
//
// Пример:
//
//	Register("double", func(ctx *Context) Int {
//	    return ctx.Arg[Int](0) * 2
//	})
//
// Register регистрирует нативную функцию func(ctx *Context) K, K — PHP-тип
// (Int/Float/Bool/Str/…). Автор пишет Register(fn) без параметров типа.
// Аргументы — ctx.Arg[T](i); ошибки — ctx.Throw / error. Slot в ABI не входит.
func Register[K Value, T func(*Context) K](name string, fn T) *Function {
	return &Function{
		Name:    name,
		NumArgs: -1, // арность не фиксирована: VM передаёт доступные регистры
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.args = args
			var out Slot
			SlotOf(&out, fn(ctx))
			return out, nil
		}},
	}
}

func typeErr(want string, got Slot) *Throwable {
	return NewThrowable("TypeError", fmt.Sprintf("expected %s, got %s", want, got.typ))
}
