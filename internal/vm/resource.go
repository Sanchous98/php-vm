package vm

import "unsafe"

// Resource — значение PHP-типа resource.
//
// ID — уникальный (в пределах процесса) номер ресурса, он же печатается в
// var_dump как resource(ID) и возвращается (int)$resource/get_resource_id().
// Kind — вид ресурса без обёртки: "stream", "directory", "stream-context", …
// Val — полезная нагрузка конкретного вида (напр. *fsHandle из пакета php;
// vm его не интерпретирует). Closed выставляется закрывающим вызовом
// (fclose/closedir) и превращает ресурс в resource(N) of type (Unknown).
type Resource struct {
	ID     int64
	Kind   string
	Val    any
	Closed bool

	// Refs — число учтённых владельцев (регистров) для ресурсов с OnDrop.
	// Обычные потоки не считаются.
	Refs int32
	// OnDrop — отложенный финализатор (ресурс процесса proc_open: закрыть
	// пайпы, не дожидаясь ребёнка). Вызывается из точки OpSweep, когда Refs
	// доходит до нуля; nil — ресурс не финализируется.
	OnDrop func()
	// dropped — финализатор уже поставлен в очередь (защита от дублей).
	dropped bool
}

// MakeResource боксит ресурс в слот. id дублируется в поле num, чтобы
// (int)$resource и get_resource_id не требовали разыменования.
func MakeResource(id int64, kind string, val any) Slot {
	return Slot{typ: ResourceType, num: uint64(id), ptr: unsafe.Pointer(&Resource{ID: id, Kind: kind, Val: val})}
}

// AsResource возвращает *Resource из слота-ресурса (nil для не-ресурса).
func AsResource(s Slot) *Resource {
	s = deref(s)
	if s.typ != ResourceType || s.ptr == nil {
		return nil
	}
	return (*Resource)(s.ptr)
}

// ResourceID возвращает id ресурса (0 для не-ресурса).
func ResourceID(s Slot) int64 {
	s = deref(s)
	if s.typ != ResourceType {
		return 0
	}
	return int64(s.num)
}

// ResourceKind возвращает вид ресурса ("stream"/"directory"/…) или "".
// Для закрытого ресурса возвращает "" (PHP отдаёт "Unknown").
func ResourceKind(s Slot) string {
	if r := AsResource(s); r != nil {
		return r.Kind
	}
	return ""
}

// ResourceClosed сообщает, что ресурс закрыт (или не является ресурсом).
func ResourceClosed(s Slot) bool {
	r := AsResource(s)
	return r == nil || r.Closed
}
