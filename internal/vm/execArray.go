package vm

// execArray.go — массивы, индексация, итерация, len/unset/isset.

func (vm *VM) opArray(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	{
		old := regs[in.A]
		wv := MakeArray(newArray())
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

// offsetTypeErr формирует PHP 8 TypeError при недопустимом типе ключа массива.
func (vm *VM) offsetTypeErr(verb string, s Slot) error {
	return NewThrowable("TypeError", "Cannot "+verb+" offset of type "+operandTypeName(deref(s))+" on array")
}

func (vm *VM) opIndex(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	src := deref(regs[in.B])
	if src.typ == ObjectType {
		// Константное выражение (const/property-default/enum-case/атрибут):
		// Zend не умеет индексировать объекты (даже ArrayAccess) на этапе
		// вычисления констант.
		if chunk != nil && chunk.constExpr {
			err := NewThrowable("Error", "Cannot use [] on objects in constant expression")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		// ArrayAccess: $obj[$k] -> offsetGet($k).
		if ci := vm.classInfo(src); ci != nil {
			if m := ci.lookupMethod("offsetGet"); m != nil {
				res, err := vm.invokeMethod(m, src, ci, []Slot{regs[in.C]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.A]
					wv := res
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				shareSlot(res)
				return execResult{}
			}
		}
		// Объект без ArrayAccess — Error (Zend: "Cannot use object of type
		// X as array").
		err := vm.objectAsArrayErr(src)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if src.typ == StringType {
		if terr := vm.stringOffsetWarnKey(regs[in.C]); terr != nil {
			if !vm.handleThrow(chunk, terr) {
				return execResult{err: terr}
			}
			return execResult{next: true}
		}
		i, ok, illegal := vm.stringOffsetKey(regs[in.C])
		if !ok {
			err := NewThrowable("TypeError", "Cannot access offset of type string on string")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		if illegal {
			vm.Warn(`Illegal string offset "` + deref(regs[in.C]).ToString() + `"`)
		}
		orig := i
		s := src.StringVal()
		if i < 0 {
			i += int64(len(s))
		}
		if i < 0 || i >= int64(len(s)) {
			vm.Warn("Uninitialized string offset " + intToString(orig))
			{
				old := regs[in.A]
				wv := MakeString("")
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		{
			old := regs[in.A]
			wv := MakeString(s[i : i+1])
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if src.typ != ArrayType {
		vm.Warn("Trying to access array offset on " + vm.arrayOffsetTypeName(src))
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	// Быстрый путь целочисленного ключа (foreach/$a[$i]): без конверсии слота
	// в ArrayKey и без предупреждений (int-ключ всегда допустим).
	if ks := deref(regs[in.C]); ks.typ == IntType {
		v, found := src.AsArray().GetInt(ks.Int())
		if !found {
			vm.Warn("Undefined array key " + intToString(ks.Int()))
		}
		v = snapReadVal(v)
		// Элемент-ссылка разыменовывается при чтении (FETCH_DIM_R):
		// значение фиксируется в момент чтения (bug72543_3).
		v = deref(v)
		shareSlot(v)
		{
			old := regs[in.A]
			wv := v
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	k, ok := vm.KeyOfWarn(regs[in.C])
	if !ok {
		err := vm.offsetTypeErr("access", regs[in.C])
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	v, found := src.AsArray().Get(k)
	if !found {
		vm.Warn("Undefined array key " + arrayKeyRepr(k))
	}
	v = deref(v)
	shareSlot(v)
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

// opIndexRaw — как opIndex, но без пометки значения разделённым. Только для
// чтения контейнера-базы записи: результат (или мутированный контейнер)
// обязательно записывается обратно в тот же элемент (INDEX_SET_OVERLOAD),
// поэтому второй владелец не появляется, а shareSlot заставлял бы копировать
// вложенный массив на каждой записи.
// opIndexUnsetRaw — промежуточное звено цепочки unset($a[k1][k2][...]):// семантика OpIndexRaw, но контейнер-строка с ключом, не приводимым к
// допустимому string-offset (нечисловая строка, массив и т.п.), даёт
// Error "Cannot unset string offsets" (Zend BP_VAR_UNSET для промежуточного
// звена), а не TypeError "Cannot access offset of type ... on string".
func (vm *VM) opIndexUnsetRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if src := deref(regs[in.B]); src.typ == StringType {
		if _, ok, _ := vm.stringOffsetKey(regs[in.C]); !ok {
			err := NewThrowable("Error", "Cannot unset string offsets")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}
	return vm.opIndexRaw(regs, consts, chunk, in)
}

func (vm *VM) opIndexRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	src := deref(regs[in.B])
	if src.typ == ObjectType {
		// ArrayAccess: $obj[$k] -> offsetGet($k).
		if ci := vm.classInfo(src); ci != nil {
			if m := ci.lookupMethod("offsetGet"); m != nil {
				res, err := vm.invokeMethod(m, src, ci, []Slot{regs[in.C]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.A]
					wv := res
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				shareSlot(res)
				return execResult{}
			}
		}
		err := vm.objectAsArrayErr(src)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if src.typ == StringType {
		if terr := vm.stringOffsetWarnKey(regs[in.C]); terr != nil {
			if !vm.handleThrow(chunk, terr) {
				return execResult{err: terr}
			}
			return execResult{next: true}
		}
		i, ok, illegal := vm.stringOffsetKey(regs[in.C])
		if !ok {
			err := NewThrowable("TypeError", "Cannot access offset of type string on string")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		if illegal {
			vm.Warn(`Illegal string offset "` + deref(regs[in.C]).ToString() + `"`)
		}
		orig := i
		s := src.StringVal()
		if i < 0 {
			i += int64(len(s))
		}
		if i < 0 || i >= int64(len(s)) {
			vm.Warn("Uninitialized string offset " + intToString(orig))
			{
				old := regs[in.A]
				wv := MakeString("")
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		{
			old := regs[in.A]
			wv := MakeString(s[i : i+1])
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if src.typ != ArrayType {
		vm.Warn("Trying to access array offset on " + vm.arrayOffsetTypeName(src))
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if ks := deref(regs[in.C]); ks.typ == IntType {
		v, found := src.AsArray().GetInt(ks.Int())
		if !found {
			vm.Warn("Undefined array key " + intToString(ks.Int()))
		}
		{
			old := regs[in.A]
			wv := v
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	k, ok := vm.KeyOfWarn(regs[in.C])
	if !ok {
		err := vm.offsetTypeErr("access", regs[in.C])
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	v, found := src.AsArray().Get(k)
	if !found {
		vm.Warn("Undefined array key " + arrayKeyRepr(k))
	}
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

// opListFetchRef — ссылочная выборка элемента для деструктуризации
// (Zend FETCH_LIST_W + MAKE_REF, см. OpListFetchRef).
func (vm *VM) opListFetchRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if regs[in.B].typ == RefType {
		r := regs[in.B].Ref()
		if r == nil {
			r = &Ref{v: MakeNull()}
			old := regs[in.B]
			wv := MakeRef(r)
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
		return vm.listFetchRefWrite(regs, chunk, in, r)
	}
	// Ссылку взять нельзя (временное значение без Ref-ячейки): Notice и
	// чтение КОПИИ (Zend FETCH_LIST_W → read path is_list=1).
	vm.Notice("Attempting to set reference to non referenceable value")
	res, err := vm.listFetchRefRead(regs[in.B], regs[in.C], chunk)
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := res
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

// listFetchRefWrite — write-ветка FETCH_LIST_W: контейнер B — Ref-ячейка.
func (vm *VM) listFetchRefWrite(regs []Slot, chunk *Chunk, in Instruction, r *Ref) execResult {
	inner := deref(r.v)
	switch inner.typ {
	case ArrayType:
		k, ok := vm.KeyOfWarn(regs[in.C])
		if !ok {
			err := vm.offsetTypeErr("access", regs[in.C])
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		a := inner.AsArray().Unshare()
		cur, found := a.Get(k)
		var cell Slot
		if found && cur.typ == RefType {
			cell = cur
		} else {
			inner2 := cur
			if !found {
				inner2 = MakeNull()
			}
			shareSlot(inner2)
			cell = MakeRef(&Ref{v: inner2})
			a.Set(k, cell)
		}
		if a != inner.AsArray() {
			// Массив был разделён (COW): копия записывается обратно в ячейку.
			if err := vm.writeRef(r, MakeArray(a), vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
		}
		{
			old := regs[in.A]
			wv := cell
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	case ObjectType:
		return vm.listFetchRefObject(regs, chunk, in, r, inner)
	case StringType:
		// Ссылку на байт строки создать нельзя (Zend: FETCH_DIM_W на строке).
		// Диагностика ключа печатается до Error (Illegal string offset).
		if terr := vm.stringOffsetWarnKey(regs[in.C]); terr != nil {
			if !vm.handleThrow(chunk, terr) {
				return execResult{err: terr}
			}
			return execResult{next: true}
		}
		if _, _, illegal := vm.stringOffsetKey(regs[in.C]); illegal {
			vm.Warn(`Illegal string offset "` + deref(regs[in.C]).ToString() + `"`)
		}
		err := NewThrowable("Error", "Cannot create references to/from string offsets")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	case ClosureType:
		// Замыкание — объект: по индексу не читается.
		err := NewThrowable("Error", "Cannot use object of type Closure as array")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// null/false автовивифицируются в массив «на месте» (Zend
	// zend_fetch_dimension_address BP_VAR_W), прочие скаляры — Error.
	base, th := vm.writeBaseArray(inner, false)
	if th != nil {
		if !vm.handleThrow(chunk, th) {
			return execResult{err: th}
		}
		return execResult{next: true}
	}
	if err := vm.writeRef(r, base, vm.chunk != nil && vm.chunk.Strict); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	return vm.listFetchRefWrite(regs, chunk, in, r)
}

// listFetchRefObject — элемент ArrayAccess-объекта для ссылочной выборки:
// by-ref offsetGet даёт настоящую ячейку элемента, иначе — Notice
// «Indirect modification ... has no effect» и копия значения (Zend
// zend_fetch_dimension_address: ветка object).
func (vm *VM) listFetchRefObject(regs []Slot, chunk *Chunk, in Instruction, r *Ref, obj Slot) execResult {
	ci := vm.classInfo(obj)
	if ci == nil {
		return execResult{}
	}
	m := ci.lookupMethod("offsetGet")
	if m == nil {
		err := vm.objectAsArrayErr(obj)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	res, err := vm.invokeMethod(m, obj, ci, []Slot{regs[in.C]})
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if res.typ == RefType {
		{
			old := regs[in.A]
			wv := res
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	v := deref(res)
	if v.typ != ObjectType {
		vm.Notice("Indirect modification of overloaded element of " + classDisplayName(ci) + " has no effect")
	}
	v = snapReadVal(v)
	shareSlot(v)
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

// listFetchRefRead — read-ветка FETCH_LIST_W (is_list=1): копия значения без
// snapReadVal — снимок значения элемента массива для ЧТЕНИЯ
// (Zend FETCH_DIM_R разыменовывает ячейку-ссылку): запись через ту же ссылку
// после чтения не должна менять уже прочитанный операнд (bug72543_1/4).
// Write-контексты (++/-- , составное присваивание) читают базу другим опкодом
// (OpIndexRaw/OpIndexRW) и сохраняют ячейку-ссылку.
func snapReadVal(v Slot) Slot {
	if v.typ == RefType {
		return deref(v)
	}
	return v
}

// диагностик для скаляров/строк; для объектов — offsetGet; для массивов —
// обычное чтение с Warning отсутствующего ключа.
func (vm *VM) listFetchRefRead(container Slot, key Slot, chunk *Chunk) (Slot, error) {
	src := deref(container)
	switch src.typ {
	case ArrayType:
		k, ok := vm.KeyOfWarn(key)
		if !ok {
			return MakeNull(), nil
		}
		v, found := src.AsArray().Get(k)
		if !found {
			if k.IsStr() {
				vm.Warn("Undefined array key \"" + k.StrVal() + "\"")
			} else {
				vm.Warn("Undefined array key " + k.PHPString())
			}
			return MakeNull(), nil
		}
		v = deref(v)
		shareSlot(v)
		return v, nil
	case ObjectType:
		ci := vm.classInfo(src)
		if ci == nil {
			return MakeNull(), nil
		}
		m := ci.lookupMethod("offsetGet")
		if m == nil {
			return Slot{}, vm.objectAsArrayErr(src)
		}
		res, err := vm.invokeMethod(m, src, ci, []Slot{key})
		if err != nil {
			return Slot{}, err
		}
		return deref(res), nil
	case ClosureType:
		return Slot{}, NewThrowable("Error", "Cannot use object of type Closure as array")
	}
	return MakeNull(), nil
}

func (vm *VM) opIndexRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	src := deref(regs[in.B])
	if src.typ == ObjectType {
		// ArrayAccess: ссылку на элемент создать нельзя — возвращаем
		// временную ячейку со значением offsetGet.
		if ci := vm.classInfo(src); ci != nil {
			if m := ci.lookupMethod("offsetGet"); m != nil {
				res, err := vm.invokeMethod(m, src, ci, []Slot{regs[in.C]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.A]
					wv := MakeRef(&Ref{v: res})
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				shareSlot(res)
				return execResult{}
			}
		}
		// Объект без ArrayAccess — Error.
		err := vm.objectAsArrayErr(src)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if src.typ == StringType {
		// FETCH_DIM_W на строке: сначала диагностика ключа, затем Error
		// (Cannot create references to/from string offsets).
		if terr := vm.stringOffsetWarnKey(regs[in.C]); terr != nil {
			if !vm.handleThrow(chunk, terr) {
				return execResult{err: terr}
			}
			return execResult{next: true}
		}
		if _, _, illegal := vm.stringOffsetKey(regs[in.C]); illegal {
			vm.Warn(`Illegal string offset "` + deref(regs[in.C]).ToString() + `"`)
		}
		err := NewThrowable("Error", "Cannot create references to/from string offsets")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if src.typ == NullType {
		// autovivify: `$undef[k]` по ссылке создаёт массив.
		src = MakeArray(newArray())
		{
			old := regs[in.B]
			wv := src
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
	}
	if src.typ != ArrayType {
		err := NewThrowable("Error", "Cannot use a scalar value as an array")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if in.C < 0 {
		// `$arr[]` в write-контексте (например, передача в by-ref параметр):
		// Zend создаёт новый элемент (null) и возвращает ссылку на него.
		a := src.AsArray().Unshare()
		cell := MakeRef(&Ref{v: MakeNull()})
		if _, err := a.Append(cell); err != nil {
			if err == errNextOccupied {
				err = NewThrowable("Error", err.Error())
			}
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.B]
			wv := MakeArray(a)
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
		{
			old := regs[in.A]
			wv := cell
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	k, ok := vm.KeyOfWarn(regs[in.C])
	if !ok {
		err := vm.offsetTypeErr("access", regs[in.C])
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	a := src.AsArray().Unshare()
	cur, found := a.Get(k)
	var cell Slot
	if found && cur.typ == RefType {
		cell = cur
	} else {
		inner := cur
		if !found {
			inner = MakeNull()
		}
		// inner остаётся владельцем только новая ячейка (элемент заменяется
		// на неё), второй владелец не появляется: пометка shared здесь
		// заставляла бы Unshare при записи в массив через ячейку делать
		// копию и терять само-цикл (`$a[1][0] =& $a[1]`, gc_020).
		cell = MakeRef(&Ref{v: inner})
		_ = a.Set(k, cell)
	}
	{
		old := regs[in.B]
		wv := MakeArray(a)
		regs[in.B] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.B), old)
		}
	}
	{
		old := regs[in.A]
		wv := cell
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

// stringOffsetSet — общая реализация записи байта строки по offset
// (`$str[$k] = $v`, в т.ч. для глобальной переменной в OpGlobalIndexSet).
// Возвращает новую строку и значение-результат присваивания (Zend: для строки
// это односимвольная строка фактически записанного байта; для отрицательного
// offset вне диапазона — null). error — исключение.
func (vm *VM) stringOffsetSet(arr Slot, keyReg Slot, valReg Slot) (Slot, Slot, error) {
	if terr := vm.stringOffsetWarnKey(keyReg); terr != nil {
		return Slot{}, Slot{}, terr
	}
	i, ok, illegal := vm.stringOffsetKey(keyReg)
	if !ok {
		return Slot{}, Slot{}, NewThrowable("TypeError", "Cannot access offset of type string on string")
	}
	if illegal {
		vm.Warn(`Illegal string offset "` + deref(keyReg).ToString() + `"`)
	}
	s := []byte(arr.StringVal())
	orig := i
	if i < 0 {
		i += int64(len(s))
	}
	if i < 0 {
		vm.Warn("Illegal string offset " + intToString(orig))
		return arr, MakeNull(), nil
	}
	// Значение приводится через __toString: исключение из него всплывает
	// наружу (exception_from_toString: $str[0] = $badStr).
	ch, cerr := vm.ToStringValue(valReg)
	if cerr != nil {
		return Slot{}, Slot{}, cerr
	}
	if ch == "" {
		return Slot{}, Slot{}, NewThrowable("Error", "Cannot assign an empty string to a string offset")
	}
	if len(ch) > 1 {
		vm.Warn("Only the first byte will be assigned to the string offset")
	}
	for int64(len(s)) < i {
		s = append(s, ' ')
	}
	if i == int64(len(s)) {
		s = append(s, ch[0])
	} else {
		s[i] = ch[0]
	}
	return MakeString(string(s)), MakeString(ch[:1]), nil
}

// opIndexSafe — A = B[C] в IS-режиме (isset()/empty() по вложенной цепочке):
// отсутствующие ключи, неподходящие типы и нечисловые строковые offset'ы дают
// null без диагностик и исключений (Zend BP_VAR_IS). Для ArrayAccess —
// offsetExists + offsetGet.
func (vm *VM) opIndexSafe(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSafe(regs, chunk, in, false)
}

// opIndexSafeWarn — как OpIndexSafe, но для IS-режима `??`: ведуще-числовой
// строковый offset («0foo») даёт значение с предупреждением Illegal string
// offset (Zend FETCH_DIM_IS в coalesce), дробные/нечисловые — null.
func (vm *VM) opIndexSafeWarn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSafe(regs, chunk, in, true)
}

func (vm *VM) indexSafe(regs []Slot, chunk *Chunk, in Instruction, warnIllegal bool) execResult {
	src := deref(regs[in.B])
	switch src.typ {
	case ObjectType:
		if ci := vm.classInfo(src); ci != nil {
			existsM := ci.lookupMethod("offsetExists")
			if existsM != nil {
				res, err := vm.invokeMethod(existsM, src, ci, []Slot{regs[in.C]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				if !deref(res).ToBool() {
					{
						old := regs[in.A]
						wv := MakeNull()
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
					return execResult{}
				}
				if getM := ci.lookupMethod("offsetGet"); getM != nil {
					val, err := vm.invokeMethod(getM, src, ci, []Slot{regs[in.C]})
					if err != nil {
						if !vm.handleThrow(chunk, err) {
							return execResult{err: err}
						}
						return execResult{next: true}
					}
					{
						old := regs[in.A]
						wv := val
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
					shareSlot(val)
					return execResult{}
				}
				{
					old := regs[in.A]
					wv := MakeNull()
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				return execResult{}
			}
		}
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	case ArrayType:
		if ks := deref(regs[in.C]); ks.typ == FloatType {
			// IS-режим: неявное float→int для ключа — Deprecated (как KeyOfWarn).
			vm.floatToIntWarn(ks)
		}
		if ks := deref(regs[in.C]); ks.typ == IntType {
			v, found := src.AsArray().GetInt(ks.Int())
			if !found {
				{
					old := regs[in.A]
					wv := MakeNull()
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				return execResult{}
			}
			{
				old := regs[in.A]
				wv := deref(v)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		k, ok := vm.KeyOfWarn(regs[in.C])
		if !ok {
			// Zend: "Cannot access offset of type X in isset or empty".
			err := NewThrowable("TypeError", "Cannot access offset of type "+
				operandTypeName(deref(regs[in.C]))+" in isset or empty")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		v, found := src.AsArray().Get(k)
		if !found {
			{
				old := regs[in.A]
				wv := MakeNull()
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		{
			old := regs[in.A]
			wv := deref(v)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	case StringType:
		// IS-режим: приведение float-ключа к int диагностируется (Deprecated),
		// а «String offset cast occurred» — нет (Zend FETCH_DIM_IS). Для
		// empty()/isset() вызывающий уже напечатал Deprecated в OpIndexIsset,
		// поэтому здесь предупреждаем только в coalesce-режиме (warnIllegal).
		if warnIllegal {
			if k := deref(regs[in.C]); k.typ == FloatType {
				vm.floatToIntWarn(k)
			}
		}
		i, ok, illegal := stringOffsetIndex(regs[in.C])
		if !ok {
			{
				old := regs[in.A]
				wv := MakeNull()
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		if illegal {
			if !warnIllegal {
				{
					old := regs[in.A]
					wv := MakeNull()
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				return execResult{}
			}
			vm.Warn(`Illegal string offset "` + deref(regs[in.C]).ToString() + `"`)
		}
		s := src.StringVal()
		if i < 0 {
			i += int64(len(s))
		}
		if i < 0 || i >= int64(len(s)) {
			{
				old := regs[in.A]
				wv := MakeNull()
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		{
			old := regs[in.A]
			wv := MakeString(s[i : i+1])
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	default:
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}

	return execResult{}
}

// opIndexRW — A = B[C] для чтения базы составного присваивания ($a[k] op= v).
// null/false автовивифицируются в массив (с обычной диагностикой «Undefined
// array key»), строка и скаляры дают Error как Zend FETCH_DIM_RW.
func (vm *VM) opIndexRW(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	// Ключ канонизируется здесь и остаётся в том же (временном) регистре:
	// последующая запись использует уже int-ключ и не дублирует
	// предупреждение (resource_key: $a[$r] += 1 / $a[$r]++ печатают одно).
	switch deref(regs[in.C]).typ {
	case ResourceType, FloatType:
		if k, ok := vm.KeyOfWarn(regs[in.C]); ok {
			regs[in.C] = MakeInt(k.IntVal())
		}
	}
	src := deref(regs[in.B])
	switch src.typ {
	case ArrayType, ObjectType:
		return vm.opIndex(regs, consts, chunk, in)
	case NullType:
		{
			old := regs[in.B]
			wv := MakeArray(newArray())
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
	case BoolType:
		if src.ToBool() {
			err := NewThrowable("Error", "Cannot use a scalar value as an array")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.B]
			wv := MakeArray(newArray())
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
	case StringType:
		err := NewThrowable("Error", "Cannot use assign-op operators with string offsets")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	default:
		err := NewThrowable("Error", "Cannot use a scalar value as an array")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	return vm.opIndex(regs, consts, chunk, in)
}

// opIndexAppendRaw — A = B[C] в режиме append-базы (`$x[][k] = v`, C — счетчик
// count(B)): для классов с запретом append (WeakMap) — Error, иначе как
// OpIndexRaw.
func (vm *VM) opIndexAppendRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if src := deref(regs[in.B]); src.typ == ObjectType {
		if rc, ok := asRuntimeClass(vm.classInfo(src)); ok && rc != nil && rc.noAppend {
			err := NewThrowable("Error", "Cannot append to "+rc.Name())
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}
	// Для массивов это RAW-выборка «будущего» элемента append-базы: элемент
	// ещё не создан, поэтому отсутствие ключа — не диагностика (Zend
	// FETCH_DIM_WRITE). Иначе (ArrayAccess-объект, строка) — обычный путь.
	if src := deref(regs[in.B]); src.typ == ArrayType {
		k, ok := vm.KeyOfWarn(regs[in.C])
		if !ok {
			err := vm.offsetTypeErr("access", regs[in.C])
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		v, _ := src.AsArray().Get(k)
		{
			old := regs[in.A]
			wv := deref(v)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	return vm.opIndexRaw(regs, consts, chunk, in)
}

func (vm *VM) opIndexSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSet(regs, consts, chunk, in, true, false)
}

// opIndexSetRes — как OpIndexSet, но значение-регистр C получает результат
// присваивания (Zend: для строкового offset это записанный символ, для
// массива/объекта — присвоенное значение). Компилятор использует её, когда
// результат присваивания нужен как значение выражения.
func (vm *VM) opIndexSetRes(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSet(regs, consts, chunk, in, true, true)
}

// opIndexSetRaw — как OpIndexSet, но без пометки записываемого значения
// разделённым. Только для writeback контейнера-приёмника: значение — это
// внутренний регистр компилятора (прочитанный OpIndexRaw контейнер), который
// умирает сразу после записи, поэтому второго владельца не создаёт.
func (vm *VM) opIndexSetRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSet(regs, consts, chunk, in, false, false)
}

// indexSet — общая реализация записи элемента (share управляет пометкой
// записываемого значения разделённым, ret — записью результата присваивания
// в регистр значения).
func (vm *VM) indexSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, share, ret bool) execResult {
	return vm.indexSetInner(regs, consts, chunk, in, share, ret)
}

func (vm *VM) indexSetInner(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, share, ret bool) execResult {
	arr := deref(regs[in.A])
	if arr.typ == ObjectType {
		// ArrayAccess: $obj[$k] = $v -> offsetSet($k, $v).
		if ci := vm.classInfo(arr); ci != nil {
			if m := ci.lookupMethod("offsetSet"); m != nil {
				_, err := vm.invokeMethod(m, arr, ci, []Slot{regs[in.B], regs[in.C]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				if share {
					shareSlot(regs[in.C])
				}
				return execResult{}
			}
		}
		// Объект без ArrayAccess — Error (Zend).
		err := vm.objectAsArrayErr(arr)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if arr.typ == StringType {
		ns, res, serr := vm.stringOffsetSet(arr, regs[in.B], regs[in.C])
		if serr != nil {
			if !vm.handleThrow(chunk, serr) {
				return execResult{err: serr}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.A]
			wv := ns
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		if ret {
			{
				old := regs[in.C]
				wv := res
				regs[in.C] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.C), old)
				}
			}
		}
		return execResult{}
	}
	if arr.typ != ArrayType {
		var werr *Throwable
		arr, werr = vm.writeBaseArray(arr, false)
		if werr != nil {
			if !vm.handleThrow(chunk, werr) {
				return execResult{err: werr}
			}
			return execResult{next: true}
		}
	}
	// Быстрый путь целочисленного ключа: без конверсии в ArrayKey.
	if ks := deref(regs[in.B]); ks.typ == IntType {
		a := arr.AsArray().Unshare()
		cur, found := a.GetInt(ks.Int())
		if found && cur.typ == RefType {
			if r := cur.Ref(); r != nil {
				if err := vm.writeRef(r, regs[in.C], vm.chunk != nil && vm.chunk.Strict); err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.A]
					wv := MakeArray(a)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				if share {
					shareSlot(regs[in.C])
				}
				return execResult{}
			}
		}
		if found && cur.typ != RefType {
			vm.noteDropped(cur)
		}
		vm.noteStored(regs[in.C])
		vm.noteSelfRefArray(a, regs[in.C])
		if err := a.SetInt(ks.Int(), regs[in.C]); err != nil {
			return execResult{err: err}
		}
		{
			old := regs[in.A]
			wv := MakeArray(a)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		if share {
			shareSlot(regs[in.C])
		}
		return execResult{}
	}
	k, ok := vm.KeyOfWarn(regs[in.B])
	if !ok {
		err := vm.offsetTypeErr("access", regs[in.B])
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	a := arr.AsArray().Unshare()
	cur, found := a.Get(k)
	if found && cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if err := vm.writeRef(r, regs[in.C], vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := MakeArray(a)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			if share {
				shareSlot(regs[in.C])
			}
			return execResult{}
		}
	}
	if found && cur.typ != RefType {
		vm.noteDropped(cur)
	}
	vm.noteStored(regs[in.C])
	vm.noteSelfRefArray(a, regs[in.C])
	if err := a.Set(k, regs[in.C]); err != nil {
		return execResult{err: err}
	}
	{
		old := regs[in.A]
		wv := MakeArray(a)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	if share {
		shareSlot(regs[in.C])
	}

	return execResult{}
}

// IndexRefCheck проверяет присваивание по ссылке элементу ArrayAccess-объекта
// (`$obj[$k] = &$x`): Zend запрещает это (Error "Cannot assign by reference to
// an array dimension of an object"), а для offsetGet без возврата по ссылке
// предваряет Notice об отсутствии эффекта. Для массивов — no-op.
func (vm *VM) IndexRefCheck(container Slot) error {
	arr := deref(container)
	if arr.typ != ObjectType {
		return nil
	}
	ci := vm.classInfo(arr)
	if ci == nil {
		return nil
	}
	// WeakMap хранит значения во внутреннем handler'е (Zend:
	// zend_weakmap_read_dimension делает zval ячейкой-ссылкой при BP_VAR_W):
	// присваивание по ссылке разрешено и не проходит через offsetGet
	// (GH-20073).
	if ci.Name() == "WeakMap" {
		return nil
	}
	m := ci.lookupMethod("offsetGet")
	if m == nil {
		return nil
	}
	if !m.ByRefReturn {
		vm.Notice("Indirect modification of overloaded element of " + classDisplayName(ci) + " has no effect")
	}
	return NewThrowable("Error", "Cannot assign by reference to an array dimension of an object")
}

// opIndexSetOverload — запись элемента как косвенная модификация: для объекта
// с ArrayAccess::offsetGet без возврата по ссылке Zend печатает Notice и
// игнорирует запись; для массивов — обычная запись.
func (vm *VM) opIndexSetOverload(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSetOverload(regs, consts, chunk, in, true)
}

// opIndexSetOverloadRaw — как OpIndexSetOverload, но без пометки значения
// разделённым (writeback контейнера-приёмника, см. opIndexSetRaw).
func (vm *VM) opIndexSetOverloadRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.indexSetOverload(regs, consts, chunk, in, false)
}

func (vm *VM) indexSetOverload(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, share bool) execResult {
	arr := deref(regs[in.A])
	if arr.typ == ObjectType {
		if ci := vm.classInfo(arr); ci != nil {
			// WeakMap: чтение элемента для записи отдаёт zval самой записи
			// (Zend возвращает ссылку), поэтому косвенная модификация
			// ($map[$k][] = ..., $map[$k]++) допустима и записывается обратно.
			if ci.Name() == "WeakMap" {
				if m := ci.lookupMethod("offsetSet"); m != nil {
					_, err := vm.invokeMethod(m, arr, ci, []Slot{regs[in.B], regs[in.C]})
					if err != nil {
						if !vm.handleThrow(chunk, err) {
							return execResult{err: err}
						}
						return execResult{next: true}
					}
					if share {
						shareSlot(regs[in.C])
					}
					return execResult{}
				}
			}
			if m := ci.lookupMethod("offsetGet"); m != nil {
				if !m.ByRefReturn {
					// offsetGet без ссылки: косвенная модификация не имеет
					// эффекта (Zend печатает Notice).
					vm.Notice("Indirect modification of overloaded element of " + classDisplayName(ci) + " has no effect")
					return execResult{}
				}
				// offsetGet вернул ссылку: модификация уже выполнена через
				// неё, write-back через offsetSet не выполняется (Zend).
				return execResult{}
			}
		}
	}
	return vm.indexSet(regs, consts, chunk, in, share, false)
}

// writeBaseArray приводит базу записи по индексу/append к массиву по правилам
// Zend (zend_fetch_dimension_address): null и false -> массив (для false с
// Deprecated), остальные скаляры — Error. appendOp=true отличает `$x[] = v`
// (строка запрещена особой формулировкой).
func (vm *VM) writeBaseArray(base Slot, appendOp bool) (Slot, *Throwable) {
	switch base.typ {
	case ArrayType:
		return base, nil
	case NullType:
		return MakeArray(newArray()), nil
	case BoolType:
		if !base.ToBool() {
			vm.Deprecated("Automatic conversion of false to array is deprecated")
			return MakeArray(newArray()), nil
		}
	case StringType:
		if appendOp {
			return Slot{}, NewThrowable("Error", "[] operator not supported for strings")
		}
	}
	return Slot{}, NewThrowable("Error", "Cannot use a scalar value as an array")
}

// objectAsArrayErr — Error для записи по индексу в объект без ArrayAccess.
func (vm *VM) objectAsArrayErr(obj Slot) *Throwable {
	ci := vm.classInfo(obj)
	name := "object"
	if ci != nil {
		name = ci.Name()
	}
	return NewThrowable("Error", "Cannot use object of type "+name+" as array")
}

func (vm *VM) opAppend(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	arr := deref(regs[in.A])
	if arr.typ == ObjectType {
		// ArrayAccess: $obj[] = $v -> offsetSet(null, $v).
		if ci := vm.classInfo(arr); ci != nil {
			if m := ci.lookupMethod("offsetSet"); m != nil {
				_, err := vm.invokeMethod(m, arr, ci, []Slot{MakeNull(), regs[in.B]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				shareSlot(regs[in.B])
				return execResult{}
			}
		}
		err := vm.objectAsArrayErr(arr)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if arr.typ != ArrayType {
		var werr *Throwable
		arr, werr = vm.writeBaseArray(arr, true)
		if werr != nil {
			if !vm.handleThrow(chunk, werr) {
				return execResult{err: werr}
			}
			return execResult{next: true}
		}
	}
	a := arr.AsArray().Unshare()
	vm.noteStored(regs[in.B])
	vm.noteSelfRefArray(a, regs[in.B])
	if _, err := a.Append(regs[in.B]); err != nil {
		if err == errNextOccupied {
			err = NewThrowable("Error", err.Error())
		}
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := MakeArray(a)
		regs[in.A] = wv
		// Append обычно возвращает в регистр тот же массив (a — результат
		// Unshare без копирования): значение не изменилось, и учёт уже
		// выставлен — вызов wRegSlow (no-op по old==v) не нужен. Достаточно
		// проверить бит; при old != wv барьер обязателен всегда (wv —
		// массив, его надо учесть).
		if old != wv || vm.cur.nTracked == 0 || vm.cur.tracked[in.A>>6]&(1<<uint(in.A&63)) == 0 {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(regs[in.B])

	return execResult{}
}

func (vm *VM) opLen(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if deref(regs[in.B]).typ == ArrayType {
		{
			old := regs[in.A]
			wv := MakeInt(int64(deref(regs[in.B]).AsArray().Len()))
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	} else {
		{
			old := regs[in.A]
			wv := MakeInt(1) // count(scalar) == 1
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}

	return execResult{}
}

func (vm *VM) opUnset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if arr := deref(regs[in.A]); arr.typ == ObjectType {
		// ArrayAccess: unset($obj[$k]) -> offsetUnset($k).
		if ci := vm.classInfo(arr); ci != nil {
			if m := ci.lookupMethod("offsetUnset"); m != nil {
				if _, err := vm.invokeMethod(m, arr, ci, []Slot{regs[in.B]}); err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
				}
				return execResult{next: true}
			}
		}
		err := vm.objectAsArrayErr(arr)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if arr := deref(regs[in.A]); arr.typ != ArrayType && arr.typ != NullType {
		// Скаляры: строка/число — Error, false — Deprecated с конверсией в
		// массив (Zend; фактического удаления не происходит).
		var err *Throwable
		switch arr.typ {
		case StringType:
			err = NewThrowable("Error", "Cannot unset string offsets")
		case BoolType:
			if !arr.ToBool() {
				vm.Deprecated("Automatic conversion of false to array is deprecated")
			} else {
				err = NewThrowable("Error", "Cannot unset offset in a non-array variable")
			}
		default:
			err = NewThrowable("Error", "Cannot unset offset in a non-array variable")
		}
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		return execResult{}
	}
	if arr := deref(regs[in.A]); arr.typ == ArrayType {
		if ks := deref(regs[in.B]); ks.typ == IntType {
			a := arr.AsArray().Unshare()
			a.UnsetInt(ks.Int())
			{
				old := regs[in.A]
				wv := MakeArray(a)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		} else if k, ok := vm.KeyOfWarn(regs[in.B]); ok {
			a := arr.AsArray().Unshare()
			a.Unset(k)
			// unset($GLOBALS['x']) удаляет и саму глобальную переменную $x:
			// $GLOBALS в Zend — таблица символов main-скоупа (CV становится
			// UNDEF, чтение печатает «Undefined variable»).
			if g := deref(vm.globals["$GLOBALS"]); g.typ == ArrayType && g.AsArray() == arr.AsArray() {
				delete(vm.globals, "$"+k.PHPString())
			}
			{
				old := regs[in.A]
				wv := MakeArray(a)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		} else {
			err := vm.offsetTypeErr("unset", regs[in.B])
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}

	return execResult{}
}

func (vm *VM) opIndexIsset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	src := deref(regs[in.B])
	switch src.typ {
	case ObjectType:
		if ci := vm.classInfo(src); ci != nil {
			if m := ci.lookupMethod("offsetExists"); m != nil {
				res, err := vm.invokeMethod(m, src, ci, []Slot{regs[in.C]})
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.A]
					wv := MakeBool(deref(res).ToBool())
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				break
			}
		}
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	case ArrayType:
		if ks := deref(regs[in.C]); ks.typ == IntType {
			v, ok := src.AsArray().GetInt(ks.Int())
			{
				old := regs[in.A]
				wv := MakeBool(ok && deref(v).typ != NullType)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			break
		}
		// Ключ конвертируется с диагностиками Zend и в isset-контексте:
		// float — Deprecated о потере точности, resource — Warning и int
		// (KeyOfWarn).
		k, ok := vm.KeyOfWarn(regs[in.C])
		if !ok {
			err := NewThrowable("TypeError", "Cannot access offset of type "+
				operandTypeName(deref(regs[in.C]))+" in isset or empty")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		v, ok := src.AsArray().Get(k)
		{
			old := regs[in.A]
			wv := MakeBool(ok && deref(v).typ != NullType)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	case StringType:
		// Строковый offset: Zend принимает числовые строки (в т.ч. с ведущими
		// нулями и пробелами); нечисловые/дробные — false. Float-ключ даёт
		// Deprecated о потере точности.
		if ks := deref(regs[in.C]); ks.typ == FloatType {
			vm.floatToIntWarn(ks)
		}
		i, ok, illegal := vm.stringOffsetKey(regs[in.C])
		if !ok || illegal {
			{
				old := regs[in.A]
				wv := MakeBool(false)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			break
		}
		s := src.StringVal()
		if i < 0 {
			i += int64(len(s))
		}
		{
			old := regs[in.A]
			wv := MakeBool(i >= 0 && i < int64(len(s)))
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	default:
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}

	return execResult{}
}

func (vm *VM) opIsObject(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	{
		old := regs[in.A]
		wv := MakeBool(isObjectSlot(regs[in.B]))
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opIterInit(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	var st Slot
	var err error
	switch in.C {
	case 2:
		// Позиционный обход массива по ссылке (foreach by reference).
		st = vm.iterInitArrayRef(regs[in.B])
		vm.anyArrayRefIter = true
		vm.addIterHint(vm.fp, int(in.A))
	case 1:
		st, err = vm.iterInitByRef(regs[in.B])
		// ArrayIterator по ссылке использует позиционное состояние обхода
		// (iterModeArrayRef) — регистрируем его для пересчёта курсоров при
		// перестройке массива (iterremap.go).
		if err == nil {
			if a := deref(st).AsArray(); a != nil && a.iterState {
				vm.anyArrayRefIter = true
				vm.addIterHint(vm.fp, int(in.A))
			}
		}
	default:
		st, err = vm.iterInit(regs[in.B])
	}
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := st
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opIterValid(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	ok, err := vm.iterValid(regs[in.B])
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := MakeBool(ok)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opIterKey(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	res, err := vm.iterKey(regs[in.B])
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := res
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(res)

	return execResult{}
}

func (vm *VM) opIterCurrent(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	res, err := vm.iterCurrent(regs[in.B])
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := res
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(res)

	return execResult{}
}

func (vm *VM) opIterCurrentRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	res, err := vm.iterCurrentRef(regs[in.B])
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := res
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(res)

	return execResult{}
}

func (vm *VM) opIterNext(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if err := vm.iterNext(regs[in.B]); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}

	return execResult{}
}
