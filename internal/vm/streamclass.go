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
