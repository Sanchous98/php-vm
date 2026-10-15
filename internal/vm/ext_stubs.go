package vm

import "strings"

// ext_stubs.go — минимальные заглушки расширений (PDO, curl, intl, simplexml),
// нужные тестам Zend/tests/bug*: регистрируются классы/методы, которых
// достаточно для наблюдаемого в тестах поведения.

// pdoStmtData — состояние PDOStatement-заглушки.
type pdoStmtData struct {
	Object
	query string
}

// curlFileData — CURLFile: у отключённого класса свойств нет (disable_classes),
// у обычного — объявленные name/mime/postname; тесты задевают только disabled.
type curlFileData struct{ Object }

// intlCharData — IntlChar (статические методы).
type intlCharData struct{ Object }

// simpleXmlData — SimpleXMLElement.
type simpleXmlData struct{ Object }

// CtxRef — контекст VM для встроенных функций пакета php.
func (vm *VM) CtxRef() *Context { return &vm.ctx }

// ObjectSlotOf — слот объекта по дескриптору класса (хендл выдаётся).
func ObjectSlotOf(ci ClassInfo, o *Object) Slot {
	if rc, ok := ci.(*runtimeClass); ok {
		return rc.objectSlot(o, -1)
	}
	return Slot{}
}

// NewExtClasses создаёт дескрипторы классов-заглушек расширений.
func NewExtClasses() []ClassInfo {
	pdoExc := RegisterClass[Object]("PDOException", nil)
	pdo := RegisterClass[Object]("PDO", nil)

	// Константы PDO (значения php 8.3).
	for name, v := range map[string]int64{
		"FETCH_DEFAULT": 0, "FETCH_LAZY": 1, "FETCH_ASSOC": 2, "FETCH_NUM": 3,
		"FETCH_BOTH": 4, "FETCH_OBJ": 5, "FETCH_BOUND": 6, "FETCH_COLUMN": 7,
		"FETCH_CLASS": 8, "FETCH_INTO": 9, "FETCH_FUNC": 10, "FETCH_NAMED": 11,
		"FETCH_KEY_PAIR": 12, "FETCH_GROUP": 65536, "FETCH_UNIQUE": 196608,
		"FETCH_CLASSTYPE": 262144, "FETCH_SERIALIZE": 524288,
		"FETCH_PROPS_LATE": 1048576, "ATTR_ERRMODE": 3, "ERRMODE_SILENT": 0,
		"ERRMODE_WARNING": 1, "ERRMODE_EXCEPTION": 2,
		"FETCH_ORI_NEXT": 0, "FETCH_ORI_PRIOR": 1, "FETCH_ORI_FIRST": 2,
		"FETCH_ORI_LAST": 3, "FETCH_ORI_ABS": 4, "FETCH_ORI_REL": 5,
	} {
		pdo.DefineConst(name, MakeInt(v))
	}
	pdo.Method("__construct", &Function{Name: "__construct", NumArgs: 1, NumRequired: 1,
		ParamNames: []string{"dsn", "username", "password", "options"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeNull(), nil
		}}})
	stmt := RegisterClass[pdoStmtData]("PDOStatement", nil)
	pdoExc.Implements()
	pdo.Method("query", &Function{Name: "query", NumArgs: 1, NumRequired: 1,
		ParamNames: []string{"query", "fetchMode"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			st := stmt.New(&ctx.vm.ctx, nil)
			if st == nil {
				return MakeBool(false), nil
			}
			if len(args) > 0 {
				if d := objectAs[pdoStmtData](st, stmt); d != nil {
					d.query = args[0].ToString()
				}
			}
			return ctx.vm.slotOf(st), nil
		}}})
	pdo.Method("prepare", &Function{Name: "prepare", NumArgs: 1, NumRequired: 1,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			st := stmt.New(&ctx.vm.ctx, nil)
			if st == nil {
				return MakeBool(false), nil
			}
			return ctx.vm.slotOf(st), nil
		}}})
	pdo.Method("exec", &Function{Name: "exec", NumArgs: 1, NumRequired: 1,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeInt(0), nil
		}}})
	pdo.Method("lastInsertId", &Function{Name: "lastInsertId", NumArgs: 0,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeString("0"), nil
		}}})

	// PDOStatement::fetch(int $mode = PDO::FETCH_DEFAULT, int $orientation = PDO::FETCH_ORI_NEXT, int $offset = 0)
	stmt.Method("fetch", &Function{Name: "fetch", NumArgs: 3, NumRequired: 0,
		ParamNames: []string{"mode", "cursorOrientation", "cursorOffset"},
		ParamTypes: []string{"int", "int", "int"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeBool(false), nil
		}}})
	stmt.Method("fetchObject", &Function{Name: "fetchObject", NumArgs: 2, NumRequired: 0,
		ParamNames: []string{"class", "constructorArgs"},
		ParamTypes: []string{"?string", "array"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			name := "stdClass"
			if len(args) > 0 && deref(args[0]).Type() != NullType {
				name = deref(args[0]).ToString()
			}
			if !validClassName(name) {
				return Slot{}, NewThrowable("TypeError",
					"PDOStatement::fetchObject(): Argument #1 ($class) must be a valid class name, "+name+" given")
			}
			ci := ctx.vm.findClass(strings.TrimLeft(name, "\\"))
			if ci == nil {
				return Slot{}, NewThrowable("Error", "Class \""+strings.TrimLeft(name, "\\")+"\" not found")
			}
			obj := ci.New(&ctx.vm.ctx, nil)
			if obj == nil {
				return MakeBool(false), nil
			}
			return ctx.vm.slotOf(obj), nil
		}}})
	stmt.Method("execute", &Function{Name: "execute", NumArgs: 1, NumRequired: 0,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeBool(true), nil
		}}})
	stmt.Method("bindValue", &Function{Name: "bindValue", NumArgs: 3, NumRequired: 2,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeBool(true), nil
		}}})
	stmt.Method("fetchAll", &Function{Name: "fetchAll", NumArgs: 3, NumRequired: 0,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeArray(NewArray()), nil
		}}})
	stmt.Method("setFetchMode", &Function{Name: "setFetchMode", NumArgs: 2, NumRequired: 1,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeBool(true), nil
		}}})

	// CURLFile: объект без объявленных свойств (для disable_classes-теста
	// достаточно, что класс существует и new выдаёт warning).
	curlFile := RegisterClass[curlFileData]("CURLFile", nil)
	curlFile.Constructor(&Function{Name: "__construct", NumArgs: 3, NumRequired: 1,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeNull(), nil
		}}})

	// IntlChar: статические методы (enumCharTypes достаточно принимать callable).
	intlChar := RegisterClass[intlCharData]("IntlChar", nil)
	intlChar.SetFinal()
	intlChar.Method("enumCharTypes", &Function{Name: "enumCharTypes", NumArgs: 1, NumRequired: 1,
		IsStatic: true, ParamNames: []string{"callback"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			if len(args) > 0 && deref(args[0]).Type() == StringType {
				s := strings.ToLower(deref(args[0]).ToString())
				for _, kw := range []string{"self", "parent", "static"} {
					if strings.HasPrefix(s, kw+"::") {
						ctx.vm.Deprecated(`Use of "` + kw + `" in callables is deprecated`)
						break
					}
				}
			}
			return MakeNull(), nil
		}}})
	intlChar.Method("isIDIgnorable", &Function{Name: "isIDIgnorable", NumArgs: 1, NumRequired: 1,
		IsStatic: true, Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeBool(false), nil
		}}})
	intlChar.Method("isIDStart", &Function{Name: "isIDStart", NumArgs: 1, NumRequired: 1,
		IsStatic: true, Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeBool(false), nil
		}}})

	// SimpleXMLElement.
	simpleXML := RegisterClass[simpleXmlData]("SimpleXMLElement", nil)
	simpleXML.Constructor(&Function{Name: "__construct", NumArgs: 3, NumRequired: 1,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeNull(), nil
		}}})

	return []ClassInfo{pdoExc, pdo, stmt, curlFile, intlChar, simpleXML}
}

// validClassName — синтаксическая проверка имени класса (Zend: идентификатор,
// возможно с пространством имён).
func validClassName(name string) bool {
	name = strings.TrimLeft(name, "\\")
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch == '\\' {
			continue
		}
		if ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= 0x80 || (i > 0 && ch >= '0' && ch <= '9') {
			continue
		}
		return false
	}
	return true
}
