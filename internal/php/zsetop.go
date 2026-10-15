package php

// zsetop.go — точный порт движков Zend php_array_diff / php_array_intersect
// (PHP 8.3, ext/standard/array.c). Порядок вызовов пользовательских
// компараторов и предупреждений (Array to string conversion при сравнении
// строк) наблюдаем, поэтому и сортировка списков, и сканирование
// воспроизводятся по шагам.

import (
	"strconv"
	"strings"

	"phpvm/internal/vm"
)

// Поведение (DIFF_*/INTERSECT_* в Zend).
const (
	setOpNormal = 1
	setOpKey    = 2
	setOpAssoc  = 6
)

// setBucket — Bucket списка: ключ, значение и признак sentinel (IS_UNDEF).
type setBucket struct {
	k     vm.ArrayKey
	v     vm.Slot
	undef bool
}

// setOpCmpState — состояние пользовательского компаратора: ошибка вызова,
// общий флаг выданного Deprecated (Zend ARRAYG(compare_deprecation_thrown)).
type setOpCmpState struct {
	ctx *vm.Context
	cb  vm.Slot
	err error
	dep *bool
}

// call повторяет php_array_user_compare_unstable / php_array_user_key_compare_unstable:
// bool-результат выдаёт Deprecated (однократно на вызов функции) и при false
// повторяет вызов с перестановкой операндов.
func (u *setOpCmpState) call(a, b vm.Slot) int {
	args := [2]vm.Slot{a, b}
	res, err := u.ctx.VMInstance().CallCallable(u.ctx, u.cb, args[:])
	if err != nil {
		if u.err == nil {
			u.err = err
		}
		return 0
	}
	if res.Type() == vm.BoolType {
		if u.dep != nil && !*u.dep {
			*u.dep = true
			u.ctx.VM().Deprecated("Returning bool from comparison function is deprecated, return an integer less than, equal to, or greater than zero")
		}
		if !res.ToBool() {
			args[0], args[1] = b, a
			res2, err2 := u.ctx.VMInstance().CallCallable(u.ctx, u.cb, args[:])
			if err2 != nil {
				if u.err == nil {
					u.err = err2
				}
				return 0
			}
			return -normSign(res2)
		}
		return 1
	}
	return normSign(res)
}

func normSign(v vm.Slot) int {
	n := phpCmpInt(v)
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

func (u *setOpCmpState) value(a, b *setBucket) int { return u.call(a.v, b.v) }
func (u *setOpCmpState) key(a, b *setBucket) int   { return u.call(arrayKeySlot(a.k), arrayKeySlot(b.k)) }

// setStringCmp — string_compare_function: приведение к строке (с
// предупреждением для массивов) и байтовое сравнение.
func setStringCmp(ctx *vm.Context, a, b *setBucket) int {
	as := sortStringOf(ctx, a.v)
	bs := sortStringOf(ctx, b.v)
	return strings.Compare(as, bs)
}

// setKeyStringCmp — php_array_key_compare_string_unstable: ключи как строки
// (int-ключи печатаются десятично).
func setKeyStringCmp(a, b *setBucket) int {
	return strings.Compare(setKeyBytes(a.k), setKeyBytes(b.k))
}

func setKeyBytes(k vm.ArrayKey) string {
	if k.IsStr() {
		return k.StrVal()
	}
	return strconv.FormatInt(k.IntVal(), 10)
}

// setOpEngine — параметры одного представителя семейства.
type setOpEngine struct {
	behavior int
	// data/key — компараторы (key нужен только для ASSOC/KEY).
	data func(*setBucket, *setBucket) int
	key  func(*setBucket, *setBucket) int
	// user — состояния пользовательских компараторов.
	userData *setOpCmpState
	userKey  *setOpCmpState
}

// runZendSetOp выполняет diff или intersect по алгоритму Zend и возвращает
// результат (порядок ключей — как в arrays[0]).
func runZendSetOp(ctx *vm.Context, arrays []*vm.Array, eng setOpEngine, intersect bool) vm.Value {
	// Списки Bucket: компактно, в порядке элементов, с sentinel.
	lists := make([][]setBucket, len(arrays))
	for i, a := range arrays {
		list := make([]setBucket, 0, a.Len()+1)
		for k, v := range a.All() {
			list = append(list, setBucket{k: k, v: v})
		}
		list = append(list, setBucket{undef: true})
		if a.Len() > 1 {
			cmp := eng.data
			if eng.behavior != setOpNormal {
				cmp = eng.key
			}
			sorted := list[:len(list)-1]
			zendSortRange(0, len(sorted), func(i, j int) int { return cmp(&sorted[i], &sorted[j]) },
				func(i, j int) { sorted[i], sorted[j] = sorted[j], sorted[i] })
		}
		lists[i] = list
	}

	// Результат: копия arrays[0]; удаления идут по ключам.
	out := vm.NewArray()
	for k, v := range arrays[0].All() {
		out.Set(k, derefLoneRef(ctx, v))
	}
	del := func(k vm.ArrayKey) { out.Unset(k) }

	undef := func(l []setBucket, p int) bool { return l[p].undef }

	ptrs := make([]int, len(lists))
	// c как в C: живёт между итерациями внешнего цикла.
	c := 0
	if intersect {
	scan:
		for !undef(lists[0], ptrs[0]) {
			i := 1
			for ; i < len(lists); i++ {
				if eng.behavior&setOpAssoc != 0 {
					// INTERSECT_KEY тоже идёт сюда.
					for !undef(lists[i], ptrs[i]) {
						c = eng.key(&lists[0][ptrs[0]], &lists[i][ptrs[i]])
						if c <= 0 {
							break
						}
						ptrs[i]++
					}
					if c == 0 && !undef(lists[i], ptrs[i]) && eng.behavior == setOpAssoc {
						if eng.data(&lists[0][ptrs[0]], &lists[i][ptrs[i]]) != 0 {
							c = 1
						}
					}
				} else {
					for !undef(lists[i], ptrs[i]) {
						c = eng.data(&lists[0][ptrs[0]], &lists[i][ptrs[i]])
						if c <= 0 {
							break
						}
						ptrs[i]++
					}
				}
				if undef(lists[i], ptrs[i]) {
					// Остаток ptrs[0] не встречается: удалить всё и выйти.
					for {
						if undef(lists[0], ptrs[0]) {
							break scan
						}
						k := lists[0][ptrs[0]].k
						ptrs[0]++
						del(k)
					}
				}
				if c != 0 {
					break
				}
				ptrs[i]++
			}
			if c != 0 {
				// Значение ptrs[0] не во всех аргументах: удалять, пока
				// сравнение с ptrs[i] < 0 (для ASSOC — один элемент).
				for {
					k := lists[0][ptrs[0]].k
					del(k)
					ptrs[0]++
					if undef(lists[0], ptrs[0]) {
						break scan
					}
					if eng.behavior == setOpNormal {
						if eng.data(&lists[0][ptrs[0]], &lists[i][ptrs[i]]) >= 0 {
							break
						}
					} else {
						break
					}
				}
			} else {
				// Есть во всех: пропустить все с тем же значением
				// (для ASSOC — один элемент).
				for {
					ptrs[0]++
					if undef(lists[0], ptrs[0]) {
						break scan
					}
					if eng.behavior == setOpNormal {
						if eng.data(&lists[0][ptrs[0]-1], &lists[0][ptrs[0]]) != 0 {
							break
						}
					} else {
						break
					}
				}
			}
		}
	} else {
	diff:
		for !undef(lists[0], ptrs[0]) {
			c = 1
			for i := 1; i < len(lists); i++ {
				// Для ASSOC/KEY Zend сканирует локальной копией указателя
				// (ptrs[i] не двигается) — количество сравнений это меняет.
				ptr := ptrs[i]
				if eng.behavior == setOpNormal {
					for !undef(lists[i], ptrs[i]) {
						c = eng.data(&lists[0][ptrs[0]], &lists[i][ptrs[i]])
						if c <= 0 {
							break
						}
						ptrs[i]++
					}
				} else {
					for !undef(lists[i], ptr) {
						c = eng.key(&lists[0][ptrs[0]], &lists[i][ptr])
						if c != 0 {
							ptr++
							continue
						}
						break
					}
				}
				if c == 0 {
					if eng.behavior == setOpNormal {
						if !undef(lists[i], ptrs[i]) {
							ptrs[i]++
						}
						break
					} else if eng.behavior == setOpAssoc {
						if !undef(lists[i], ptr) {
							if eng.data(&lists[0][ptrs[0]], &lists[i][ptr]) != 0 {
								c = -1
							} else {
								break
							}
						}
					} else { // setOpKey
						break
					}
				}
			}
			if c == 0 {
				// Значение ptrs[0] есть в другом аргументе: удалить все такие.
				for {
					k := lists[0][ptrs[0]].k
					del(k)
					ptrs[0]++
					if undef(lists[0], ptrs[0]) {
						break diff
					}
					if eng.behavior == setOpNormal {
						if eng.data(&lists[0][ptrs[0]-1], &lists[0][ptrs[0]]) != 0 {
							break
						}
					} else {
						break
					}
				}
			} else {
				// Значения нет ни в одном другом аргументе: пропустить.
				for {
					ptrs[0]++
					if undef(lists[0], ptrs[0]) {
						break diff
					}
					if eng.behavior == setOpNormal {
						if eng.data(&lists[0][ptrs[0]-1], &lists[0][ptrs[0]]) != 0 {
							break
						}
					} else {
						break
					}
				}
			}
		}
	}
	if eng.userData != nil {
		throwCallable(ctx, eng.userData.err)
	}
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if eng.userKey != nil {
		throwCallable(ctx, eng.userKey.err)
	}
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.MakeArray(out)
}
