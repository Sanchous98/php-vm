package vm

// Type — тег типа значения. Соответствует типам PHP там, где они нам нужны:
// null, bool, int, float, string. Позже добавятся array, object, reference.
//
// Имена совпадают с типами PHP (int/float), а не с Go (int64/float64), чтобы
// код читался в терминах PHP-семантики.
type Type uint8

const (
	NullType Type = iota
	BoolType
	IntType
	FloatType
	StringType
	ObjectType
	ArrayType
	RefType
	ClosureType
	ResourceType
)

var typeNames = [...]string{
	NullType:     "null",
	BoolType:     "bool",
	IntType:      "int",
	FloatType:    "float",
	StringType:   "string",
	ObjectType:   "object",
	ArrayType:    "array",
	RefType:      "reference",
	ClosureType:  "Closure",
	ResourceType: "resource",
}

func (t Type) String() string {
	if int(t) < len(typeNames) {
		return typeNames[t]
	}
	return "unknown"
}
