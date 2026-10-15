package vm

// NewStreamClasses регистрирует стандартные классы потоков.
//
// php_user_filter — базовый класс для пользовательских фильтров
// (stream_filter_register). Свойства params/stream объявляются динамически;
// методы (filter/oncreate/onclose) определяет наследник.
func NewStreamClasses() []ClassInfo {
	uf := NewClass("php_user_filter", nil)
	uf.DeclareDefault("params", MakeString(""))
	uf.DeclareDefault("stream", MakeNull())
	return []ClassInfo{uf}
}

// HasConcreteMethod сообщает, что у класса есть настоящий метод name (в
// отличие от HasMethod не считает __call/__callStatic и не смотрит записи-
// подписи внутренних интерфейсов).
func (vm *VM) HasConcreteMethod(ci ClassInfo, name string) bool {
	return ci != nil && ci.lookupMethod(name) != nil
}
