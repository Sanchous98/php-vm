package php

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// --- AST: утверждения ---

type Program struct {
	Stmts []Stmt
	// Lines/ExprLines — 1-based номера строк для узлов (для диагностик VM).
	Lines     map[Stmt]int
	ExprLines map[Expr]int
	// StrictTypes — в файле объявлен declare(strict_types=1).
	StrictTypes bool
	// TypeWarnings — предупреждения о confusable-типах (integer/double/
	// boolean/resource), собранные в порядке разбора.
	TypeWarnings []TypeWarning
	// HaltOffset — смещение данных после `__halt_compiler();` (значение
	// магической константы __COMPILER_HALT_OFFSET__); -1 — нет.
	HaltOffset int64
}

// TypeWarning — предупреждение разбора о неоднозначном имени типа.
type TypeWarning struct {
	Line int
	Msg  string
}

type Stmt interface{}

type EchoStmt struct{ Args []Expr }
type ExprStmt struct{ X Expr }
type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
}
type WhileStmt struct {
	Cond Expr
	Body []Stmt
}
type DoWhileStmt struct {
	Body []Stmt
	Cond Expr
}
type ForStmt struct {
	Init, Cond, Post []Expr
	Body             []Stmt
}
type ForeachStmt struct {
	Expr Expr
	Key  string
	Val  string
	// KeyTarget/ValTarget — альтернативные цели-деструктуризации
	// (`foreach ($a as [$x, $y])` / `as $k => [$x, $y]`). Если заданы,
	// соответствующие Key/Val не используются.
	KeyTarget Expr
	ValTarget Expr
	ByRef     bool
	// KeyRef — ключ объявлен ссылкой (`as &$k => $v`) — фатальная ошибка.
	KeyRef bool
	Body   []Stmt
}
type SwitchStmt struct {
	Subject Expr
	Cases   []*SwitchCase
}
type SwitchCase struct {
	Cond    Expr // nil для default
	Default bool
	Body    []Stmt
}
type BreakStmt struct {
	Level int
	// OpKind — вид операнда `break N`: 0 — нет/целое, 1 — double/string
	// литерал, 2 — не литерал. Диагностика как в Zend (PHP 7+).
	OpKind int
}
type ContinueStmt struct {
	Level  int
	OpKind int
}
type ReturnStmt struct{ X Expr }
type FuncDecl struct {
	Name        string
	Params      []Param
	Body        []Stmt
	ByRefReturn bool
	RetType     string
	Attrs       []AttrDecl
	// Strict — функция объявлена в файле с declare(strict_types=1).
	Strict bool
	// Ticks — значение declare(ticks=N), действующее в точке объявления
	// (Zend компилирует tick-опкоды и в тела функций, объявленных в области
	// declare(ticks)).
	Ticks int
	// DefFile/DefLine — исходное место объявления (для inlined include).
	DefFile string
	DefLine int
}

// StrictStmt — граница региона с иным режимом strict_types, возникающая при
// инлайне include/eval. Push=true открывает регион Strict, Push=false
// восстанавливает предыдущий режим.
type StrictStmt struct {
	Push   bool
	Strict bool
	// File — путь исходного файла региона (для FuncDecl.DefFile/ClassDecl).
	File string
	// Lines/ExprLines — карты строк подключаемого файла (inline include):
	// диагностики внутри вклеенного кода должны указывать строки своего файла.
	Lines     map[Stmt]int
	ExprLines map[Expr]int
	// CallName — имя конструкции (include/require[_once]) для кадра трейса.
	CallName string
	// CallNode — узел IncludeExpr: строка конструкции берётся из карт строк
	// файла на этапе компиляции (нужна для кадра require в трейсе).
	CallNode Expr
}
type Param struct {
	Name             string
	Type             string
	Default          Expr
	ByRef            bool
	Promoted         bool   // параметр конструктора с модификатором видимости
	PromotedReadonly bool   // promoted-параметр с модификатором readonly
	PromotedVis      string // видимость promoted-свойства
	Variadic         bool   // ...$name
	Attrs            []AttrDecl
}

// AttrDecl — разобранный атрибут `Name(arg, name: arg, ...)`.
type AttrDecl struct {
	Name string
	Args []AttrArg
	// Line — строка объявления атрибута (для диагностик newInstance).
	Line int
}

// AttrArg — аргумент атрибута: необязательное имя (named argument) и значение.
type AttrArg struct {
	Name string
	Val  Expr
}

// ClassDecl — объявление класса/интерфейса/трейта.
type ClassDecl struct {
	Name       string
	Parent     string   // extends (для класса)
	Interfaces []string // implements (класс) / extends (интерфейс)
	Uses       []string // use TraitA, TraitB;
	// UsesLine — строка первого `use Trait` в теле класса (для диагностик
	// «Cannot use 'static' as trait name»: Zend указывает на `use`).
	UsesLine    int
	Attrs       []AttrDecl
	IsInterface bool
	IsTrait     bool
	IsAbstract  bool
	IsFinal     bool
	// IsReadonly — `readonly class`; ReadonlyDup — модификатор readonly
	// указан более одного раза (фатал на этапе компиляции).
	IsReadonly  bool
	ReadonlyDup bool
	// FinalDup — модификатор final указан более одного раза.
	FinalDup bool
	// IsEnum — enum-объявление; EnumBacking — "int"/"string"/"" (pure).
	IsEnum      bool
	EnumBacking string
	// Strict — класс объявлен в файле с declare(strict_types=1).
	Strict bool
	// DefFile — исходный файл объявления (для inlined include).
	DefFile string
	Members []*ClassMember
	// TraitAdapt — адаптации use Trait { ... } (insteadof / as).
	TraitAdapt []*TraitAdapt
	// Line — 1-based строка объявления класса (для диагностик компилятора).
	Line int
	// MissingParent/MissingInterface — имя родителя/интерфейса, неизвестного
	// на этапе компиляции (может быть определён автозагрузкой в рантайме).
	MissingParent    string
	MissingInterface string
	// MissingIfaces — все интерфейсы, неизвестные на этапе компиляции (нужны
	// для рантайм-объявления класса вне верхнего уровня).
	MissingIfaces []string
	// IfaceNotInterface — имя «интерфейса», который на самом деле класс;
	// для класса с рантайм-разрешаемым родителем Zend сообщает об этом при
	// связывании (после автозагрузки родителя), а не при компиляции.
	IfaceNotInterface string
	// RuntimeFatal — сообщение фатальной ошибки, которую PHP поднимает в
	// момент выполнения объявления класса (напр. нереализованные abstract-
	// методы). Печатается OpFatal на позиции объявления.
	RuntimeFatal string
}

// parseError — ошибка разбора, которую PHP печатает как "Parse error: ...".
type parseError struct {
	msg  string
	line int
	// bracketOrEOF — ошибка возникла на конце файла или на закрывающей скобке:
	// тогда PHP предпочитает диагностику несовпадающих скобок.
	bracketOrEOF bool
}

func (e *parseError) Error() string { return e.msg }

// TraitAdapt — одна адаптация трейта в `use T { ... }`.
type TraitAdapt struct {
	Trait         string   // Trait::method ("" — если не указан)
	Method        string   // method
	Insteadof     []string // Trait::method insteadof A, B
	Alias         bool     // метод содержит `as`
	AliasName     string   // as NAME ("" — только смена видимости)
	Vis           string   // as public|protected|private
	AliasFinal    bool     // as final
	AliasStatic   bool     // as static (запрещено)
	AliasAbstract bool     // as abstract (запрещено)
}

// ClassMember — член класса: свойство, константа или метод.
type ClassMember struct {
	Kind     string // "prop" | "const" | "method"
	Vis      string // "public" | "protected" | "private" | ""
	Static   bool
	StaticKw bool // модификатор static указан явно (для констант)
	Abstract bool
	Final    bool
	// FinalDup — модификатор final указан более одного раза.
	FinalDup bool
	Readonly bool
	// ReadonlyDup — модификатор readonly указан более одного раза.
	ReadonlyDup bool
	// FirstDup — какой модификатор продублирован первым ("access"/"final"/
	// "readonly"): PHP сообщает о первом нарушении в порядке объявления.
	FirstDup string

	PropName string
	Default  Expr
	PropType string
	// PromotedAttrs — атрибуты promoted-параметра (цель уже проверена как
	// параметр; при копировании в свойство повторно не валидируется).
	PromotedAttrs bool

	ConstName string
	ConstVal  Expr
	ConstType string

	MName       string
	Params      []Param
	Body        []Stmt
	ByRefReturn bool
	RetType     string
	// Ticks — declare(ticks=N), действующее при объявлении метода (tick-
	// опкоды компилируются и в тело метода, объявленного в этой области).
	Ticks int

	// Line — 1-based строка объявления члена (для диагностик компилятора).
	Line int
	// Doc — doc-комментарий `/** ... */` перед объявлением члена.
	Doc string
	// Attrs — атрибуты `#[...]` перед объявлением члена.
	Attrs []AttrDecl
}
type BlockStmt struct {
	Body []Stmt
	// Ticks — declare(ticks=N), действующее внутри блока (блочная форма
	// declare(ticks=N){...}); 0 — обычный блок.
	Ticks int
}

// TicksStmt — файловая форма declare(ticks=N); (без тела): tick-опкоды
// эмитятся для последующих операторов текущей лексической области.
type TicksStmt struct {
	N int
}
type UnsetStmt struct{ Args []Expr }
type GotoStmt struct {
	Label string
	Line  int
}
type LabelStmt struct {
	Name string
	Line int
}
type ThrowStmt struct{ X Expr }
type GlobalStmt struct {
	Names []string
	// Dyn — динамические имена (`global $$x;` / `global ${expr};`): выражения
	// вычисляются в точке оператора (побочные эффекты), привязка — в рантайме.
	Dyn []Expr
}
type StaticVarStmt struct {
	Names   []string
	Default []Expr
}
type TryStmt struct {
	Body       []Stmt
	Catches    []*CatchClause
	Finally    []Stmt
	HasFinally bool
}
type CatchClause struct {
	Classes []string
	Var     string
	Body    []Stmt
	// Line — строка `catch` (диагностика "Cannot re-assign $this").
	Line int
}

// --- AST: выражения ---

type Expr interface{}

type IntLit struct{ V int64 }
type FloatLit struct {
	V   float64
	Val string
}
type StrLit struct{ V string }
type BoolLit struct{ V bool }
type NullLit struct{}

type ArrayElem struct {
	Key    Expr // nil => автоиндекс
	Val    Expr
	ByRef  bool
	Spread bool // ...$val (array unpack)
	// Line — строка пустого элемента `[1, , 2]` (для диагностики компилятора).
	Line int
}
type ArrayLit struct {
	Elems []ArrayElem
	// Long — литерал записан как array(...) (в отличие от [...]); важно для
	// диагностики "Cannot assign to array(), use [] instead".
	Long bool
}

type VarExpr struct {
	Name string
	// NameExpr — динамическое имя переменной ($$x, ${expr}, $$$x). Если
	// не nil, Name игнорируется, а имя вычисляется из выражения в рантайме.
	NameExpr Expr
}
type IndexExpr struct {
	Arr Expr
	Key Expr // nil => $a[]
}
type PropExpr struct {
	Recv Expr
	Name string
	// NameExpr — динамическое имя свойства ($obj->$p, $obj->{expr}).
	NameExpr Expr
}
type BinExpr struct {
	Op   string
	L, R Expr
}
type LogicalExpr struct {
	Op   string // "&&", "||", "and", "or", "xor"
	L, R Expr
}
type UnaryExpr struct {
	Op string
	X  Expr
}
type IncDecExpr struct {
	Op  string // "++" | "--"
	Pre bool
	X   Expr
}
type AssignExpr struct {
	Target Expr
	Op     string // "=", "+=", "-=", "*=", "/=", ".=", "%="
	Value  Expr
	ByRef  bool // $a = &$b
}

// TernaryExpr — `cond ? then : else` (Then == nil для короткой формы `?:`).
// Paren — выражение было в скобках: `(a ? b : c) ? d : e` допустимо, а
// непарентетизированное правое вложение тернарников — компиляторный фатал.
type TernaryExpr struct {
	Cond, Then, Else Expr
	Paren            bool
}
type CoalesceExpr struct{ L, R Expr }
type CallExpr struct {
	Name string
	Args []Expr
	// AssertText — исходный текст первого аргумента assert() (для диагностик
	// Zend: "assert($a != 0)"); заполняется только для вызовов assert.
	AssertText string
	// Fallback — глобальное (непространственное) имя функции, к которому нужно
	// откатиться, если Name не найдено (PHP-семантика fallback для
	// неквалифицированных вызовов внутри namespace). Пусто — отката нет.
	Fallback string
	// ConstStmt — вызов define() из оператора `const NAME = ...;`: значение
	// вычисляется в контексте константного выражения (zend_ast_evaluate).
	ConstStmt bool
}

// NamedArgExpr — именованный аргумент вызова (PHP 8): `name: value`.
// Компилятор раскладывает его в позицию по имени параметра.
type NamedArgExpr struct {
	Name string
	X    Expr
}

// ArgSpreadExpr — распакованный аргумент вызова (PHP 8): `f(...$args)`.
type ArgSpreadExpr struct{ X Expr }

type MethodCallExpr struct {
	Recv Expr
	Name string
	// NameExpr — динамическое имя метода ($obj->$m(...)).
	NameExpr Expr
	Args     []Expr
}
type StaticCallExpr struct {
	Class string
	// ClassExpr — класс задан произвольным выражением: EXPR::m(...) (PHP 8).
	ClassExpr Expr
	Name      string
	// NameExpr — динамическое имя статического метода (Class::$m(...)).
	NameExpr Expr
	Args     []Expr
}
type NewExpr struct {
	Class     string
	ClassExpr Expr // non-nil => new $var(...)
	Args      []Expr
}

// NewAnonClassExpr — анонимный класс: `new class(args) extends ... { ... }`.
type NewAnonClassExpr struct {
	Decl *ClassDecl
	Args []Expr
}
type DynCallExpr struct {
	Name Expr
	Args []Expr
}
type StaticPropExpr struct {
	Class string
	// ClassExpr — класс задан произвольным выражением: EXPR::$prop (PHP 8).
	ClassExpr Expr
	Name      string
	// NameExpr — динамическое имя статического свойства (Class::${e}).
	NameExpr Expr
}
type ConstExpr struct {
	Name string
	// ClassExpr — константа класса, заданного выражением: EXPR::CONST (PHP 8).
	ClassExpr Expr
	// NameExpr — динамическое имя константы: Class::{expr} (PHP 8.3).
	NameExpr Expr
	// Fallback — глобальное имя константы для отката (см. CallExpr.Fallback).
	Fallback string
}
type CastExpr struct {
	Type string
	X    Expr
}
type MagicExpr struct {
	Name string // __LINE__, __FUNCTION__, ...
	Line int
	// NS — значение __NAMESPACE__ (текущее пространство имён на момент разбора).
	NS string
}

// ClassKeywordExpr — выражение `Class::class` (PHP 8): возвращает FQ-имя
// класса. self/static/parent разрешаются в рантайме, именованный класс — на
// этапе компиляции (существование класса не проверяется, как в PHP).
type ClassKeywordExpr struct {
	Class string // self/static/parent или FQ-имя
	// ClassExpr — класс задан произвольным выражением: EXPR::class (PHP 8).
	ClassExpr Expr
}

// MatchArm — ветвь выражения match. Default=true означает ветвь `default`
// (Conds не используется).
type MatchArm struct {
	Conds   []Expr
	Default bool
	Body    Expr
	// Line — строка начала ветви (для диагностики дублирующегося default).
	Line int
}

// MatchExpr — выражение `match (subject) { conds => body, ... }` (PHP 8).
type MatchExpr struct {
	Subject Expr
	Arms    []MatchArm
}

// NullsafeExpr — цепочка обращений, начинающаяся с `?->`: если Recv === null,
// вычисление всей цепочки Body прекращается и результатом становится null.
// В Body на месте базового значения стоит NullsafeBase.
type NullsafeExpr struct {
	Recv Expr
	Body Expr
}

// NullsafeBase — маркер базового значения внутри NullsafeExpr.Body.
type NullsafeBase struct{}

// ListElem — элемент цели-деструктуризации (list(...) / [...] = ...).
type ListElem struct {
	Key    Expr // ключ для list("k"=>$v); nil => позиционный
	Target Expr // nil => пропуск (list(,$b))
	ByRef  bool // &$v — ссылочная распаковка
	// Spread — `...$v` в цели деструктуризации: компилятор выдаёт фатал
	// «Spread operator is not supported in assignments».
	Spread bool
}
type ListExpr struct {
	Elems []ListElem
	// Style — синтаксис цели деструктуризации (ZEND_ARRAY_SYNTAX_*): короткий
	// [...] = ..., list(...) = ... или array(...) (недопустимая цель).
	Style int
}

// Стили синтаксиса деструктуризации (Zend ZEND_ARRAY_SYNTAX_*).
const (
	listStyleShort = 1 // [...] = ...
	listStyleList  = 2 // list(...) = ...
	listStyleArray = 3 // array(...) = ... — запрещено
)

// RegExpr — внутренний узел: значение уже лежит в регистре. Используется
// компилятором для присваивания результата в lvalue (деструктуризация).
type RegExpr struct{ Reg uint8 }

// IncludeExpr — языковая конструкция include/include_once/require/require_once.
// Это выражение: `$x = include "f.php";` или `include "f.php";`.
type IncludeExpr struct {
	Path    Expr
	Once    bool // include_once / require_once
	Require bool // require / require_once
}
type IssetExpr struct{ Args []Expr }
type EmptyExpr struct{ X Expr }
type PrintExpr struct{ X Expr }
type ExitExpr struct{ X Expr }
type CloneExpr struct{ X Expr }
type ThrowExpr struct{ X Expr }
type InstanceOfExpr struct {
	X     Expr
	Class string
	// ClassExpr — динамическое имя класса (`instanceof (expr)`, `instanceof $c`).
	ClassExpr Expr
}

// ClosureExpr — анонимная функция: function (params) use (...) { body }.
type ClosureExpr struct {
	Params      []Param
	Uses        []Param // ByRef отмечает use (&$x)
	ByRefReturn bool
	Static      bool
	RetType     string // объявленный тип возврата (`: T`), "" — не объявлен
	Body        []Stmt
	// Attrs — атрибуты `#[...]` перед `function` (PHP 8).
	Attrs []AttrDecl
	// Line — строка объявления (`function ...`), для диагностик неявных
	// преобразований аргументов (Zend приписывает их объявлению функции).
	Line int
	// Ticks — declare(ticks=N), действующее в точке объявления замыкания:
	// Zend эмитит ZEND_TICKS в op_array, скомпилированном при активной
	// директиве (фикс: тики в теле замыкания, fibers/ticks).
	Ticks int
}

// ArrowFnExpr — стрелочная функция: fn (params) => expr. Захватывает внешние
// переменные по значению неявно.
type ArrowFnExpr struct {
	Params  []Param
	Body    Expr
	Static  bool
	RetType string // объявленный тип возврата (`: T`), "" — не объявлен
	// ByRefReturn — `fn& (...) => ...` (возврат по ссылке).
	ByRefReturn bool
	// Attrs — атрибуты `#[...]` перед `fn` (PHP 8).
	Attrs []AttrDecl
	// Line — строка объявления `fn ...`.
	Line int
	// Ticks — declare(ticks=N) в точке объявления (как у ClosureExpr).
	Ticks int
}

// YieldExpr — выражение генератора:
//
//	yield              — Key == nil, Val == nil
//	yield $v           — Key == nil, Val == $v
//	yield $k => $v     — Key == $k,   Val == $v
//	yield from $it     — From == true, Val == $it
//
// Любая функция, тело которой содержит YieldExpr, компилируется как функция-
// генератор (возвращает объект Generator).
type YieldExpr struct {
	Key  Expr // nil — автоматический целочисленный ключ
	Val  Expr // nil для `yield;` (значение NULL)
	From bool // `yield from`
}

// --- Parser ---

type Parser struct {
	// attrErr — ошибка, обнаруженная при разборе атрибутов (дубликат имени,
	// позиционный аргумент после именованного, неконстантное выражение,
	// динамическое имя класса, распаковка). fatalError печатается как Fatal
	// error, parseError — как Parse error.
	attrErr error
	// noIncDec > 0 — внутри nullsafe-цепочки постфиксные ++/-- не являются
	// частью звена (Zend: `$a?->b++` — инкремент всей цепочки).
	noIncDec int
	// listTargetDepth > 0 — разбирается цель list(): вложенные list() не
	// проверяются на обязательный `=`.
	listTargetDepth int
	// arrayLitDepth > 0 — разбор элемента массива: list() внутри [] даёт
	// компайл-фатал «Cannot mix [] and list()», а не Parse error.
	arrayLitDepth int
	toks          []Token
	pos           int
	src           string
	depth         int

	// lineStarts — позиции начала строк (для pos→line). Строится лениво.
	lineStarts []int
	// stmtLines/exprLines — 1-based строки узлов (для диагностик VM).
	stmtLines map[Stmt]int
	exprLines map[Expr]int

	// Контекст пространства имён (заполняется по мере разбора).
	ns        string            // текущее пространство ("" — глобальное)
	classUses map[string]string // alias(ниж.регистр) -> FQ для классов
	funcUses  map[string]string // для use function
	constUses map[string]string // для use const (псевдоним регистрозависим, как в Zend)
	// constImports — таблица импортов const с точным регистром псевдонима
	// (имена констант регистрозависимы) и seenConsts — имена const-объявлений
	// и целей импорта в текущем файле (FC(seen_symbols) в Zend) для диагностик
	// "the name is already in use".
	constImports map[string]string
	seenConsts   map[string]bool
	// seenFuncs — объявленные в файле функции (нижний регистр, с пространством
	// имён): конфликт с псевдонимом `use function`.
	seenFuncs map[string]bool

	// anyStmt — был ли уже разобран хоть один оператор (для проверки, что
	// declare(strict_types=1) стоит в самом начале скрипта).
	anyStmt  bool
	declared bool

	// pendingClassUses — импорты классов в файле (для отложенной проверки
	// коллизий с объявлениями классов), seenClassLine/seenClassName — верхние
	// объявления классов (нижний регистр FQ-имени → строка/имя).
	pendingClassUses []pendingClassUse
	seenClassLine    map[string]int
	seenClassName    map[string]string

	// Состояние объявлений namespace: nsMode 0 — объявлений ещё не было,
	// 1 — bracketed (`namespace X {`), 2 — unbracketed (`namespace X;`).
	// nsDepth > 0 — разбор внутри bracketed-блока; nsBlockClosed — блок
	// namespace {} уже закрыт (код вне блоков после него запрещён).
	nsMode        int
	nsDepth       int
	nsBlockClosed bool

	// strictTypes — файл объявил declare(strict_types=1).
	strictTypes bool

	// ticks — значение declare(ticks=N), действующее для последующих
	// операторов текущей лексической области (0 — выключено).
	ticks int

	// typeErr — ошибка разбора, накопленная при разборе типа (например,
	// атрибут в середине intersection-типа). Проверяется через typeNameErr.
	typeErr error

	// typeWarnings — накопленные предупреждения о confusable-типах.
	typeWarnings []TypeWarning
}

// literalTruthy сообщает, что литеральное выражение истинно (для
// declare(strict_types=...)).
func literalTruthy(e Expr) bool {
	switch x := e.(type) {
	case *IntLit:
		return x.V != 0
	case *BoolLit:
		return x.V
	}
	return false
}

// pendingClassUse — импорт класса, отложенный до конца разбора файла.
type pendingClassUse struct {
	full  string
	alias string
	key   string
	line  int
}

// maxParseDepth ограничивает рекурсию парсера, чтобы глубоко вложенный вход
// (например, 17000 подряд "[") давал ошибку, а не переполнение стека Go.
const maxParseDepth = 1500

func (p *Parser) enter() error {
	p.depth++
	if p.depth > maxParseDepth {
		return fmt.Errorf("parser: превышена максимальная глубина вложенности")
	}
	return nil
}

func (p *Parser) leave() { p.depth-- }

func Parse(src string) (*Program, error) {
	HaltOffset = -1
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &Parser{toks: toks, src: src}
	for _, w := range lexWarnings {
		p.typeWarnings = append(p.typeWarnings, TypeWarning{Line: p.lineAt(w.pos), Msg: w.msg})
	}
	prog, perr := p.program()
	if p.attrErr != nil {
		return nil, p.attrErr
	}
	if perr != nil {
		if be := bracketDiag(toks, src, perr); be != nil {
			return nil, be
		}
		return nil, perr
	}
	prog.HaltOffset = HaltOffset
	return prog, nil
}

// bracketDiag возвращает диагностику несовпадающих скобок, если ошибка разбора
// возникла на закрывающей скобке/конце файла (как это делает Zend). Иначе nil.
func bracketDiag(toks []Token, src string, perr error) error {
	if pe, ok := perr.(*parseError); ok && !pe.bracketOrEOF {
		return nil
	}
	return checkBracketBalance(toks, src)
}

// checkBracketBalance повторяет диагностику Zend о несовпадающих скобках
// ("Unclosed '('", "Unmatched ')'", "Unclosed '(' does not match ']'"). Она
// выполняется до основного разбора, т.к. PHP сообщает именно эти сообщения.
func checkBracketBalance(toks []Token, src string) error {
	type open struct {
		ch   byte
		line int
	}
	var stack []open
	for _, t := range toks {
		if t.Kind != TOp {
			continue
		}
		switch t.Val {
		case "(", "[", "{":
			stack = append(stack, open{t.Val[0], lineOf(src, t.Pos)})
		case ")", "]", "}":
			want := byte('(')
			switch t.Val[0] {
			case ')':
				want = '('
			case ']':
				want = '['
			case '}':
				want = '{'
			}
			if len(stack) == 0 {
				return &parseError{msg: fmt.Sprintf("Unmatched '%c'", t.Val[0]), line: lineOf(src, t.Pos)}
			}
			top := stack[len(stack)-1]
			if top.ch != want {
				msg := fmt.Sprintf("Unclosed '%c'", top.ch)
				if top.line != lineOf(src, t.Pos) {
					msg += fmt.Sprintf(" on line %d", top.line)
				}
				msg += fmt.Sprintf(" does not match '%c'", t.Val[0])
				return &parseError{msg: msg, line: lineOf(src, t.Pos)}
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		top := stack[len(stack)-1]
		msg := fmt.Sprintf("Unclosed '%c'", top.ch)
		if eof := lineOf(src, len(src)); top.line != eof {
			msg += fmt.Sprintf(" on line %d", top.line)
		}
		return &parseError{msg: msg, line: top.line}
	}
	return nil
}

// ParseMixed разбирает смешанный PHP/HTML-файл: текст вне `<?php ... ?>`
// становится литеральным выводом (echo), несколько блоков конкатенируются.
// Открывающие теги `<?php`, `<?=` и `<?` распознаются.
func ParseMixed(src string) (*Program, error) {
	HaltOffset = -1
	var shShift int64
	src, shShift = stripShebangKeepLines(src)
	toks, err := lexMode(src, false)
	if err != nil {
		return nil, err
	}
	p := &Parser{toks: toks, src: src}
	for _, w := range lexWarnings {
		p.typeWarnings = append(p.typeWarnings, TypeWarning{Line: p.lineAt(w.pos), Msg: w.msg})
	}
	prog, perr := p.program()
	if p.attrErr != nil {
		return nil, p.attrErr
	}
	if perr != nil {
		if be := bracketDiag(toks, src, perr); be != nil {
			return nil, be
		}
		return nil, perr
	}
	prog.HaltOffset = HaltOffset
	if prog.HaltOffset >= 0 && shShift != 0 {
		// Смещение __COMPILER_HALT_OFFSET__ — в координатах исходного файла.
		prog.HaltOffset -= shShift
	}
	return prog, nil
}

// stripShebangKeepLines убирает первую строку-шебанг (`#!...`), но сохраняет
// нумерацию строк: строка заменяется пустым PHP-блоком, а перевод строки —
// закрывающим тегом, который его «съедает». Так __LINE__ остаётся корректным.
// Возвращает также сдвиг байтовых смещений после перезаписи (для
// __COMPILER_HALT_OFFSET__).
func stripShebangKeepLines(src string) (string, int64) {
	if !strings.HasPrefix(src, "#!") {
		return src, 0
	}
	if i := strings.IndexByte(src, '\n'); i >= 0 {
		const repl = "<?php ?>"
		return repl + src[i:], int64(len(repl) - i)
	}
	return "<?php ?>", 0
}

// ParseExpr разбирает одно выражение (для интерполяции строк).
func ParseExpr(src string) (Expr, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &Parser{toks: toks, src: src}
	return p.expr()
}

// parseAttrExpr разбирает выражение аргумента атрибута в контексте текущего
// пространства имён и use-алиасов объявления (иначе `ORM\JoinColumn::UNIQUE`
// резолвился бы как глобальное имя).
func (p *Parser) parseAttrExpr(src string) (Expr, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	sub := &Parser{toks: toks, src: src, ns: p.ns,
		classUses: p.classUses, funcUses: p.funcUses, constUses: p.constUses}
	return sub.expr()
}

// magicConsts — магические константы PHP.
var magicConsts = map[string]bool{
	"__LINE__": true, "__FUNCTION__": true, "__CLASS__": true,
	"__METHOD__": true, "__NAMESPACE__": true, "__DIR__": true, "__FILE__": true,
	"__TRAIT__": true,
}

// lineOf считает 1-based номер строки для байтовой позиции pos в src.
func lineOf(src string, pos int) int {
	if pos > len(src) {
		pos = len(src)
	}
	n := 1
	for i := 0; i < pos; i++ {
		if src[i] == '\n' {
			n++
		}
	}
	return n
}

// buildLineStarts возвращает отсортированные позиции начал строк (0, после
// каждого \n).
func buildLineStarts(src string) []int {
	starts := make([]int, 1, 64)
	starts[0] = 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineAt возвращает 1-based номер строки для позиции pos (бинарный поиск).
func (p *Parser) lineAt(pos int) int {
	if p.lineStarts == nil {
		p.lineStarts = buildLineStarts(p.src)
	}
	lo, hi := 0, len(p.lineStarts)
	for lo < hi {
		mid := (lo + hi) / 2
		if p.lineStarts[mid] <= pos {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// docCommentBefore возвращает doc-комментарий `/** ... */`, непосредственно
// предшествующий позиции pos (с учётом пробелов между ними).
func (p *Parser) docCommentBefore(pos int) string {
	i := pos
	for i > 0 {
		c := p.src[i-1]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			i--
			continue
		}
		break
	}
	if i < 2 || p.src[i-1] != '/' || p.src[i-2] != '*' {
		return ""
	}
	j := i - 2
	for j >= 1 {
		if p.src[j-1] == '/' && p.src[j] == '*' {
			break
		}
		j--
	}
	if j < 1 {
		return ""
	}
	start := j - 1
	if start+2 >= len(p.src) || p.src[start+1] != '*' || p.src[start+2] != '*' {
		return ""
	}
	return p.src[start:i]
}

// parseAttrs разбирает сырые тексты атрибутов `#[...]` (без обрамления) в
// список AttrDecl. Поддерживаются группы `#[A, B(...)]`, именованные
// аргументы и произвольные константные выражения аргументов.
func (p *Parser) parseAttrs(raws []AttrRaw) []AttrDecl {
	var out []AttrDecl
	for _, raw := range raws {
		for _, item := range splitAttrTop(raw.Text, ',') {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			// Строка атрибута — его собственный `#[`, а не следующего
			// токена (иначе newInstance() кадр указывает на `class`).
			ad := AttrDecl{Line: raw.Line}
			if paren := strings.IndexByte(item, '('); paren >= 0 {
				name := strings.TrimSpace(item[:paren])
				if strings.HasPrefix(name, "$") {
					p.attrErr = &parseError{msg: "syntax error, unexpected variable " + strconv.Quote(name), line: raw.Line}
					return out
				}
				ad.Name = p.resolveClassName(name)
				args := strings.TrimSpace(item[paren+1:])
				if strings.HasSuffix(args, ")") {
					args = strings.TrimSpace(args[:len(args)-1])
				}
				namedSeen := false
				seenNames := map[string]bool{}
				for _, a := range splitAttrTop(args, ',') {
					a = strings.TrimSpace(a)
					if a == "" {
						continue
					}
					arg := AttrArg{}
					if col := attrNamedColon(a); col >= 0 {
						arg.Name = strings.TrimSpace(a[:col])
						a = strings.TrimSpace(a[col+1:])
					}
					if arg.Name != "" {
						if seenNames[arg.Name] {
							p.attrErr = &fatalError{msg: "Duplicate named parameter $" + arg.Name, line: p.lineAt(p.cur().Pos)}
							return out
						}
						seenNames[arg.Name] = true
						namedSeen = true
					} else if namedSeen {
						p.attrErr = &fatalError{msg: "Cannot use positional argument after named argument", line: p.lineAt(p.cur().Pos)}
						return out
					}
					if strings.HasPrefix(a, "...") {
						if strings.TrimSpace(a[3:]) == "" {
							// `F(...)` — first-class callable в атрибуте.
							// Zend сообщает строку объявления, к которому
							// прикреплён атрибут.
							line := raw.Line
							if p.pos < len(p.toks) {
								line = p.lineAt(p.cur().Pos)
							}
							p.attrErr = &fatalError{msg: "Cannot create Closure as attribute argument", line: line}
						} else {
							line := raw.Line
							if p.pos < len(p.toks) {
								line = p.lineAt(p.cur().Pos)
							}
							p.attrErr = &fatalError{msg: "Cannot use unpacking in attribute argument list", line: line}
						}
						return out
					}
					if e, err := p.parseAttrExpr(a); err == nil {
						if !attrExprAllowed(e) {
							p.attrErr = &fatalError{msg: "Constant expression contains invalid operations", line: raw.Line}
							return out
						}
						if attrHasDynamicClassRef(e) {
							p.attrErr = &fatalError{msg: "Dynamic class names are not allowed in compile-time class constant references", line: raw.Line}
							return out
						}
						arg.Val = e
					}
					ad.Args = append(ad.Args, arg)
				}
			} else {
				if strings.HasPrefix(item, "$") {
					p.attrErr = &parseError{msg: "syntax error, unexpected variable " + strconv.Quote(item), line: raw.Line}
					return out
				}
				ad.Name = p.resolveClassName(item)
			}
			out = append(out, ad)
		}
	}
	return out
}

// attrExprAllowed сообщает, что выражение допустимо в аргументе атрибута:
// константное выражение или `new` (PHP 8.3 «new in initializers»).
func attrExprAllowed(e Expr) bool {
	if isConstExpr(e) {
		return true
	}
	switch x := e.(type) {
	case *NewExpr:
		for _, a := range x.Args {
			if !attrExprAllowed(a) {
				return false
			}
		}
		return true
	case *NewAnonClassExpr:
		return true
	}
	return false
}

// attrHasDynamicClassRef сообщает, что константное выражение атрибута содержит
// класс, заданный выражением (EXPR::CONST/::class) — Zend запрещает такие
// ссылки в compile-time контексте.
func attrHasDynamicClassRef(e Expr) bool {
	switch x := e.(type) {
	case *ConstExpr:
		return x.ClassExpr != nil || x.NameExpr != nil
	case *ClassKeywordExpr:
		return x.ClassExpr != nil
	case *StaticPropExpr:
		return x.ClassExpr != nil || x.NameExpr != nil
	case *StaticCallExpr:
		return x.ClassExpr != nil || x.NameExpr != nil
	case *UnaryExpr:
		return attrHasDynamicClassRef(x.X)
	case *BinExpr:
		return attrHasDynamicClassRef(x.L) || attrHasDynamicClassRef(x.R)
	case *LogicalExpr:
		return attrHasDynamicClassRef(x.L) || attrHasDynamicClassRef(x.R)
	case *TernaryExpr:
		if x.Cond != nil && attrHasDynamicClassRef(x.Cond) {
			return true
		}
		return attrHasDynamicClassRef(x.Then) || attrHasDynamicClassRef(x.Else)
	case *CoalesceExpr:
		return attrHasDynamicClassRef(x.L) || attrHasDynamicClassRef(x.R)
	case *ArrayLit:
		for _, el := range x.Elems {
			if attrHasDynamicClassRef(el.Val) || (el.Key != nil && attrHasDynamicClassRef(el.Key)) {
				return true
			}
		}
	case *IndexExpr:
		return attrHasDynamicClassRef(x.Arr) || (x.Key != nil && attrHasDynamicClassRef(x.Key))
	case *CastExpr:
		return attrHasDynamicClassRef(x.X)
	}
	return false
}

// splitAttrTop делит строку по sep на верхнем уровне (вне (), [], "").
func splitAttrTop(s string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '"', '\'':
			q := s[i]
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// attrNamedColon возвращает индекс одиночного ':' (named argument), не
// являющегося частью '::'.
func attrNamedColon(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				return -1
			}
			if i > 0 && s[i-1] == ':' {
				return -1
			}
			return i
		}
		if s[i] == '(' || s[i] == '[' {
			return -1
		}
	}
	return -1
}

// interp строит выражение-конкатенацию из частей интерполированной строки.
func (p *Parser) interp(parts []InterpPart) (Expr, error) {
	var e Expr
	onlyExpr := false
	for _, part := range parts {
		var cur Expr
		if part.IsExpr {
			x, err := ParseExpr(part.Text)
			if err != nil {
				return nil, err
			}
			cur = x
			if part.Heredoc {
				// В heredoc диагностики (undefined variable и т.п.) подавляются,
				// как при сравнении с `php -r` (там они уходят в stderr).
				cur = &UnaryExpr{Op: "@", X: cur}
			}
			if part.DollarBrace {
				// `${var}`/`${expr}` — устаревшая форма: предупреждение
				// выводится при вычислении, до подстановки значения.
				name := "__php_dollar_brace_deprecation"
				if part.DollarBraceExpr {
					name = "__php_dollar_brace_expr_deprecation"
				}
				cur = &BinExpr{Op: ".", L: &CallExpr{Name: name}, R: cur}
			}
		} else {
			if part.Text == "" {
				continue
			}
			cur = &StrLit{V: part.Text}
		}
		if e == nil {
			e = cur
			onlyExpr = part.IsExpr
		} else {
			onlyExpr = false
			e = &BinExpr{Op: ".", L: e, R: cur}
		}
	}
	if e == nil {
		return &StrLit{V: ""}, nil
	}
	// Единственная интерполированная часть без литералов должна дать строку
	// (`"$x"` → string), а не исходное значение переменной.
	if onlyExpr {
		e = &CastExpr{Type: "string", X: e}
	}
	return e, nil
}

func (p *Parser) cur() Token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return p.toks[len(p.toks)-1]
}

// moveAttrsToNext присоединяет атрибуты к следующему токену (для
// `#[A] static function` — атрибут прикреплён к `static`, а читается с
// `function`/`fn`).
func (p *Parser) moveAttrsToNext(attrs []AttrRaw) {
	if len(attrs) == 0 || p.pos >= len(p.toks) {
		return
	}
	p.toks[p.pos].Attrs = append(attrs, p.toks[p.pos].Attrs...)
}

// takeAttrs возвращает атрибуты текущего токена и очищает их у токена, чтобы
// последующий разбор типа не принял уже разобранные атрибуты за атрибуты типа
// (`#[A] int $x` — атрибут параметра, а не типа).
func (p *Parser) takeAttrs() []AttrRaw {
	if p.pos < len(p.toks) {
		a := p.toks[p.pos].Attrs
		p.toks[p.pos].Attrs = nil
		return a
	}
	return nil
}

func (p *Parser) peekAt(n int) Token {
	i := p.pos + n
	if i < len(p.toks) {
		return p.toks[i]
	}
	return p.toks[len(p.toks)-1]
}

func (p *Parser) next() Token {
	t := p.cur()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *Parser) is(k Kind, val string) bool {
	t := p.cur()
	return t.Kind == k && (val == "" || t.Val == val)
}

func (p *Parser) kw(val string) bool { return p.is(TKeyword, val) }

func (p *Parser) accept(k Kind, val string) bool {
	if p.is(k, val) {
		p.next()
		return true
	}
	return false
}

func (p *Parser) expect(k Kind, val string) error {
	if !p.accept(k, val) {
		return p.syntaxErrExpect(p.cur(), expectName(k, val))
	}
	return nil
}

// expectName — описание ожидаемого токена для сообщения "expecting ...".
func expectName(k Kind, val string) string {
	switch k {
	case TVar:
		return "variable"
	case TEOF:
		return "end of file"
	}
	return fmt.Sprintf("%q", val)
}

// tokenDesc возвращает PHP-описание неожиданного токена (часть сообщения
// после "unexpected ").
func tokenDesc(t Token) string {
	// Атрибут `#[...]` прикрепляется лексером к следующему токену; PHP
	// сообщает о нём как об отдельном токене "#[".
	if len(t.Attrs) > 0 {
		return `token "#["`
	}
	switch t.Kind {
	case TEOF:
		return "end of file"
	case TVar:
		return fmt.Sprintf("variable %q", t.Val)
	case TInt:
		return fmt.Sprintf("integer %q", t.Val)
	case TFloat:
		return fmt.Sprintf("float %q", t.Val)
	case TStr:
		if strings.HasPrefix(t.Val, "'") {
			return "single-quoted string"
		}
		return "double-quoted string"
	case TIdent:
		return fmt.Sprintf("identifier %q", t.Val)
	default:
		return fmt.Sprintf("token %q", t.Val)
	}
}

// atBracketOrEOF сообщает, что токен — конец файла или закрывающая скобка.
func atBracketOrEOF(t Token) bool {
	if t.Kind == TEOF {
		return true
	}
	return t.Kind == TOp && (t.Val == ")" || t.Val == "]" || t.Val == "}")
}

// syntaxErr строит PHP-совместимую ошибку "unexpected <tok>" (без ожидания).
func (p *Parser) syntaxErr(t Token) error {
	return &parseError{msg: "syntax error, unexpected " + tokenDesc(t), line: p.lineAt(t.Pos), bracketOrEOF: atBracketOrEOF(t)}
}

// syntaxErrExpect строит "unexpected <tok>, expecting <what>".
func (p *Parser) syntaxErrExpect(t Token, what string) error {
	return &parseError{msg: "syntax error, unexpected " + tokenDesc(t) + ", expecting " + what, line: p.lineAt(t.Pos), bracketOrEOF: atBracketOrEOF(t)}
}

func (p *Parser) program() (*Program, error) {
	prog := &Program{}
	for !p.is(TEOF, "") {
		if p.kw("namespace") && !p.nsRelativeStmtAhead() {
			stmts, err := p.namespaceDecl()
			if err != nil {
				return nil, err
			}
			prog.Stmts = append(prog.Stmts, stmts...)
			p.anyStmt = true
			if p.nsMode == 1 {
				p.nsBlockClosed = true
			}
			continue
		}
		startLine := p.lineAt(p.cur().Pos)
		st, err := p.statement()
		if err != nil {
			return nil, err
		}
		if p.nsMode == 1 && p.nsBlockClosed && !p.declared && st != nil {
			// Файл с bracketed-namespace: код допускается только внутри `{}`.
			return nil, &fatalError{msg: "No code may exist outside of namespace {}", line: startLine}
		}
		if st != nil {
			prog.Stmts = append(prog.Stmts, st)
		}
		if st != nil && !p.declared {
			// Пустой оператор `;` — nop: он не мешает объявлению namespace.
			p.anyStmt = true
		}
		p.declared = false
	}
	prog.Lines = p.stmtLines
	prog.ExprLines = p.exprLines
	prog.StrictTypes = p.strictTypes
	prog.TypeWarnings = p.typeWarnings
	if err := p.checkUseClassConflicts(); err != nil {
		return nil, err
	}
	return prog, nil
}

// checkUseClassConflicts повторяет проверку Zend «the name is already in use»
// для импортов классов и объявлений классов в одном файле: ошибка сообщается
// на том из двух мест, которое идёт позже.
func (p *Parser) checkUseClassConflicts() error {
	for _, u := range p.pendingClassUses {
		cline, ok := p.seenClassLine[u.key]
		if !ok {
			continue
		}
		// Импорт самого объявленного класса (совпадающие FQ-имена) — не
		// конфликт: `namespace Test; use Test\Foo; class Foo {}` корректен.
		if strings.EqualFold(strings.TrimLeft(u.full, "\\"), strings.TrimLeft(p.seenClassName[u.key], "\\")) {
			continue
		}
		if u.line > cline {
			return &fatalError{msg: fmt.Sprintf("Cannot use %s as %s because the name is already in use", u.full, u.alias), line: u.line}
		}
		return &fatalError{msg: fmt.Sprintf("Cannot declare class %s because the name is already in use", p.seenClassName[u.key]), line: cline}
	}
	return nil
}

// nsRelativeStmtAhead сообщает, что `namespace` в начале оператора — не
// объявление, а namespace-relative имя `namespace\Foo` (лексер Zend отдаёт
// T_NAME_RELATIVE, когда `\` идёт сразу за ключевым словом).
func (p *Parser) nsRelativeStmtAhead() bool {
	t := p.cur()
	n := p.peekAt(1)
	return n.Kind == TOp && n.Val == "\\" && n.Pos == t.Pos+len("namespace")
}

// nsBracketedForm сообщает, что текущее объявление namespace — bracketed
// (`namespace X { ... }`), а не `namespace X;`. Просматривает токены имени.
func (p *Parser) nsBracketedForm() bool {
	for i := 1; i < 64; i++ {
		t := p.peekAt(i)
		if t.Kind == TOp && t.Val == "{" {
			return true
		}
		if t.Kind == TEOF || (t.Kind == TOp && t.Val == ";") {
			return false
		}
	}
	return false
}

// namespaceDecl разбирает `namespace Name;` или `namespace Name { ... }`.
// Для блочной формы возвращает операторы тела (они вливаются в программу —
// объявления должны hoist'иться), для точечной — nil.
func (p *Parser) namespaceDecl() ([]Stmt, error) {
	nsLine := p.lineAt(p.cur().Pos)
	bracketed := p.nsBracketedForm()
	p.next() // namespace
	// Правила размещения объявлений namespace (Zend, compile-time fatal):
	// вложенным считается только bracketed-объявление внутри блока;
	// unbracketed внутри блока — смешение форм.
	if p.nsDepth > 0 && bracketed {
		return nil, &fatalError{msg: "Namespace declarations cannot be nested", line: nsLine}
	}
	first := p.nsMode == 0
	if bracketed {
		if p.nsMode == 2 {
			return nil, &fatalError{msg: "Cannot mix bracketed namespace declarations with unbracketed namespace declarations", line: nsLine}
		}
		p.nsMode = 1
	} else {
		if p.nsMode == 1 {
			return nil, &fatalError{msg: "Cannot mix bracketed namespace declarations with unbracketed namespace declarations", line: nsLine}
		}
		p.nsMode = 2
	}
	if first && p.anyStmt {
		return nil, &fatalError{msg: "Namespace declaration statement has to be the very first statement or after any declare call in the script", line: nsLine}
	}
	name := ""
	if !p.is(TOp, "{") {
		if p.is(TKeyword, "namespace") && p.peekAt(1).Kind == TOp && p.peekAt(1).Val == "\\" {
			// `namespace NAMEspace\xyz;`: имя лексер Zend отдаёт единым
			// токеном T_NAME_RELATIVE, недопустимым в объявлении namespace.
			pos := p.cur().Pos
			raw := p.rawNameFromHere()
			return nil, &parseError{
				msg:  fmt.Sprintf("syntax error, unexpected namespace-relative name \"%s\", expecting \"{\"", raw),
				line: p.lineAt(pos),
			}
		}
		if p.is(TKeyword, "namespace") {
			// `namespace namespace;` / `namespace Namespace {}` — компиляторная
			// ошибка Zend (имя не может называться namespace).
			return nil, &fatalError{
				msg:  fmt.Sprintf("Cannot use '%s' as namespace name", tokName(p.cur())),
				line: p.lineAt(p.cur().Pos),
			}
		}
		if p.is(TOp, "\\") {
			// Ведущий `\` в имени объявления запрещён (namespace_declaration_name).
			pos := p.cur().Pos
			raw := p.rawNameFromHere()
			return nil, &parseError{
				msg:  fmt.Sprintf("syntax error, unexpected fully qualified name \"%s\", expecting \"{\"", raw),
				line: p.lineAt(pos),
			}
		}
		if !isNameTok(p.cur()) {
			return nil, p.syntaxErrExpect(p.cur(), `"{"`)
		}
		name = strings.Trim(p.rawQualifiedName(), "\\")
	}
	if p.is(TOp, "{") {
		p.next() // {
		savedNS := p.ns
		savedCU, savedFU, savedConU := p.classUses, p.funcUses, p.constUses
		savedCI := p.constImports
		p.ns = name
		p.classUses, p.funcUses, p.constUses = nil, nil, nil
		p.constImports = nil
		p.nsDepth++
		body, err := p.stmtsUntil("}")
		p.nsDepth--
		p.ns = savedNS
		p.classUses, p.funcUses, p.constUses = savedCU, savedFU, savedConU
		p.constImports = savedCI
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	if err := p.endStmt(); err != nil {
		return nil, err
	}
	p.ns = name
	p.classUses, p.funcUses, p.constUses = nil, nil, nil
	p.constImports = nil
	return nil, nil
}

// block ожидает `{ ... }`.
func (p *Parser) block() ([]Stmt, error) {
	if err := p.expect(TOp, "{"); err != nil {
		return nil, err
	}
	return p.stmtsUntil("}")
}

func (p *Parser) stmtsUntil(end string) ([]Stmt, error) {
	var out []Stmt
	for !p.is(TOp, end) {
		if p.is(TEOF, "") {
			return nil, fmt.Errorf("parser: незакрытый блок")
		}
		st, err := p.statement()
		if err != nil {
			return nil, err
		}
		if st != nil {
			out = append(out, st)
		}
	}
	p.next()
	return out, nil
}

// statementOrBlock — одиночный оператор или блок.
func (p *Parser) statementOrBlock() ([]Stmt, error) {
	if p.is(TOp, "{") {
		return p.block()
	}
	st, err := p.statement()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, nil
	}
	return []Stmt{st}, nil
}

// thenBlock: `{...}` | `: ... endKw ;` | одиночный оператор.
func (p *Parser) thenBlock(endKw string) ([]Stmt, error) {
	if p.is(TOp, "{") {
		return p.block()
	}
	if p.accept(TOp, ":") {
		var out []Stmt
		for !p.kw(endKw) && !p.kw("else") && !p.kw("elseif") {
			if p.is(TEOF, "") {
				return nil, fmt.Errorf("parser: незакрытый блок (ожидалось %s)", endKw)
			}
			st, err := p.statement()
			if err != nil {
				return nil, err
			}
			if st != nil {
				out = append(out, st)
			}
		}
		if p.kw(endKw) {
			p.next() // endKw
			p.accept(TOp, ";")
		}
		return out, nil
	}
	st, err := p.statement()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, nil
	}
	return []Stmt{st}, nil
}

// statement — обёртка над statementInner: запоминает строку начального токена
// оператора (для диагностик VM).
func (p *Parser) statement() (Stmt, error) {
	start := p.cur().Pos
	st, err := p.statementInner()
	if err == nil && st != nil {
		if p.stmtLines == nil {
			p.stmtLines = map[Stmt]int{}
		}
		if _, ok := p.stmtLines[st]; !ok {
			p.stmtLines[st] = p.lineAt(start)
		}
	}
	return st, err
}

func (p *Parser) statementInner() (Stmt, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	switch {
	case p.accept(TOp, ";"):
		return nil, nil
	case p.cur().Kind == TIdent && p.peekAt(1).Kind == TOp && p.peekAt(1).Val == ":":
		// Метка goto: `label:`
		name := p.next().Val
		ln := p.lineAt(p.cur().Pos)
		p.next() // :
		return &LabelStmt{Name: name, Line: ln}, nil
	case p.is(TIdent, "goto") && p.peekAt(1).Kind == TIdent:
		p.next() // goto
		lblTok := p.next()
		lbl := lblTok.Val
		ln := p.lineAt(lblTok.Pos)
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return &GotoStmt{Label: lbl, Line: ln}, nil
	case p.is(TInlineHTML, ""):
		// Литеральный HTML вне PHP-блока — выводится как есть.
		t := p.next()
		return &EchoStmt{Args: []Expr{&StrLit{V: t.Val}}}, nil
	case p.kw("echo"):
		p.next()
		var args []Expr
		for {
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			args = append(args, x)
			if !p.accept(TOp, ",") {
				break
			}
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return &EchoStmt{Args: args}, nil
	case p.kw("if"):
		return p.ifStmt()
	case p.kw("declare"):
		return p.declareStmt()
	case p.kw("while"):
		p.next()
		cond, err := p.paren()
		if err != nil {
			return nil, err
		}
		body, err := p.thenBlock("endwhile")
		if err != nil {
			return nil, err
		}
		return &WhileStmt{Cond: cond, Body: body}, nil
	case p.kw("do"):
		p.next()
		body, err := p.statementOrBlock()
		if err != nil {
			return nil, err
		}
		if !p.kw("while") {
			return nil, fmt.Errorf("parser: do без while")
		}
		p.next()
		cond, err := p.paren()
		if err != nil {
			return nil, err
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return &DoWhileStmt{Body: body, Cond: cond}, nil
	case p.kw("for"):
		return p.forStmt()
	case p.kw("foreach"):
		return p.foreachStmt()
	case p.kw("switch"):
		return p.switchStmt()
	case p.kw("return"):
		p.next()
		var x Expr
		var err error
		if !p.is(TOp, ";") {
			x, err = p.expr()
			if err != nil {
				return nil, err
			}
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return &ReturnStmt{X: x}, nil
	case p.kw("break"), p.kw("continue"):
		isBreak := p.kw("break")
		p.next()
		level := 1
		// opKind: 0 — операнда нет или целочисленный литерал; 1 — литерал
		// double/string; 2 — не литерал (переменная, выражение, true/null/…).
		// Zend проверяет операнд на этапе компиляции (PHP 7/8).
		opKind := 0
		if p.is(TInt, "") {
			level = int(p.next().Num)
		} else if !p.is(TOp, ";") {
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			switch x.(type) {
			case *FloatLit, *StrLit:
				opKind = 1
			default:
				opKind = 2
			}
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		if isBreak {
			return &BreakStmt{Level: level, OpKind: opKind}, nil
		}
		return &ContinueStmt{Level: level, OpKind: opKind}, nil
	case p.kw("function"):
		// function name(...) — именованная функция (не замыкание);
		// `function &name()` — возврат по ссылке.
		if p.peekAt(1).Kind == TIdent || p.peekAt(1).Kind == TKeyword || p.peekAt(1).Val == "&" {
			return p.funcDecl()
		}
	case p.kw("unset"):
		p.next()
		if err := p.expect(TOp, "("); err != nil {
			return nil, err
		}
		var args []Expr
		for !p.is(TOp, ")") {
			argLine := p.lineAt(p.cur().Pos)
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			if isThisExpr(x) {
				return nil, &fatalError{msg: "Cannot unset $this", line: argLine}
			}
			args = append(args, x)
			if !p.accept(TOp, ",") {
				break
			}
		}
		if err := p.expect(TOp, ")"); err != nil {
			return nil, err
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return &UnsetStmt{Args: args}, nil
	case p.kw("throw"):
		p.next()
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return &ThrowStmt{X: x}, nil
	case p.kw("global"):
		p.next()
		var names []string
		var dyn []Expr
		for {
			t := p.cur()
			if t.Kind == TVar && t.Val == "$" {
				// `global $$x` / `global ${expr}`: имя вычисляется в рантайме.
				p.next()
				e, err := p.dynAfterDollar()
				if err != nil {
					return nil, err
				}
				dyn = append(dyn, e)
			} else {
				t = p.next()
				if t.Val == "$this" {
					return nil, &fatalError{msg: "Cannot use $this as global variable", line: p.lineAt(t.Pos)}
				}
				names = append(names, t.Val)
			}
			if !p.accept(TOp, ",") {
				// Zend: после имени в global ожидается "," или ";".
				if !p.accept(TOp, ";") && !p.is(TInlineHTML, "") {
					return nil, p.syntaxErrExpect(p.cur(), `"," or ";"`)
				}
				break
			}
		}
		return &GlobalStmt{Names: names, Dyn: dyn}, nil
	case p.kw("static"):
		// static $x = ...;  (статические переменные функции)
		if p.peekAt(1).Kind == TVar {
			p.next()
			st := &StaticVarStmt{}
			for {
				tok := p.next()
				if tok.Val == "$this" {
					return nil, &fatalError{msg: "Cannot use $this as static variable", line: p.lineAt(tok.Pos)}
				}
				name := tok.Val
				st.Names = append(st.Names, name)
				if p.accept(TOp, "=") {
					x, err := p.expr()
					if err != nil {
						return nil, err
					}
					st.Default = append(st.Default, x)
				} else {
					st.Default = append(st.Default, nil)
				}
				if !p.accept(TOp, ",") {
					break
				}
			}
			if err := p.endStmt(); err != nil {
				return nil, err
			}
			return st, nil
		}
	case p.kw("try"):
		return p.tryStmt()
	case p.kw("const"):
		// const NAME = expr; — верхнеуровневая константа. Регистрируем её
		// рантайм-вызовом define() (значение может быть non-literal, напр.
		// `new`).
		p.next()
		var defs []Stmt
		for {
			namePos := p.cur().Pos
			// Имя константы верхнего уровня — идентификатор; ключевое слово
			// здесь Parse error (в классе const-имена могут быть любыми).
			// true/false/null PHP лексит как T_STRING — они допустимы как
			// имена (ошибка «Cannot redeclare constant» выдаётся позже).
			if t := p.cur(); t.Kind == TKeyword && t.Val != "true" && t.Val != "false" && t.Val != "null" {
				return nil, p.syntaxErrExpect(t, "identifier")
			}
			name := tokName(p.next())
			if err := p.expect(TOp, "="); err != nil {
				return nil, err
			}
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			// PHP регистрирует `const NAME` в текущем пространстве имён.
			// true/false/null — предопределённые константы, их переобъявление
			// запрещено (Zend: "Cannot redeclare constant 'NULL'").
			switch strings.ToLower(name) {
			case "true", "false", "null":
				return nil, &fatalError{msg: fmt.Sprintf("Cannot redeclare constant '%s'", name), line: p.lineAt(namePos)}
			}
			if tgt, ok := p.constImports[name]; ok && tgt != p.qualifyDecl(name) {
				return nil, &fatalError{msg: fmt.Sprintf("Cannot declare const %s because the name is already in use", p.qualifyDecl(name)), line: p.lineAt(namePos)}
			}
			if p.seenConsts == nil {
				p.seenConsts = map[string]bool{}
			}
			seenNames := append([]string(nil), name)
			for _, n := range seenNames {
				p.seenConsts[p.qualifyDecl(n)] = true
			}
			name = p.qualifyDecl(name)
			defs = append(defs, &ExprStmt{X: &CallExpr{
				Name:      "define",
				Args:      []Expr{&StrLit{V: name}, v},
				ConstStmt: true,
			}})
			if !p.accept(TOp, ",") {
				break
			}
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		if len(defs) == 1 {
			return defs[0], nil
		}
		return &BlockStmt{Body: defs}, nil
	case p.kw("class"), p.kw("interface"), p.kw("trait"),
		p.kw("abstract"), p.kw("final"):
		return p.classDecl()
	case p.is(TIdent, "readonly") && p.classStartAhead():
		return p.classDecl()
	case p.is(TIdent, "enum") && (p.peekAt(1).Kind == TIdent || p.peekAt(1).Kind == TKeyword):
		return p.classDecl()
	case p.kw("namespace"):
		// namespace внутри блока недопустим; разбираем как no-op-обёртку,
		// чтобы не падать на нестандартном входе.
		if _, err := p.namespaceDecl(); err != nil {
			return nil, err
		}
		return nil, nil
	case p.kw("use"):
		return p.useStmt()
	case p.kw("public"), p.kw("protected"), p.kw("private"):
		// Пока не поддерживаем: пропускаем объявление до `;` или `{`.
		return p.skipDecl()
	}

	// `{ ... }` как составной оператор
	if p.is(TOp, "{") {
		body, err := p.block()
		if err != nil {
			return nil, err
		}
		return &BlockStmt{Body: body}, nil
	}

	// выражение/присваивание
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	// В PHP для оператора-выражения ожидание `;` не сообщается.
	if err := p.endStmtExpr(); err != nil {
		return nil, err
	}
	return &ExprStmt{X: x}, nil
}

// endStmtExpr завершает оператор-выражение: как endStmt, но при неожиданном
// токене не добавляет ", expecting \";\"" (совпадает с PHP).
func (p *Parser) endStmtExpr() error {
	if p.accept(TOp, ";") {
		return nil
	}
	// `?>` неявно завершает оператор, конец файла — нет (см. endStmt).
	if p.is(TInlineHTML, "") {
		return nil
	}
	return p.syntaxErr(p.cur())
}

func (p *Parser) endStmt() error {
	if p.accept(TOp, ";") {
		return nil
	}
	// `?>` неявно завершает оператор, а конец файла — нет: PHP требует `;`
	// у простых операторов (`<?php echo 1` — parse error).
	if p.is(TInlineHTML, "") {
		return nil
	}
	return p.expect(TOp, ";")
}

// hasDynamicGlobal ищет `global $$x` / `global ${expr}` в теле (не заходя в
// вложенные функции/замыкания/классы — у них своя область видимости).
func hasDynamicGlobal(list []Stmt) bool {
	for _, st := range list {
		switch s := st.(type) {
		case *GlobalStmt:
			if len(s.Dyn) > 0 {
				return true
			}
		case *BlockStmt:
			if hasDynamicGlobal(s.Body) {
				return true
			}
		case *IfStmt:
			if hasDynamicGlobal(s.Then) || hasDynamicGlobal(s.Else) {
				return true
			}
		case *WhileStmt:
			if hasDynamicGlobal(s.Body) {
				return true
			}
		case *DoWhileStmt:
			if hasDynamicGlobal(s.Body) {
				return true
			}
		case *ForStmt:
			if hasDynamicGlobal(s.Body) {
				return true
			}
		case *ForeachStmt:
			if hasDynamicGlobal(s.Body) {
				return true
			}
		case *SwitchStmt:
			for _, cs := range s.Cases {
				if hasDynamicGlobal(cs.Body) {
					return true
				}
			}
		case *TryStmt:
			if hasDynamicGlobal(s.Body) || hasDynamicGlobal(s.Finally) {
				return true
			}
			for _, cc := range s.Catches {
				if hasDynamicGlobal(cc.Body) {
					return true
				}
			}
		}
	}
	return false
}

// useStmt разбирает `use`-импорт:
//
//	use Foo\Bar;
//	use Foo\Bar as Baz;
//	use function Foo\bar;
//	use const Foo\BAR;
//	use Foo\{A, B as C, function f, const X};
func (p *Parser) useStmt() (Stmt, error) {
	p.next() // use
	kind := "class"
	if p.kw("function") {
		kind = "function"
		p.next()
	} else if p.kw("const") {
		kind = "const"
		p.next()
	}
	for {
		line := p.lineAt(p.cur().Pos)
		raw := p.rawQualifiedName()
		// групповой импорт: `Prefix\{...}` (после имени остаётся `\{`)
		if p.is(TOp, "\\") && p.peekAt(1).Val == "{" {
			p.next() // \
		}
		if p.is(TOp, "{") {
			p.next() // {
			prefix := strings.Trim(raw, "\\")
			first := true
			// Ожидаемый набор для позиции элемента группы. У первого элемента
			// он полный; после запятой (из-за default-сокращений LALR) Zend
			// сообщает только `}`.
			groupWhat := func() string {
				if !first {
					return `"}"`
				}
				what := "identifier or namespaced name"
				if kind == "class" {
					what += ` or "function" or "const"`
				}
				return what
			}
			for {
				t := p.cur()
				// Полностью определённое имя (`\Baz`) в группе недопустимо.
				if p.is(TOp, "\\") && isNameTok(p.peekAt(1)) {
					rawName := p.rawNameFromHere()
					return nil, &parseError{
						msg:  fmt.Sprintf("syntax error, unexpected fully qualified name \"%s\", expecting %s", rawName, groupWhat()),
						line: p.lineAt(t.Pos),
					}
				}
				// Namespace-relative имя (`namespace\Baz`) в группе недопустимо.
				if p.is(TKeyword, "namespace") && p.peekAt(1).Kind == TOp && p.peekAt(1).Val == "\\" {
					rawName := p.rawNameFromHere()
					return nil, &parseError{
						msg:  fmt.Sprintf("syntax error, unexpected namespace-relative name \"%s\", expecting %s", rawName, groupWhat()),
						line: p.lineAt(t.Pos),
					}
				}
				// Хвостовая запятая: `Foo\{A,}`.
				if !first && p.is(TOp, "}") {
					break
				}
				gkind := kind
				if p.kw("function") || p.kw("const") {
					// В `use const/function Foo\{...}` ключевые слова у
					// элементов группы запрещены (unprefixed_use_declaration).
					if kind != "class" {
						return nil, p.syntaxErrExpect(t, groupWhat())
					}
					gkind = t.Val
					p.next()
					// После ключевого слова обязано идти имя.
					if p.cur().Kind != TIdent {
						return nil, p.syntaxErrExpect(p.cur(), "identifier or namespaced name")
					}
				} else if p.cur().Kind != TIdent {
					return nil, p.syntaxErrExpect(t, groupWhat())
				}
				item := p.rawQualifiedName()
				alias := p.useAlias(item)
				if err := p.checkUseAlias(gkind, item, alias); err != nil {
					return nil, err
				}
				full := prefix + "\\" + strings.Trim(item, "\\")
				if err := p.addUse(gkind, full, alias); err != nil {
					return nil, err
				}
				first = false
				if !p.accept(TOp, ",") {
					break
				}
			}
			if err := p.expect(TOp, "}"); err != nil {
				return nil, err
			}
		} else {
			hasAs := p.kw("as")
			alias := p.useAlias(raw)
			if err := p.checkUseAlias(kind, raw, alias); err != nil {
				return nil, err
			}
			trimmed := strings.Trim(raw, "\\")
			// В глобальном пространстве имён импорт несоставного имени без
			// `as` ни на что не влияет (Zend: E_WARNING).
			if p.ns == "" && !hasAs && !strings.Contains(trimmed, "\\") {
				p.typeWarnings = append(p.typeWarnings, TypeWarning{
					Line: line,
					Msg:  fmt.Sprintf("The use statement with non-compound name '%s' has no effect", trimmed),
				})
			}
			if err := p.addUse(kind, trimmed, alias); err != nil {
				return nil, err
			}
		}
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.endStmt(); err != nil {
		return nil, err
	}
	return nil, nil
}

// checkUseAlias проверяет, что псевдоним класса в use не является
// зарезервированным именем типа (fatal уровня Zend).
func (p *Parser) checkUseAlias(kind, full, alias string) error {
	if kind != "class" {
		return nil
	}
	short := alias
	if i := strings.LastIndex(short, "\\"); i >= 0 {
		short = short[i+1:]
	}
	if reservedTypeNames[strings.ToLower(short)] {
		line := p.lineAt(p.cur().Pos)
		return &fatalError{msg: fmt.Sprintf("Cannot use %s as %s because '%s' is a special class name",
			strings.Trim(full, "\\"), alias, alias), line: line}
	}
	return nil
}

// useAlias читает необязательный `as alias` и возвращает итоговый псевдоним.
func (p *Parser) useAlias(name string) string {
	if p.kw("as") {
		p.next()
		return strings.Trim(tokName(p.next()), "\\")
	}
	return lastSegment(name)
}

// addUse регистрирует импорт в соответствующей таблице псевдонимов.
func (p *Parser) addUse(kind, full, alias string) error {
	if full == "" || alias == "" {
		return nil
	}
	if kind == "const" {
		return p.addConstUse(full, alias)
	}
	key := strings.ToLower(alias)
	switch kind {
	case "function":
		if p.funcUses == nil {
			p.funcUses = map[string]string{}
		}
		dupFail := func() error {
			return &fatalError{msg: fmt.Sprintf("Cannot use function %s as %s because the name is already in use", full, alias), line: p.lineAt(p.cur().Pos)}
		}
		if _, ok := p.funcUses[key]; ok {
			// Zend не разрешает повторный импорт с тем же псевдонимом даже
			// для той же функции (`use function foo\bar; use function foo\bar;`).
			return dupFail()
		}
		if p.seenFuncs[strings.ToLower(p.qualifyDecl(alias))] {
			return dupFail()
		}
		p.funcUses[key] = full
	default:
		if p.classUses == nil {
			p.classUses = map[string]string{}
		}
		p.classUses[key] = full
		// Конфликт с классом, объявленным в этом же файле, проверяется после
		// разбора всего файла: класс может стоять и выше, и ниже импорта.
		p.pendingClassUses = append(p.pendingClassUses, pendingClassUse{
			full:  full,
			alias: alias,
			key:   strings.ToLower(p.qualifyDecl(alias)),
			line:  p.lineAt(p.cur().Pos),
		})
	}
	return nil
}

// addConstUse регистрирует `use const X as Y` и повторяет проверки Zend
// «the name is already in use» (имена констант регистрозависимы).
func (p *Parser) addConstUse(full, alias string) error {
	fail := func() error {
		line := p.lineAt(p.cur().Pos)
		return &fatalError{msg: fmt.Sprintf("Cannot use const %s as %s because the name is already in use", full, alias), line: line}
	}
	// Проверка против уже объявленных в этом файле const (FC(seen_symbols)).
	check := alias
	if p.ns != "" {
		check = strings.ToLower(p.ns) + "\\" + alias
	}
	if p.seenConsts[check] && !strings.EqualFold(full, check) {
		return fail()
	}
	// Повторный импорт того же псевдонима.
	if _, ok := p.constImports[alias]; ok {
		return fail()
	}
	if p.constImports == nil {
		p.constImports = map[string]string{}
	}
	p.constImports[alias] = full
	if p.constUses == nil {
		p.constUses = map[string]string{}
	}
	p.constUses[alias] = full
	if p.seenConsts == nil {
		p.seenConsts = map[string]bool{}
	}
	p.seenConsts[p.qualifyDecl(full)] = true
	return nil
}

func (p *Parser) skipDecl() (Stmt, error) {
	depth := 0
	for !p.is(TEOF, "") {
		if p.is(TOp, "{") {
			depth++
		} else if p.is(TOp, "}") {
			depth--
			p.next()
			if depth == 0 {
				return nil, nil
			}
			continue
		} else if p.is(TOp, ";") && depth == 0 {
			p.next()
			return nil, nil
		}
		p.next()
	}
	return nil, nil
}

// promotedProps синтезирует объявления свойств для promoted-параметров
// конструктора (public/protected/private/readonly).
func promotedProps(params []Param) []*ClassMember {
	var out []*ClassMember
	for _, pr := range params {
		if !pr.Promoted {
			continue
		}
		name := strings.TrimPrefix(pr.Name, "$")
		out = append(out, &ClassMember{Kind: "prop", Vis: pr.PromotedVis, PropName: name,
			PropType: pr.Type, Readonly: pr.PromotedReadonly,
			Attrs: pr.Attrs, PromotedAttrs: len(pr.Attrs) > 0})
	}
	return out
}

// promoteParams превращает promoted-параметры конструктора (public/private/
// protected) в присваивания свойств `$this->x = $x;` в начале тела.
func promoteConstructor(_ string, params []Param, body []Stmt) []Stmt {
	var pre []Stmt
	for _, pr := range params {
		if !pr.Promoted {
			continue
		}
		prop := strings.TrimPrefix(pr.Name, "$")
		pre = append(pre, &ExprStmt{X: &AssignExpr{
			Target: &PropExpr{Recv: &VarExpr{Name: "$this"}, Name: prop},
			Op:     "=",
			Value:  &VarExpr{Name: pr.Name},
		}})
	}
	if len(pre) == 0 {
		return body
	}
	return append(pre, body...)
}

// classStartAhead сообщает, что за текущим токеном `readonly` следует
// объявление класса (возможно, через другие модификаторы). Используется,
// чтобы отличать `readonly class` от использования `readonly` как имени.
func (p *Parser) classStartAhead() bool {
	for i := 1; i <= 3; i++ {
		t := p.peekAt(i)
		if t.Kind == TKeyword {
			switch t.Val {
			case "class", "interface", "trait", "abstract", "final":
				return true
			}
			return false
		}
		if t.Kind == TIdent {
			if t.Val == "readonly" {
				continue
			}
			if t.Val == "enum" {
				return true
			}
			return false
		}
		return false
	}
	return false
}

// classDecl разбирает class/interface/trait (возможно с abstract/final).
func (p *Parser) classDecl() (Stmt, error) {
	isAbstract := false
	isFinal := false
	finalCount := 0
	isReadonly := false
	readonlyCount := 0
	// `#[A] readonly class Foo`: атрибуты прикреплены к первому токену
	// (модификатору), а не к `class` — забираем их до разбора модификаторов.
	preAttrs := p.takeAttrs()
	for {
		switch {
		case p.kw("abstract"):
			isAbstract = true
			p.next()
		case p.kw("final"):
			isFinal = true
			finalCount++
			p.next()
		case p.is(TIdent, "readonly"):
			readonlyCount++
			isReadonly = true
			p.next()
		default:
			goto modsDone
		}
	}
modsDone:
	var kind string
	switch {
	case p.kw("class"):
		kind = "class"
	case p.kw("interface"):
		kind = "interface"
	case p.kw("trait"):
		kind = "trait"
	case p.is(TIdent, "enum"):
		kind = "enum"
	default:
		return nil, fmt.Errorf("parser: ожидалось объявление класса на позиции %d", p.cur().Pos)
	}
	// После модификатора (abstract/final/readonly) Zend допускает только
	// `class`: `abstract interface` и `readonly enum` — ошибка разбора.
	if kind != "class" && (isAbstract || isFinal || isReadonly) {
		return nil, &parseError{
			msg:  fmt.Sprintf("syntax error, unexpected token %q, expecting \"abstract\" or \"final\" or \"readonly\" or \"class\"", p.cur().Val),
			line: p.lineAt(p.cur().Pos),
		}
	}
	classLine := p.lineAt(p.cur().Pos)
	classAttrs := p.parseAttrs(append(preAttrs, p.takeAttrs()...))
	p.next()

	cd := &ClassDecl{IsInterface: kind == "interface", IsTrait: kind == "trait", IsAbstract: isAbstract, IsFinal: isFinal,
		IsReadonly: isReadonly, ReadonlyDup: readonlyCount > 1, FinalDup: finalCount > 1, Line: classLine, Attrs: classAttrs}
	if kind == "enum" {
		cd.IsEnum = true
	}
	// Имя объявляемого класса не проходит через таблицу use-алиасов (Zend
	// резолвит только ссылки на классы, не объявления).
	rawDeclName := p.rawQualifiedName()
	if strings.HasPrefix(rawDeclName, "\\") {
		cd.Name = strings.TrimLeft(rawDeclName, "\\")
	} else {
		cd.Name = p.qualifyDecl(strings.TrimLeft(rawDeclName, "\\"))
	}
	if p.seenClassLine == nil {
		p.seenClassLine = map[string]int{}
		p.seenClassName = map[string]string{}
	}
	classKey := strings.ToLower(cd.Name)
	if _, dup := p.seenClassLine[classKey]; !dup {
		p.seenClassLine[classKey] = classLine
		p.seenClassName[classKey] = cd.Name
	}
	if kind == "enum" && p.accept(TOp, ":") {
		tb, terr := p.typeNameErr()
		if terr != nil {
			return nil, terr
		}
		cd.EnumBacking = tb
	}
	// Трейты не могут расширять класс/реализовывать интерфейс.
	if kind == "trait" && (p.kw("extends") || p.kw("implements")) {
		return nil, &parseError{
			msg:  fmt.Sprintf("syntax error, unexpected token %q, expecting \"{\"", p.cur().Val),
			line: p.lineAt(p.cur().Pos),
		}
	}
	if p.kw("extends") && p.kwAdjacentBackslash() {
		// `extends\A` — в Zend это единый T_NAME_QUALIFIED.
		return nil, p.errAdjacentNamespacedName("{")
	}
	if p.accept(TKeyword, "extends") {
		if cd.IsInterface {
			for {
				cd.Interfaces = append(cd.Interfaces, p.className())
				if !p.accept(TOp, ",") {
					break
				}
			}
		} else {
			cd.Parent = p.className()
		}
	}
	if p.kw("implements") && p.kwAdjacentBackslash() {
		// `implements\A` — в Zend это единый T_NAME_QUALIFIED.
		return nil, p.errAdjacentNamespacedName("{")
	}
	if p.accept(TKeyword, "implements") {
		for {
			cd.Interfaces = append(cd.Interfaces, p.className())
			if !p.accept(TOp, ",") {
				break
			}
		}
	}
	if err := p.expect(TOp, "{"); err != nil {
		return nil, err
	}
	for !p.is(TOp, "}") {
		if p.is(TEOF, "") {
			return nil, fmt.Errorf("parser: незакрытое тело класса %q", cd.Name)
		}
		if p.accept(TOp, ";") {
			continue
		}
		if p.kw("use") {
			if cd.UsesLine == 0 {
				cd.UsesLine = p.lineAt(p.cur().Pos)
			}
			p.next()
			for {
				cd.Uses = append(cd.Uses, p.className())
				if !p.accept(TOp, ",") {
					break
				}
			}
			if p.is(TOp, "{") {
				p.next()
				for !p.is(TOp, "}") && !p.is(TEOF, "") {
					if p.accept(TOp, ";") {
						continue
					}
					ta := &TraitAdapt{}
					first := p.className()
					if p.accept(TOp, "::") {
						ta.Trait = first
						ta.Method = tokName(p.next())
					} else {
						ta.Method = first
					}
					switch {
					case p.kw("insteadof") || p.is(TIdent, "insteadof"):
						p.next()
						for {
							ta.Insteadof = append(ta.Insteadof, p.className())
							if !p.accept(TOp, ",") {
								break
							}
						}
					case p.kw("as"):
						p.next()
						ta.Alias = true
						switch {
						case p.kw("public") || p.kw("protected") || p.kw("private"):
							ta.Vis = p.next().Val
						case p.kw("final"):
							ta.AliasFinal = true
							p.next()
						case p.kw("static"):
							ta.AliasStatic = true
							p.next()
						case p.kw("abstract"):
							ta.AliasAbstract = true
							p.next()
						case p.is(TIdent, "readonly"):
							// Zend: readonly-модификатор к методу неприменим
							// (в т.ч. в позиции имени псевдонима).
							return nil, &fatalError{msg: "Cannot use the readonly modifier on a method", line: p.lineAt(p.cur().Pos)}
						}
						if p.is(TIdent, "") || p.is(TKeyword, "") {
							ta.AliasName = tokName(p.next())
						}
					}
					cd.TraitAdapt = append(cd.TraitAdapt, ta)
					if err := p.endStmt(); err != nil {
						return nil, err
					}
				}
				if p.is(TOp, "}") {
					p.next()
				}
				p.accept(TOp, ";")
				continue
			}
			if err := p.endStmt(); err != nil {
				return nil, err
			}
			continue
		}
		ms, err := p.classMember()
		if err != nil {
			return nil, err
		}
		cd.Members = append(cd.Members, ms...)
	}
	p.next() // }
	return cd, nil
}

// classMember разбирает одного члена класса (свойство/константу/метод).
// constNameAhead сообщает, что текущий токен — имя константы в
// `const NAME = ...` (за ним сразу `=` или `,`), а не тип в `const TYPE NAME`.
func (p *Parser) constNameAhead() bool {
	n := p.peekAt(1)
	return n.Kind == TOp && (n.Val == "=" || n.Val == ",")
}

func (p *Parser) classMember() ([]*ClassMember, error) {
	m := &ClassMember{Line: p.lineAt(p.cur().Pos), Doc: p.docCommentBefore(p.cur().Pos), Attrs: p.parseAttrs(p.takeAttrs())}
	accessCount := 0
	for {
		switch {
		case p.kw("public"):
			accessCount++
			if accessCount > 1 && m.FirstDup == "" {
				m.FirstDup = "access"
			}
			m.Vis = "public"
			p.next()
		case p.kw("protected"):
			accessCount++
			if accessCount > 1 && m.FirstDup == "" {
				m.FirstDup = "access"
			}
			m.Vis = "protected"
			p.next()
		case p.kw("private"):
			accessCount++
			if accessCount > 1 && m.FirstDup == "" {
				m.FirstDup = "access"
			}
			m.Vis = "private"
			p.next()
		case p.kw("static"):
			if m.Static && m.FirstDup == "" {
				m.FirstDup = "static"
			}
			m.Static = true
			p.next()
		case p.kw("abstract"):
			if m.Abstract && m.FirstDup == "" {
				m.FirstDup = "abstract"
			}
			m.Abstract = true
			p.next()
		case p.kw("final"):
			if m.Final {
				m.FinalDup = true
				if m.FirstDup == "" {
					m.FirstDup = "final"
				}
			}
			m.Final = true
			p.next()
		case p.is(TIdent, "readonly") || p.kw("readonly"):
			if m.Readonly {
				m.ReadonlyDup = true
				if m.FirstDup == "" {
					m.FirstDup = "readonly"
				}
			}
			m.Readonly = true
			p.next()
		case p.is(TIdent, "var"):
			accessCount++
			if accessCount > 1 && m.FirstDup == "" {
				m.FirstDup = "access"
			}
			m.Vis = "public"
			p.next()
		default:
			goto modsDone
		}
	}
modsDone:
	switch {
	case p.kw("function"):
		p.next()
		m.Kind = "method"
		m.ByRefReturn = p.accept(TOp, "&")
		m.MName = p.next().Val
		params, err := p.paramList()
		if err != nil {
			return nil, err
		}
		m.Params = params
		// необязательный тип возврата: `: type`
		if p.accept(TOp, ":") {
			rt, rerr := p.typeNameErr()
			if rerr != nil {
				return nil, rerr
			}
			m.RetType = rt
		}
		if p.is(TOp, "{") {
			m.Ticks = p.ticks
			savedTicks := p.ticks
			body, err := p.block()
			p.ticks = savedTicks
			if err != nil {
				return nil, err
			}
			if !strings.EqualFold(m.MName, "__construct") {
				for _, pr := range m.Params {
					if pr.Promoted {
						return nil, &fatalError{msg: "Cannot declare promoted property outside a constructor", line: m.Line}
					}
				}
			}
			m.Body = promoteConstructor(m.MName, m.Params, body)
			props := promotedProps(m.Params)
			return append(props, m), nil
		}
		// Нет тела: сигнатура (метод интерфейса / `abstract function`).
		// Это контракт, а не реализация — не регистрируется как вызываемая.
		m.Kind = "signature"
		for _, pr := range m.Params {
			if pr.Promoted {
				return nil, &fatalError{msg: "Cannot declare promoted property in an abstract constructor", line: m.Line}
			}
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return []*ClassMember{m}, nil
	case p.kw("case"):
		p.next()
		cm := &ClassMember{Kind: "case", Readonly: m.Readonly, ReadonlyDup: m.ReadonlyDup, FirstDup: m.FirstDup, Line: m.Line, Doc: m.Doc, Attrs: m.Attrs}
		cm.ConstName = tokName(p.next())
		if p.accept(TOp, "=") {
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			cm.ConstVal = v
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return []*ClassMember{cm}, nil
	case p.kw("const"):
		p.next()
		constType := "" // типизированная константа: `const TYPE NAME = ...` (не `const NAME = ...`)
		if p.isTypeStart() && !p.constNameAhead() {
			ct, cerr := p.typeNameErr()
			if cerr != nil {
				return nil, cerr
			}
			constType = ct
		}
		var out []*ClassMember
		for {
			cm := &ClassMember{Kind: "const", Vis: m.Vis, Static: true, StaticKw: m.Static, Abstract: m.Abstract, Final: m.Final, ConstType: constType,
				Readonly: m.Readonly, ReadonlyDup: m.ReadonlyDup, FirstDup: m.FirstDup, Line: m.Line, Doc: m.Doc, Attrs: m.Attrs}
			cm.ConstName = tokName(p.next())
			if err := p.expect(TOp, "="); err != nil {
				return nil, err
			}
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			cm.ConstVal = v
			out = append(out, cm)
			if !p.accept(TOp, ",") {
				break
			}
		}
		if err := p.endStmt(); err != nil {
			return nil, err
		}
		return out, nil
	}

	// `?static` недопустим как тип свойства/параметра (только как тип
	// возврата). PHP сообщает об "static" как о неожиданном токене.
	if p.is(TOp, "?") && p.peekAt(1).Kind == TKeyword && p.peekAt(1).Val == "static" {
		return nil, p.syntaxErr(p.peekAt(1))
	}

	// свойство: [тип] $name [= expr][, ...]
	propType := ""
	if p.isTypeStart() {
		pt, perr := p.typeNameErr()
		if perr != nil {
			return nil, perr
		}
		propType = pt
	}
	var out []*ClassMember
	for {
		if !p.is(TVar, "") {
			if len(out) == 0 {
				break
			}
			return nil, p.syntaxErrExpect(p.cur(), "variable")
		}
		name := strings.TrimPrefix(p.next().Val, "$")
		pm := &ClassMember{Kind: "prop", Vis: m.Vis, Static: m.Static, PropName: name, PropType: propType,
			Readonly: m.Readonly, ReadonlyDup: m.ReadonlyDup, Final: m.Final, FinalDup: m.FinalDup,
			Abstract: m.Abstract, FirstDup: m.FirstDup, Line: m.Line, Doc: m.Doc, Attrs: m.Attrs}
		if p.accept(TOp, "=") {
			def, err := p.expr()
			if err != nil {
				return nil, err
			}
			pm.Default = def
		}
		out = append(out, pm)
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.endStmt(); err != nil {
		return nil, err
	}
	return out, nil
}

// declareStmt разбирает declare(directive=value, ...);  тело блока для
// блочной формы выполняется, директивы игнорируются.
func (p *Parser) declareStmt() (Stmt, error) {
	p.next() // declare
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	strict := false
	var encVal Expr
	encLine := 0
	ticksVal := -1
	for !p.is(TOp, ")") {
		if p.is(TEOF, "") {
			return nil, fmt.Errorf("parser: незакрытый declare")
		}
		if p.cur().Kind != TIdent && p.cur().Kind != TKeyword {
			return nil, fmt.Errorf("parser: declare: ожидалось имя директивы на позиции %d", p.cur().Pos)
		}
		name := p.cur().Val
		dline := p.lineAt(p.cur().Pos)
		p.next() // имя директивы
		if err := p.expect(TOp, "="); err != nil {
			return nil, err
		}
		if strings.EqualFold(name, "strict_types") {
			strict = true
		}
		val, err := p.expr()
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(name, "strict_types") {
			p.strictTypes = literalTruthy(val)
		}
		if strings.EqualFold(name, "encoding") {
			encVal = val
			encLine = dline
		}
		if strings.EqualFold(name, "ticks") {
			// Zend принимает только литеральное значение (AST_ZVAL);
			// выражения вида 1+1 или -1 — фатал на этапе компиляции.
			switch v := val.(type) {
			case *IntLit:
				if v.V > 0 {
					ticksVal = int(v.V)
				} else {
					ticksVal = 0
				}
			case *FloatLit, *StrLit, *BoolLit, *NullLit:
				ticksVal = 0
			default:
				return nil, &fatalError{msg: "declare(ticks) value must be a literal", line: dline}
			}
		}
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	if p.accept(TOp, ";") {
		p.declared = true
		if ticksVal >= 0 {
			// Файловая форма: директива действует для последующих операторов
			// той же лексической области (компилятор включает тики маркером;
			// p.ticks запоминает значение для объявлений функций/методов).
			p.ticks = ticksVal
			return &TicksStmt{N: ticksVal}, nil
		}
		if strict && p.anyStmt {
			return nil, &fatalError{msg: "strict_types declaration must be the very first statement in the script", line: p.lineAt(p.cur().Pos)}
		}
		if encVal != nil {
			// declare(encoding=...): только самая первая инструкция скрипта.
			if p.anyStmt {
				fe := &fatalError{msg: "Encoding declaration pragma must be the very first statement in the script", line: encLine}
				if v, ok := iniValues["zend.multibyte"]; !ok || iniBoolInt(v) == 0 {
					fe.warnMsg = "declare(encoding=...) ignored because Zend multibyte feature is turned off by settings"
					fe.warnLine = encLine
				}
				return nil, fe
			}
			lit := false
			switch encVal.(type) {
			case *IntLit, *FloatLit, *StrLit, *BoolLit, *NullLit:
				lit = true
			}
			return &ExprStmt{X: &CallExpr{Name: "__php_declare_encoding", Args: []Expr{
				encVal, &BoolLit{V: lit}, &IntLit{V: int64(encLine)},
			}}}, nil
		}
		return nil, nil
	}
	if p.is(TOp, "{") {
		if strict {
			if p.anyStmt {
				return nil, &fatalError{msg: "strict_types declaration must be the very first statement in the script", line: p.lineAt(p.cur().Pos)}
			}
			return nil, &fatalError{msg: "strict_types declaration must not use block mode", line: p.lineAt(p.cur().Pos)}
		}
		savedTicks := p.ticks
		if ticksVal >= 0 {
			p.ticks = ticksVal
		}
		body, err := p.block()
		p.ticks = savedTicks
		if err != nil {
			return nil, err
		}
		if ticksVal >= 0 {
			return &BlockStmt{Body: body, Ticks: ticksVal}, nil
		}
		return &BlockStmt{Body: body}, nil
	}
	if p.accept(TOp, ":") {
		if strict {
			if p.anyStmt {
				return nil, &fatalError{msg: "strict_types declaration must be the very first statement in the script", line: p.lineAt(p.cur().Pos)}
			}
			return nil, &fatalError{msg: "strict_types declaration must not use block mode", line: p.lineAt(p.cur().Pos)}
		}
		savedTicks := p.ticks
		if ticksVal >= 0 {
			p.ticks = ticksVal
		}
		var out []Stmt
		for !p.kw("enddeclare") {
			if p.is(TEOF, "") {
				return nil, fmt.Errorf("parser: незакрытый declare (ожидалось enddeclare)")
			}
			st, err := p.statement()
			if err != nil {
				p.ticks = savedTicks
				return nil, err
			}
			if st != nil {
				out = append(out, st)
			}
		}
		p.ticks = savedTicks
		p.next() // enddeclare
		p.accept(TOp, ";")
		if ticksVal >= 0 {
			return &BlockStmt{Body: out, Ticks: ticksVal}, nil
		}
		return &BlockStmt{Body: out}, nil
	}
	return nil, fmt.Errorf("parser: declare: ожидалось ;, { или :")
}

func (p *Parser) ifStmt() (Stmt, error) {
	p.next() // if
	cond, err := p.paren()
	if err != nil {
		return nil, err
	}
	then, err := p.thenBlock("endif")
	if err != nil {
		return nil, err
	}
	els, err := p.ifTail()
	if err != nil {
		return nil, err
	}
	return &IfStmt{Cond: cond, Then: then, Else: els}, nil
}

func (p *Parser) ifTail() ([]Stmt, error) {
	if p.accept(TKeyword, "elseif") {
		cond, err := p.paren()
		if err != nil {
			return nil, err
		}
		then, err := p.thenBlock("endif")
		if err != nil {
			return nil, err
		}
		els, err := p.ifTail()
		if err != nil {
			return nil, err
		}
		return []Stmt{&IfStmt{Cond: cond, Then: then, Else: els}}, nil
	}
	if p.accept(TKeyword, "else") {
		if p.kw("if") {
			st, err := p.ifStmt()
			if err != nil {
				return nil, err
			}
			return []Stmt{st}, nil
		}
		return p.thenBlock("endif")
	}
	return nil, nil
}

func (p *Parser) paren() (Expr, error) {
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	return x, nil
}

func (p *Parser) parenBody() (Expr, []Stmt, error) {
	cond, err := p.paren()
	if err != nil {
		return nil, nil, err
	}
	body, err := p.statementOrBlock()
	if err != nil {
		return nil, nil, err
	}
	return cond, body, nil
}

func (p *Parser) forStmt() (Stmt, error) {
	p.next()
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	init, err := p.exprList(";")
	if err != nil {
		return nil, err
	}
	if err := p.expect(TOp, ";"); err != nil {
		return nil, err
	}
	cond, err := p.exprList(";")
	if err != nil {
		return nil, err
	}
	if err := p.expect(TOp, ";"); err != nil {
		return nil, err
	}
	post, err := p.exprList(")")
	if err != nil {
		return nil, err
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	body, err := p.thenBlock("endfor")
	if err != nil {
		return nil, err
	}
	return &ForStmt{Init: init, Cond: cond, Post: post, Body: body}, nil
}

func (p *Parser) exprList(end string) ([]Expr, error) {
	var out []Expr
	if p.is(TOp, end) {
		return out, nil
	}
	for {
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		out = append(out, x)
		if !p.accept(TOp, ",") {
			break
		}
	}
	return out, nil
}

func (p *Parser) foreachStmt() (Stmt, error) {
	p.next()
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	if !p.accept(TKeyword, "as") && !p.accept(TIdent, "as") {
		return nil, fmt.Errorf("parser: foreach без as")
	}
	st := &ForeachStmt{Expr: x}
	// Цель — либо переменная (возможно с &), либо деструктуризация [..]/list(..).
	parseTarget := func() (string, Expr, error) {
		if p.is(TOp, "[") {
			e, err := p.arrayLit()
			if err != nil {
				return "", nil, err
			}
			return "", asListTarget(e), nil
		}
		if p.kw("list") {
			p.next()
			p.listTargetDepth++
			e, err := p.listExpr()
			p.listTargetDepth--
			if err != nil {
				return "", nil, err
			}
			return "", e, nil
		}
		e, err := p.expr()
		if err != nil {
			return "", nil, err
		}
		if v, ok := e.(*VarExpr); ok && v.NameExpr == nil {
			return v.Name, nil, nil
		}
		return "", e, nil
	}
	// первый элемент: [&]$v | [..]  либо  $k => [&]$v | [..]
	byref := false
	keyRef := false
	if p.accept(TOp, "&") {
		byref = true
		keyRef = true
	}
	name, target, err := parseTarget()
	if err != nil {
		return nil, err
	}
	if p.accept(TOp, "=>") {
		st.Key, st.KeyTarget = name, target
		st.KeyRef = keyRef
		if p.accept(TOp, "&") {
			byref = true
		}
		vn, vt, err := parseTarget()
		if err != nil {
			return nil, err
		}
		st.Val, st.ValTarget = vn, vt
	} else {
		st.Val, st.ValTarget = name, target
	}
	st.ByRef = byref
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	body, err := p.thenBlock("endforeach")
	if err != nil {
		return nil, err
	}
	st.Body = body
	return st, nil
}

func (p *Parser) switchStmt() (Stmt, error) {
	p.next()
	subj, err := p.paren()
	if err != nil {
		return nil, err
	}
	alt := false
	if p.accept(TOp, ":") {
		alt = true
	} else if err := p.expect(TOp, "{"); err != nil {
		return nil, err
	}
	sw := &SwitchStmt{Subject: subj}
	sawDefault := false
	for !p.isSwitchEnd(alt) {
		c := &SwitchCase{}
		if p.accept(TKeyword, "case") {
			ce, err := p.expr()
			if err != nil {
				return nil, err
			}
			c.Cond = ce
			if !p.accept(TOp, ":") {
				if err := p.expect(TOp, ";"); err != nil {
					return nil, err
				}
			}
		} else if p.accept(TKeyword, "default") {
			if sawDefault {
				// Zend: повторный default — компайл-фатал (034.phpt).
				return nil, &fatalError{
					msg:  "Switch statements may only contain one default clause",
					line: p.lineAt(p.cur().Pos),
				}
			}
			sawDefault = true
			c.Default = true
			if !p.accept(TOp, ":") {
				if err := p.expect(TOp, ";"); err != nil {
					return nil, err
				}
			}
		} else {
			return nil, fmt.Errorf("parser: switch: ожидалось case/default")
		}
		for !p.is(TKeyword, "case") && !p.is(TKeyword, "default") && !p.isSwitchEnd(alt) {
			st, err := p.statement()
			if err != nil {
				return nil, err
			}
			if st != nil {
				c.Body = append(c.Body, st)
			}
		}
		sw.Cases = append(sw.Cases, c)
	}
	if alt {
		p.next() // endswitch
		p.accept(TOp, ";")
	} else {
		p.next() // }
	}
	return sw, nil
}

func (p *Parser) isSwitchEnd(alt bool) bool {
	if alt {
		return p.kw("endswitch") || p.is(TEOF, "")
	}
	return p.is(TOp, "}")
}

func (p *Parser) tryStmt() (Stmt, error) {
	p.next()
	body, err := p.block()
	if err != nil {
		return nil, err
	}
	st := &TryStmt{Body: body}
	for p.kw("catch") {
		catchLine := p.lineAt(p.cur().Pos)
		p.next()
		if err := p.expect(TOp, "("); err != nil {
			return nil, err
		}
		cc := &CatchClause{Line: catchLine}
		for {
			cc.Classes = append(cc.Classes, p.className())
			if !p.accept(TOp, "|") {
				break
			}
		}
		if p.is(TVar, "") {
			if p.cur().Val == "$this" {
				return nil, &fatalError{msg: "Cannot re-assign $this", line: p.lineAt(p.cur().Pos)}
			}
			cc.Var = p.next().Val
		}
		if err := p.expect(TOp, ")"); err != nil {
			return nil, err
		}
		cb, err := p.block()
		if err != nil {
			return nil, err
		}
		cc.Body = cb
		st.Catches = append(st.Catches, cc)
	}
	if p.accept(TKeyword, "finally") {
		fb, err := p.block()
		if err != nil {
			return nil, err
		}
		st.Finally = fb
		st.HasFinally = true
	}
	return st, nil
}

func (p *Parser) funcDecl() (Stmt, error) {
	attrs := p.parseAttrs(p.takeAttrs())
	p.next() // function
	byref := p.accept(TOp, "&")
	// Имя функции верхнего уровня — только T_STRING: ключевое слово здесь
	// даёт Parse error (Zend: function_declaration_statement требует T_STRING;
	// сообщение LALR — «expecting "("»: после function парсер ждёт также
	// открывающую скобку анонимной функции).
	if t := p.cur(); t.Kind == TKeyword {
		return nil, p.syntaxErrExpect(t, `"("`)
	}
	name := p.qualifyDecl(tokName(p.next()))
	params, err := p.paramList()
	if err != nil {
		return nil, err
	}
	ret := ""
	if p.accept(TOp, ":") {
		rt, rerr := p.typeNameErr()
		if rerr != nil {
			return nil, rerr
		}
		ret = rt
	}
	declLine := p.lineAt(p.cur().Pos)
	declTicks := p.ticks
	savedTicks := p.ticks
	body, err := p.block()
	p.ticks = savedTicks
	if err != nil {
		return nil, err
	}
	for _, pr := range params {
		if pr.Promoted {
			return nil, &fatalError{msg: "Cannot declare promoted property outside a constructor", line: declLine}
		}
	}
	// Имя функции участвует в проверке конфликтов `use function X as Y`
	// (в т.ч. условных объявлений внутри if/циклов).
	if p.seenFuncs == nil {
		p.seenFuncs = map[string]bool{}
	}
	if p.funcUses != nil {
		// Объявление функции с именем, уже занятым импортом `use function`:
		// Zend выдаёт компайл-фатал (define_imported). Псевдоним импорта
		// хранится без пространства имён, диагностика — с полным именем.
		if _, ok := p.funcUses[strings.ToLower(name)]; ok {
			return nil, &fatalError{msg: "Cannot declare function " + p.qualifyDecl(name) + " because the name is already in use", line: declLine}
		}
	}
	p.seenFuncs[strings.ToLower(p.qualifyDecl(name))] = true
	return &FuncDecl{Name: name, Params: params, Body: body, ByRefReturn: byref, RetType: ret, Attrs: attrs, Ticks: declTicks}, nil
}

// typeKeywords — ключевые слова, допустимые как имена типов.
var typeKeywords = map[string]bool{
	"array": true, "static": true, "callable": true,
	"null": true, "true": true, "false": true,
}

// isTypeStart сообщает, начинается ли текущий токен с типа.
func (p *Parser) isTypeStart() bool {
	if p.is(TIdent, "") {
		return true
	}
	if p.is(TOp, "?") || p.is(TOp, "(") || p.is(TOp, "\\") {
		return true
	}
	if t := p.cur(); t.Kind == TKeyword && typeKeywords[t.Val] {
		return true
	}
	// Относительное имя `namespace\Foo` в позиции типа.
	if t := p.cur(); t.Kind == TKeyword && t.Val == "namespace" {
		if n := p.peekAt(1); n.Kind == TOp && n.Val == "\\" {
			return true
		}
	}
	return false
}

// typeName разбирает тип и возвращает его нормализованную строку ("" — типа нет).
func (p *Parser) typeName() string {
	if !p.isTypeStart() {
		return ""
	}
	p.typeErr = nil
	return p.unionType()
}

// typeNameErr — как typeName, но возвращает накопленную ошибку разбора типа.
func (p *Parser) typeNameErr() (string, error) {
	s := p.typeName()
	if p.typeErr != nil {
		e := p.typeErr
		p.typeErr = nil
		return "", e
	}
	return s, nil
}

func (p *Parser) unionType() string {
	parts := []string{p.intersectionType()}
	for p.accept(TOp, "|") {
		parts = append(parts, p.intersectionType())
	}
	return strings.Join(parts, "|")
}

func (p *Parser) intersectionType() string {
	s := p.singleType()
	if s == "" {
		return ""
	}
	// Nullable-тип не может входить в intersection (`?A&B` запрещён); PHP
	// останавливает разбор и сообщает об "&" как о неожиданном токене.
	if strings.HasPrefix(s, "?") {
		return s
	}
	for p.is(TOp, "&") && p.typeStartsAt(1) {
		if len(p.peekAt(1).Attrs) > 0 {
			if p.typeErr == nil {
				p.typeErr = p.syntaxErr(p.peekAt(1))
			}
			return s
		}
		p.next()
		s += "&" + p.singleType()
	}
	return s
}

// typeStartsAt сообщает, начинается ли тип через n токенов (для отличия
// пересечения A&B от передачи по ссылке A &$x).
func (p *Parser) typeStartsAt(n int) bool {
	t := p.peekAt(n)
	if t.Kind == TIdent {
		return true
	}
	if t.Kind == TOp && t.Val == "\\" {
		return true
	}
	if t.Kind == TKeyword && typeKeywords[t.Val] {
		return true
	}
	if t.Kind == TKeyword && t.Val == "namespace" {
		return true
	}
	return false
}

func (p *Parser) singleType() string {
	if len(p.cur().Attrs) > 0 {
		if p.typeErr == nil {
			p.typeErr = p.syntaxErr(p.cur())
		}
		return ""
	}
	if p.accept(TOp, "?") {
		return "?" + p.singleType()
	}
	if p.accept(TOp, "(") {
		inner := p.unionType()
		p.expect(TOp, ")")
		return "(" + inner + ")"
	}
	line := p.lineAt(p.cur().Pos)
	var b strings.Builder
	qualified := false
	// Префикс относительного имени `namespace\Foo`.
	if t := p.cur(); t.Kind == TKeyword && t.Val == "namespace" &&
		p.peekAt(1).Kind == TOp && p.peekAt(1).Val == "\\" {
		b.WriteString("namespace\\")
		qualified = true
		p.next() // namespace
		p.next() // \
	}
	for p.accept(TOp, "\\") {
		b.WriteByte('\\')
		qualified = true
	}
	t := p.cur()
	if t.Kind == TIdent || (t.Kind == TKeyword && typeKeywords[t.Val]) {
		b.WriteString(t.Val)
		p.next()
	}
	for p.is(TOp, "\\") {
		qualified = true
		p.next()
		b.WriteByte('\\')
		b.WriteString(p.next().Val)
	}
	raw := b.String()
	if qualified {
		// Квалифицированная запись встроенного типа (\int, namespace\int) —
		// компайл-фатал: Zend zend_compile_single_typename сверяет
		// разрешённое имя со списком builtin_types до разрешения имени.
		if unq, ok := qualifiedBuiltinType(raw); ok {
			p.typeErr = &fatalError{msg: "Type declaration '" + unq + "' must be unqualified", line: line}
			return p.resolveTypeAlias(raw)
		}
	}
	p.maybeConfusableTypeWarning(raw, qualified, line)
	return p.resolveTypeAlias(raw)
}

// confusableTypeSuggestion — подсказка скалярного типа для confusable-имени
// (integer -> int и т.п.). "resource" обрабатывается отдельно.
var confusableTypeSuggestion = map[string]string{
	"integer": "int",
	"double":  "float",
	"boolean": "bool",
}

// qualifiedBuiltinTypes — имена встроенных типов, которые Zend запрещает
// записывать квалифицированно (\int, namespace\int): список builtin_types из
// zend_compile.c (array/callable/static/self/parent в него не входят — они
// разбираются как ключевые слова).
var qualifiedBuiltinTypes = map[string]bool{
	"null": true, "true": true, "false": true, "int": true, "float": true,
	"string": true, "bool": true, "void": true, "never": true,
	"iterable": true, "object": true, "mixed": true,
}

// qualifiedBuiltinType возвращает (в нижнем регистре) имя встроенного типа,
// если raw записан квалифицированно (с ведущим "\" или "namespace\").
func qualifiedBuiltinType(raw string) (string, bool) {
	n := strings.TrimPrefix(raw, "namespace\\")
	n = strings.TrimPrefix(n, "\\")
	n = strings.ToLower(n)
	if qualifiedBuiltinTypes[n] {
		return n, true
	}
	return "", false
}

// maybeConfusableTypeWarning регистрирует Warning для неоднозначных имён типов
// (integer/double/boolean/resource), как Zend. Предупреждение не выдаётся для
// квалифицированных имён (`\integer`, `namespace\integer`, `Foo\boolean`) и
// имён, импортированных через `use` (они явно ссылаются на класс).
func (p *Parser) maybeConfusableTypeWarning(raw string, qualified bool, line int) {
	if qualified || raw == "" || raw[0] == '\\' || strings.Contains(raw, "\\") {
		return
	}
	lower := strings.ToLower(raw)
	if p.classUses != nil {
		if _, ok := p.classUses[lower]; ok {
			return
		}
	}
	fq := lower
	if p.ns != "" {
		fq = p.ns + "\\" + lower
	}
	importClause := ""
	if p.ns != "" {
		importClause = ` or import the class with "use"`
	}
	var msg string
	if sugg, ok := confusableTypeSuggestion[lower]; ok {
		msg = `"` + lower + `" will be interpreted as a class name. Did you mean "` + sugg +
			`"? Write "\` + fq + `"` + importClause + " to suppress this warning"
	} else if lower == "resource" {
		msg = `"resource" is not a supported builtin type and will be interpreted as a class name. Write "\` +
			fq + `"` + importClause + " to suppress this warning"
	} else {
		return
	}
	p.typeWarnings = append(p.typeWarnings, TypeWarning{Line: line, Msg: msg})
}

// resolveTypeAlias заменяет имя класса в объявлении типа на цель use-алиаса.
// Встроенные типы и self/static/parent не трогаются.
func (p *Parser) resolveTypeAlias(name string) string {
	if name == "" || name[0] == '\\' {
		return name
	}
	if rest, ok := strings.CutPrefix(name, "namespace\\"); ok {
		if p.ns == "" {
			return rest
		}
		return p.ns + "\\" + rest
	}
	low := strings.ToLower(name)
	if typeKeywords[low] || builtinTypeNames[low] {
		return name
	}
	first, rest := splitFirstName(name)
	if mapped, ok := p.classUses[strings.ToLower(first)]; ok {
		if rest == "" {
			return mapped
		}
		return mapped + "\\" + rest
	}
	if p.ns != "" {
		return p.ns + "\\" + name
	}
	return name
}

func (p *Parser) paramList() ([]Param, error) {
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	var params []Param
	for !p.is(TOp, ")") {
		var pr Param
		pr.Attrs = p.parseAttrs(p.takeAttrs())
		// модификаторы promoted-свойств (PHP 8.1 constructor promotion)
		for p.kw("public") || p.kw("protected") || p.kw("private") ||
			p.is(TIdent, "readonly") || p.kw("readonly") {
			switch {
			case p.kw("public"):
				pr.PromotedVis = "public"
			case p.kw("protected"):
				pr.PromotedVis = "protected"
			case p.kw("private"):
				pr.PromotedVis = "private"
			default:
				pr.PromotedReadonly = true
			}
			pr.Promoted = true
			p.next()
		}
		// тип параметра (опционально)
		if p.is(TOp, "?") && p.peekAt(1).Kind == TKeyword && p.peekAt(1).Val == "static" {
			return nil, p.syntaxErr(p.peekAt(1))
		}
		pt, perr := p.typeNameErr()
		if perr != nil {
			return nil, perr
		}
		pr.Type = pt
		if p.accept(TOp, "&") {
			pr.ByRef = true
		}
		if p.accept(TOp, "...") {
			pr.Variadic = true
		}
		pr.Name = p.next().Val
		if p.accept(TOp, "=") {
			d, err := p.expr()
			if err != nil {
				return nil, err
			}
			pr.Default = d
		}
		params = append(params, pr)
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	return params, nil
}

// closureExpr разбирает анонимную функцию. `function` ещё не съеден (для
// static function — съеден `static`, а `function` на текущей позиции).
func (p *Parser) closureExpr(static bool) (Expr, error) {
	line := p.lineAt(p.cur().Pos)
	attrs := p.parseAttrs(p.takeAttrs())
	if err := p.expect(TKeyword, "function"); err != nil {
		return nil, err
	}
	byref := p.accept(TOp, "&")
	params, err := p.paramList()
	if err != nil {
		return nil, err
	}
	var uses []Param
	if p.kw("use") {
		p.next()
		if err := p.expect(TOp, "("); err != nil {
			return nil, err
		}
		for !p.is(TOp, ")") {
			var u Param
			if p.accept(TOp, "&") {
				u.ByRef = true
			}
			useTok := p.cur()
			if useTok.Kind == TVar && useTok.Val == "$this" {
				return nil, &fatalError{msg: "Cannot use $this as lexical variable", line: p.lineAt(useTok.Pos)}
			}
			u.Name = p.next().Val
			uses = append(uses, u)
			if !p.accept(TOp, ",") {
				break
			}
		}
		if err := p.expect(TOp, ")"); err != nil {
			return nil, err
		}
	}
	// необязательный тип возврата: `: type`
	retType := ""
	if p.accept(TOp, ":") {
		rt, rerr := p.typeNameErr()
		if rerr != nil {
			return nil, rerr
		}
		retType = rt
	}
	declTicks := p.ticks
	savedTicks := p.ticks
	body, err := p.block()
	p.ticks = savedTicks
	if err != nil {
		return nil, err
	}
	return &ClosureExpr{Params: params, Uses: uses, ByRefReturn: byref, Static: static, RetType: retType, Body: body, Attrs: attrs, Line: line, Ticks: declTicks}, nil
}

// arrowFnExpr разбирает `fn (params) => expr`; `fn` ещё не съеден.
func (p *Parser) arrowFnExpr(static bool) (Expr, error) {
	line := p.lineAt(p.cur().Pos)
	attrs := p.parseAttrs(p.takeAttrs())
	p.next() // fn
	byref := p.accept(TOp, "&")
	params, err := p.paramList()
	if err != nil {
		return nil, err
	}
	// необязательный тип возврата: `: type`
	retType := ""
	if p.accept(TOp, ":") {
		rt, rerr := p.typeNameErr()
		if rerr != nil {
			return nil, rerr
		}
		retType = rt
	}
	if err := p.expect(TOp, "=>"); err != nil {
		return nil, err
	}
	body, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &ArrowFnExpr{Params: params, Body: body, Static: static, RetType: retType, ByRefReturn: byref, Attrs: attrs, Line: line, Ticks: p.ticks}, nil
}

// --- Выражения: приоритетный подъём ---

// expr — обёртка над exprInner: запоминает строку начального токена выражения.
func (p *Parser) expr() (Expr, error) {
	start := p.cur().Pos
	e, err := p.exprInner()
	if err == nil && e != nil {
		if p.exprLines == nil {
			p.exprLines = map[Expr]int{}
		}
		if _, ok := p.exprLines[e]; !ok {
			p.exprLines[e] = p.lineAt(start)
		}
	}
	return e, err
}

func (p *Parser) exprInner() (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	return p.orExpr()
}

func (p *Parser) orExpr() (Expr, error) {
	l, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	for p.kw("or") {
		p.next()
		r, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		l = &LogicalExpr{Op: "or", L: l, R: r}
	}
	return l, nil
}

func (p *Parser) andExpr() (Expr, error) {
	l, err := p.assignExpr()
	if err != nil {
		return nil, err
	}
	for p.kw("and") || p.kw("xor") {
		op := p.next().Val
		r, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		l = &LogicalExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

var assignOps = map[string]bool{
	"=": true, "+=": true, "-=": true, "*=": true, "/=": true, ".=": true, "%=": true,
	"&=": true, "|=": true, "^=": true, "<<=": true, ">>=": true, "**=": true, "??=": true,
}

func (p *Parser) assignExpr() (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	l, err := p.ternaryExpr()
	if err != nil {
		return nil, err
	}
	if p.is(TOp, "=") {
		if !isAssignTarget(l) {
			return nil, p.writeCtxErr(l)
		}
		p.next()
		byref := p.accept(TOp, "&")
		r, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		if byref && !isByRefTarget(r) {
			return nil, p.syntaxErr(p.cur())
		}
		return &AssignExpr{Target: asListTarget(l), Op: "=", Value: r, ByRef: byref}, nil
	}
	if p.cur().Kind == TOp && assignOps[p.cur().Val] {
		if !isAssignTarget(l) {
			return nil, p.writeCtxErr(l)
		}
		op := p.next().Val
		r, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		return &AssignExpr{Target: l, Op: op, Value: r}, nil
	}
	return l, nil
}

// writeCtxErr возвращает компайл-фатал Zend для цели записи, которая
// синтаксически разобрана, но недопустима (вызов функции/метода, nullsafe).
// Для прочих выражений — обычная ошибка разбора (как в PHP: `1 = 2`).
func (p *Parser) writeCtxErr(e Expr) error {
	line := p.lineAt(p.cur().Pos)
	if hasNullsafe(e) {
		return &fatalError{msg: "Can't use nullsafe operator in write context", line: line}
	}
	switch e.(type) {
	case *MethodCallExpr, *StaticCallExpr:
		return &fatalError{msg: "Can't use method return value in write context", line: line}
	case *CallExpr, *DynCallExpr:
		return &fatalError{msg: "Can't use function return value in write context", line: line}
	}
	return p.syntaxErr(p.cur())
}

// hasNullsafe сообщает, что выражение содержит nullsafe-цепочку (?->).
func hasNullsafe(e Expr) bool {
	switch t := e.(type) {
	case *NullsafeExpr, *NullsafeBase:
		return true
	case *PropExpr:
		return hasNullsafe(t.Recv)
	case *MethodCallExpr:
		return hasNullsafe(t.Recv)
	case *IndexExpr:
		return hasNullsafe(t.Arr)
	}
	return false
}

// isThisExpr сообщает, что выражение — переменная $this.
func isThisExpr(e Expr) bool {
	if v, ok := e.(*VarExpr); ok {
		return v.Name == "$this" && v.NameExpr == nil
	}
	return false
}

// isIssetTarget сообщает, что выражение допустимо в isset(): переменная,
// элемент массива, свойство (в том числе статическое) или nullsafe-цепочка,
// оканчивающаяся ими.
func isIssetTarget(e Expr) bool {
	switch x := e.(type) {
	case *VarExpr, *IndexExpr, *PropExpr, *StaticPropExpr:
		return true
	case *NullsafeExpr:
		return isIssetTarget(x.Recv)
	}
	return false
}

// isAssignTarget сообщает, может ли выражение быть целью присваивания.
// Соответствует множеству типов, которые принимает компилятор (см. assign).
func isAssignTarget(e Expr) bool {
	switch e.(type) {
	case *VarExpr, *IndexExpr, *PropExpr, *StaticPropExpr, *ListExpr, *ArrayLit:
		return true
	}
	return false
}

// isByRefTarget сообщает, допустима ли правая часть `= &` (ссылочное
// присваивание). Константы/литералы/вызовы класса — нет.
func isByRefTarget(e Expr) bool {
	switch e.(type) {
	case *ConstExpr, *IntLit, *FloatLit, *StrLit, *BoolLit, *NullLit,
		*ClassKeywordExpr, *CastExpr, *MagicExpr:
		return false
	}
	return true
}

func (p *Parser) ternaryExpr() (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	cond, err := p.coalesceExpr()
	if err != nil {
		return nil, err
	}
	if p.is(TOp, "?") {
		qLine := p.lineAt(p.cur().Pos)
		p.next()
		var then Expr
		if !p.is(TOp, ":") {
			then, err = p.expr()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expect(TOp, ":"); err != nil {
			return nil, err
		}
		els, err := p.ternaryExpr()
		if err != nil {
			return nil, err
		}
		if inner, ok := els.(*TernaryExpr); ok && !inner.Paren {
			outerShort := then == nil
			innerShort := inner.Then == nil
			// Короткие тернарники можно вкладывать друг в друга, остальные
			// комбинации требуют скобок (как в Zend zend_compile_conditional).
			if !(outerShort && innerShort) {
				var pat, fix string
				switch {
				case outerShort:
					pat = "a ?: b ? c : d"
					fix = "either `(a ?: b) ? c : d` or `a ?: (b ? c : d)`"
				case innerShort:
					pat = "a ? b : c ?: d"
					fix = "either `(a ? b : c) ?: d` or `a ? b : (c ?: d)`"
				default:
					pat = "a ? b : c ? d : e"
					fix = "either `(a ? b : c) ? d : e` or `a ? b : (c ? d : e)`"
				}
				return nil, &fatalError{
					msg:  fmt.Sprintf("Unparenthesized `%s` is not supported. Use %s", pat, fix),
					line: qLine,
				}
			}
		}
		return &TernaryExpr{Cond: cond, Then: then, Else: els}, nil
	}
	return cond, nil
}

func (p *Parser) coalesceExpr() (Expr, error) {
	l, err := p.lorExpr()
	if err != nil {
		return nil, err
	}
	for p.accept(TOp, "??") {
		r, err := p.lorExpr()
		if err != nil {
			return nil, err
		}
		l = &CoalesceExpr{L: l, R: r}
	}
	return l, nil
}

func (p *Parser) lorExpr() (Expr, error) {
	l, err := p.landExpr()
	if err != nil {
		return nil, err
	}
	for p.accept(TOp, "||") {
		r, err := p.landExpr()
		if err != nil {
			return nil, err
		}
		l = &LogicalExpr{Op: "||", L: l, R: r}
	}
	return l, nil
}

func (p *Parser) landExpr() (Expr, error) {
	l, err := p.bitorExpr()
	if err != nil {
		return nil, err
	}
	for p.accept(TOp, "&&") {
		r, err := p.bitorExpr()
		if err != nil {
			return nil, err
		}
		l = &LogicalExpr{Op: "&&", L: l, R: r}
	}
	return l, nil
}

func (p *Parser) bitorExpr() (Expr, error) {
	l, err := p.bitxorExpr()
	if err != nil {
		return nil, err
	}
	for p.accept(TOp, "|") || p.accept(TOp, "|=") {
		// |=
		if p.toks[p.pos-1].Val == "|=" {
			p.pos--
			break
		}
		r, err := p.bitxorExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: "|", L: l, R: r}
	}
	return l, nil
}

func (p *Parser) bitxorExpr() (Expr, error) {
	l, err := p.bitandExpr()
	if err != nil {
		return nil, err
	}
	for p.accept(TOp, "^") {
		r, err := p.bitandExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: "^", L: l, R: r}
	}
	return l, nil
}

func (p *Parser) bitandExpr() (Expr, error) {
	l, err := p.equalityExpr()
	if err != nil {
		return nil, err
	}
	for p.is(TOp, "&") {
		p.next()
		r, err := p.equalityExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: "&", L: l, R: r}
	}
	return l, nil
}

func (p *Parser) equalityExpr() (Expr, error) {
	l, err := p.relationalExpr()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		switch {
		case p.is(TOp, "=="), p.is(TOp, "!="), p.is(TOp, "<>"), p.is(TOp, "==="),
			p.is(TOp, "!=="), p.is(TOp, "<=>"):
			op = p.cur().Val
		default:
			return l, nil
		}
		p.next()
		r, err := p.relationalExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: op, L: l, R: r}
	}
}

func (p *Parser) relationalExpr() (Expr, error) {
	l, err := p.shiftExpr()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		switch {
		case p.is(TOp, "<"), p.is(TOp, "<="), p.is(TOp, ">"), p.is(TOp, ">="):
			op = p.cur().Val
		default:
			return p.instanceofTail(l)
		}
		p.next()
		r, err := p.shiftExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: op, L: l, R: r}
	}
}

func (p *Parser) instanceofTail(l Expr) (Expr, error) {
	for p.kw("instanceof") {
		p.next()
		if p.is(TOp, "(") {
			// `instanceof (expr)` — имя класса вычисляется выражением.
			e, err := p.paren()
			if err != nil {
				return nil, err
			}
			l = &InstanceOfExpr{X: l, ClassExpr: e}
			continue
		}
		name := p.className()
		l = &InstanceOfExpr{X: l, Class: name}
	}
	return l, nil
}

func (p *Parser) className() string {
	return p.resolveClassName(p.rawQualifiedName())
}

// isNameTok сообщает, что токен может быть сегментом квалифицированного имени
// (идентификатор или ключевое слово — в PHP reserved-слова допустимы как
// сегменты пространств имён).
func isNameTok(t Token) bool {
	return t.Kind == TIdent || t.Kind == TKeyword
}

// tokName возвращает текст токена как он записан в исходнике. Лексер хранит
// ключевые слова в Val в нижнем регистре, но имена объявляемых сущностей
// (классы, константы, кейсы enum, методы) должны сохранять регистр: PHP
// различает `A::Case` и `A::case`, `class List` и `class list`.
func tokName(t Token) string {
	if t.Raw != "" {
		return t.Raw
	}
	return t.Val
}

// rawQualifiedName читает имя вида `[\]seg(\seg)*` и возвращает его как есть,
// с сохранением ведущего `\`. Для групп `Foo\{...}` останавливается перед `\{`.
func (p *Parser) rawQualifiedName() string {
	var b strings.Builder
	// Относительное имя `namespace\Foo` — префикс текущего пространства имён.
	rel := false
	if p.is(TKeyword, "namespace") && p.peekAt(1).Kind == TOp && p.peekAt(1).Val == "\\" {
		p.next() // namespace
		p.next() // \
		rel = true
	}
	for p.is(TOp, "\\") && isNameTok(p.peekAt(1)) {
		p.next() // \
		b.WriteByte('\\')
		b.WriteString(tokName(p.next()))
	}
	if b.Len() == 0 {
		b.WriteString(tokName(p.next()))
	}
	for p.is(TOp, "\\") && isNameTok(p.peekAt(1)) {
		p.next() // \
		b.WriteByte('\\')
		b.WriteString(p.next().Val)
	}
	if rel {
		// Ведущий `\` делает имя полностью определённым (fallback не нужен).
		if p.ns != "" {
			return "\\" + p.ns + "\\" + b.String()
		}
		return "\\" + b.String()
	}
	return b.String()
}

// kwAdjacentBackslash сообщает, что текущий ключевой токен сразу (без
// пробела) переходит в `\` — в Zend такой текст лексится единым
// T_NAME_QUALIFIED (`extends\A`, `implements\A`, `namespace\X`).
func (p *Parser) kwAdjacentBackslash() bool {
	t := p.cur()
	n := p.peekAt(1)
	return t.Kind == TKeyword && n.Kind == TOp && n.Val == "\\" && n.Pos == t.Pos+len(t.Val)
}

// errAdjacentNamespacedName строит "unexpected namespaced name ...".
func (p *Parser) errAdjacentNamespacedName(what string) error {
	pos := p.cur().Pos
	raw := p.rawNameFromHere()
	return &parseError{
		msg:  fmt.Sprintf("syntax error, unexpected namespaced name \"%s\", expecting %q", raw, what),
		line: p.lineAt(pos),
	}
}

// rawNameFromHere читает имя `[\]seg(\seg)*` (как rawQualifiedName), но
// возвращает исходный текст токенов, включая ведущий `\`, без разыменования
// namespace-relative имён: используется только для текстов ошибок.
func (p *Parser) rawNameFromHere() string {
	var b strings.Builder
	for p.is(TOp, "\\") && isNameTok(p.peekAt(1)) {
		p.next() // \
		b.WriteByte('\\')
		b.WriteString(tokName(p.next()))
	}
	if b.Len() == 0 {
		b.WriteString(tokName(p.next()))
	}
	for p.is(TOp, "\\") && isNameTok(p.peekAt(1)) {
		p.next() // \
		b.WriteByte('\\')
		b.WriteString(tokName(p.next()))
	}
	return b.String()
}

// splitFirstName делит имя на первый сегмент и остаток (без ведущего `\`).
func splitFirstName(name string) (string, string) {
	name = strings.TrimPrefix(name, "\\")
	if i := strings.IndexByte(name, '\\'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return name, ""
}

// lastSegment возвращает последний сегмент имени (для псевдонима по умолчанию).
func lastSegment(name string) string {
	name = strings.TrimPrefix(name, "\\")
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// resolveClassName приводит имя класса к полному (FQ) имени с учётом текущего
// namespace и use-алиасов. Ведущий `\` даёт абсолютное имя, self/static/parent
// не трогаются. У классов нет отката к глобальному пространству.
func (p *Parser) resolveClassName(raw string) string {
	if raw == "" {
		return raw
	}
	if raw[0] == '\\' {
		return raw[1:]
	}
	low := strings.ToLower(raw)
	if low == "self" || low == "static" || low == "parent" {
		return raw
	}
	first, rest := splitFirstName(raw)
	if mapped, ok := p.classUses[strings.ToLower(first)]; ok {
		if rest == "" {
			return mapped
		}
		return mapped + "\\" + rest
	}
	if p.ns != "" {
		return p.ns + "\\" + raw
	}
	return raw
}

// resolveFuncName возвращает FQ-имя функции и глобальный fallback (для
// неквалифицированных имён внутри namespace).
func (p *Parser) resolveFuncName(raw string) (name, fallback string) {
	if raw == "" {
		return raw, ""
	}
	if raw[0] == '\\' {
		return raw[1:], ""
	}
	first, rest := splitFirstName(raw)
	if rest != "" {
		// Квалифицированное имя (`C\f`): первый сегмент ищется только в
		// таблице импортов классов (Zend zend_resolve_function_name смотрит
		// FC(imports); импорты function/const для квалифицированных имён не
		// применяются). Отката к глобальному пространству нет.
		if mapped, ok := p.classUses[strings.ToLower(first)]; ok {
			return mapped + "\\" + rest, ""
		}
		if p.ns != "" {
			return p.ns + "\\" + raw, ""
		}
		return raw, ""
	}
	if mapped, ok := p.funcUses[strings.ToLower(first)]; ok {
		return mapped, ""
	}
	if p.ns != "" {
		return p.ns + "\\" + raw, raw
	}
	return raw, ""
}

// resolveConstName возвращает FQ-имя константы и глобальный fallback.
func (p *Parser) resolveConstName(raw string) (name, fallback string) {
	if raw == "" {
		return raw, ""
	}
	if raw[0] == '\\' {
		return raw[1:], ""
	}
	first, rest := splitFirstName(raw)
	if rest != "" {
		// Квалифицированное имя (`C\K`): как и для функций, применяется
		// только таблица импортов классов; импорты const не действуют
		// (Zend zend_resolve_const_name → FC(imports) kind == class).
		if mapped, ok := p.classUses[strings.ToLower(first)]; ok {
			return mapped + "\\" + rest, ""
		}
		if p.ns != "" {
			return p.ns + "\\" + raw, ""
		}
		return raw, ""
	}
	if mapped, ok := p.constUses[first]; ok {
		return mapped, ""
	}
	if p.ns != "" {
		return p.ns + "\\" + raw, raw
	}
	return raw, ""
}

// assertArgText возвращает исходный текст первого аргумента вызова assert(),
// начиная с позиции start (до запятой/закрывающей скобки верхнего уровня).
func (p *Parser) assertArgText(start int) string {
	depth := 0
	var quote byte
	for i := start; i < len(p.src); i++ {
		c := p.src[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[', '{':
			depth++
		case ')':
			if depth == 0 {
				return normalizeAssertArgText(strings.TrimSpace(p.src[start:i]))
			}
			depth--
		case ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return normalizeAssertArgText(strings.TrimSpace(p.src[start:i]))
			}
		}
	}
	return ""
}

// normalizeAssertArgText выравнивает отступы многострочного текста assert():
// Zend рендерит выражение через zend_ast_export с собственными отступами, а не
// копирует исходные пробелы. Убираем общий минимальный отступ последующих
// строк (typed_class_constants_ast_print).
func normalizeAssertArgText(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	min := -1
	for _, ln := range lines[1:] {
		t := strings.TrimLeft(ln, " \t")
		if t == "" {
			continue
		}
		ind := len(ln) - len(t)
		if min < 0 || ind < min {
			min = ind
		}
	}
	if min <= 0 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		ln := lines[i]
		j := 0
		for j < len(ln) && j < min && (ln[j] == ' ' || ln[j] == '\t') {
			j++
		}
		lines[i] = ln[j:]
	}
	return strings.Join(lines, "\n")
}

// qualifyDecl добавляет текущее пространство имён к имени объявления функции.
func (p *Parser) qualifyDecl(name string) string {
	if p.ns != "" {
		return p.ns + "\\" + name
	}
	return name
}

// nameRefExpr разбирает имя (возможно квалифицированное) как выражение: вызов
// функции, ссылку на класс (перед `::`) или константу.
func (p *Parser) nameRefExpr() (Expr, error) {
	pos := p.cur().Pos
	raw := p.rawQualifiedName()
	up := strings.ToUpper(raw)
	if magicConsts[up] {
		return &MagicExpr{Name: up, Line: lineOf(p.src, pos), NS: p.ns}, nil
	}
	if p.is(TOp, "(") {
		p.next()
		argStart := p.cur().Pos
		args, err := p.argList()
		if err != nil {
			return nil, err
		}
		if raw == "eval" {
			return &CallExpr{Name: "eval", Args: args}, nil
		}
		fq, fb := p.resolveFuncName(raw)
		ce := &CallExpr{Name: fq, Fallback: fb, Args: args}
		if strings.EqualFold(strings.TrimLeft(raw, "\\"), "assert") && len(args) > 0 {
			ce.AssertText = "assert(" + p.assertArgText(argStart) + ")"
		}
		return ce, nil
	}
	if p.is(TOp, "::") {
		return &ConstExpr{Name: p.resolveClassName(raw)}, nil
	}
	fq, fb := p.resolveConstName(raw)
	return &ConstExpr{Name: fq, Fallback: fb}, nil
}

// classRefName извлекает имя класса/специального селектора (self/static/parent)
// из выражения слева от `::`.
func classRefName(x Expr) string {
	switch v := x.(type) {
	case *VarExpr:
		// Переменная (в т.ч. $this) — класс задан значением: имя резолвится
		// в рантайме (`$e::class` → __php_class_of), а не текстом "$e".
		_ = v
		return ""
	case *ConstExpr:
		// `Test::NAME::CONST` — имя класса вычисляется из значения константы;
		// резолвим в рантайме (имя содержит "::").
		if strings.Contains(v.Name, "::") {
			return ""
		}
		return v.Name
	}
	return ""
}

func (p *Parser) shiftExpr() (Expr, error) {
	l, err := p.additiveExpr()
	if err != nil {
		return nil, err
	}
	for p.is(TOp, "<<") || p.is(TOp, ">>") {
		op := p.next().Val
		r, err := p.additiveExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

func (p *Parser) additiveExpr() (Expr, error) {
	l, err := p.mulExpr()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		switch {
		case p.is(TOp, "+"), p.is(TOp, "-"), p.is(TOp, "."):
			op = p.cur().Val
		default:
			return l, nil
		}
		p.next()
		r, err := p.mulExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: op, L: l, R: r}
	}
}

func (p *Parser) mulExpr() (Expr, error) {
	l, err := p.unaryExpr()
	if err != nil {
		return nil, err
	}
	for p.is(TOp, "*") || p.is(TOp, "/") || p.is(TOp, "%") {
		op := p.next().Val
		r, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		l = &BinExpr{Op: op, L: l, R: r}
	}
	return l, nil
}

var castTypes = map[string]bool{
	"int": true, "integer": true, "float": true, "double": true, "real": true,
	"string": true, "bool": true, "boolean": true, "array": true, "object": true,
	"unset": true, "binary": true,
}

func (p *Parser) unaryExpr() (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	switch {
	case p.is(TOp, "!") || p.is(TOp, "~") || p.is(TOp, "+") || p.is(TOp, "-") || p.is(TOp, "@"):
		op := p.next().Val
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		// Zend: '!' имеет МЕНЬШИЙ приоритет, чем instanceof, поэтому
		// `!$x instanceof Y` — это `!($x instanceof Y)`.
		if op == "!" {
			if x, err = p.instanceofTail(x); err != nil {
				return nil, err
			}
		}
		if op == "@" {
			return &UnaryExpr{Op: "@", X: x}, nil
		}
		// PHP: -9223372036854775808 — это int PHP_INT_MIN, а не float.
		if op == "-" {
			if fl, ok := x.(*FloatLit); ok && fl.Val == "9223372036854775808" {
				return &IntLit{V: math.MinInt64}, nil
			}
		}
		return &UnaryExpr{Op: op, X: x}, nil
	case p.kw("not"):
		p.next()
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		if x, err = p.instanceofTail(x); err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: "!", X: x}, nil
	case p.is(TOp, "++") || p.is(TOp, "--"):
		op := p.next().Val
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &IncDecExpr{Op: op, Pre: true, X: x}, nil
	case p.kw("clone"):
		p.next()
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &CloneExpr{X: x}, nil
	case p.kw("throw"):
		p.next()
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &ThrowExpr{X: x}, nil
	case p.kw("new"):
		return p.newExpr()
	case p.is(TIdent, "include") || p.is(TIdent, "include_once") ||
		p.is(TIdent, "require") || p.is(TIdent, "require_once"):
		name := p.next().Val
		path, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		return &IncludeExpr{
			Path:    path,
			Once:    strings.HasSuffix(name, "_once"),
			Require: strings.HasPrefix(name, "require"),
		}, nil
	case p.kw("print"):
		p.next()
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		return &PrintExpr{X: x}, nil
	case p.kw("isset"):
		p.next()
		if err := p.expect(TOp, "("); err != nil {
			return nil, err
		}
		var args []Expr
		for !p.is(TOp, ")") {
			argLine := p.lineAt(p.cur().Pos)
			a, err := p.expr()
			if err != nil {
				return nil, err
			}
			if !isIssetTarget(a) {
				// Zend допускает в isset() только выборку переменной.
				return nil, &fatalError{msg: `Cannot use isset() on the result of an expression (you can use "null !== expression" instead)`, line: argLine}
			}
			args = append(args, a)
			if !p.accept(TOp, ",") {
				break
			}
		}
		if err := p.expect(TOp, ")"); err != nil {
			return nil, err
		}
		return &IssetExpr{Args: args}, nil
	case p.kw("empty"):
		p.next()
		x, err := p.paren()
		if err != nil {
			return nil, err
		}
		return &EmptyExpr{X: x}, nil
	case p.kw("exit") || p.kw("die"):
		p.next()
		var arg Expr
		var err error
		if p.is(TOp, "(") {
			p.next()
			if !p.is(TOp, ")") {
				arg, err = p.expr()
				if err != nil {
					return nil, err
				}
			}
			if err := p.expect(TOp, ")"); err != nil {
				return nil, err
			}
		} else if !p.is(TOp, ";") && !p.is(TEOF, "") && !p.is(TInlineHTML, "") &&
			!atBracketOrEOF(p.cur()) && !p.is(TOp, ",") {
			arg, err = p.expr()
			if err != nil {
				return nil, err
			}
		}
		return &ExitExpr{X: arg}, nil
	}
	// приведение типа (int) и т.п.
	if p.is(TOp, "(") && (p.peekAt(1).Kind == TIdent || p.peekAt(1).Kind == TKeyword) && castTypes[p.peekAt(1).Val] && p.peekAt(2).Val == ")" {
		castTok := p.peekAt(1)
		p.next()
		typ := p.next().Val
		p.next() // )
		if typ == "real" {
			return nil, &parseError{msg: "The (real) cast has been removed, use (float) instead", line: p.lineAt(castTok.Pos)}
		}
		if typ == "unset" {
			return nil, &fatalError{msg: "The (unset) cast is no longer supported", line: p.lineAt(castTok.Pos)}
		}
		x, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &CastExpr{Type: typ, X: x}, nil
	}
	l, err := p.powExpr()
	if err != nil {
		return nil, err
	}
	// Присваивание в PHP привязывается к переменной-операнду (shift-правило
	// yacc): `1 + $a = 1` разбирается как `1 + ($a = 1)`, а не `(1 + $a) = 1`.
	// Поэтому `=`/`+=` распознаём прямо здесь, на уровне операнда.
	if p.is(TOp, "=") {
		if !isAssignTarget(l) {
			return nil, p.writeCtxErr(l)
		}
		p.next()
		byref := p.accept(TOp, "&")
		r, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		if byref && !isByRefTarget(r) {
			return nil, p.syntaxErr(p.cur())
		}
		return &AssignExpr{Target: asListTarget(l), Op: "=", Value: r, ByRef: byref}, nil
	}
	if p.cur().Kind == TOp && assignOps[p.cur().Val] {
		if !isAssignTarget(l) {
			return nil, p.writeCtxErr(l)
		}
		op := p.next().Val
		r, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		return &AssignExpr{Target: l, Op: op, Value: r}, nil
	}
	return l, nil
}

func (p *Parser) powExpr() (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	l, err := p.postfixExpr()
	if err != nil {
		return nil, err
	}
	if p.accept(TOp, "**") {
		r, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &BinExpr{Op: "**", L: l, R: r}, nil
	}
	return l, nil
}

func (p *Parser) postfixExpr() (Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	return p.postfixTail(x)
}

// memberAccess разбирает `name[(args)]` после `->`/`?->` с получателем recv.
func (p *Parser) memberAccess(recv Expr) (Expr, error) {
	var name string
	var nameExpr Expr
	namePos := p.cur().Pos
	if p.cur().Kind == TVar || p.is(TOp, "{") {
		ne, err := p.dynMemberName()
		if err != nil {
			return nil, err
		}
		nameExpr = ne
	} else {
		name = p.next().Val
	}
	if p.accept(TOp, "(") {
		// Zend: литеральное не-строковое имя метода — ошибка компиляции.
		if nameExpr != nil && !isMethodNameExpr(nameExpr) {
			return nil, &fatalError{msg: "Method name must be a string", line: p.lineAt(namePos)}
		}
		args, err := p.argList()
		if err != nil {
			return nil, err
		}
		return &MethodCallExpr{Recv: recv, Name: name, NameExpr: nameExpr, Args: args}, nil
	}
	return &PropExpr{Recv: recv, Name: name, NameExpr: nameExpr}, nil
}

// isMethodNameExpr сообщает, что выражение-имя метода может быть вычислено в
// рантайме (не литеральный int/float/bool/null/array): Zend отвергает
// последние на этапе компиляции ("Method name must be a string").
func isMethodNameExpr(e Expr) bool {
	switch e.(type) {
	case *IntLit, *FloatLit, *BoolLit, *NullLit, *ArrayLit:
		return false
	}
	return true
}

// constClassNameNonString сообщает, что выражение слева от `::` — константно
// вычислимое имя класса, не являющееся строкой (Zend: "Illegal class name").
func constClassNameNonString(e Expr) bool {
	switch v := e.(type) {
	case *StrLit:
		return false
	case *IntLit, *FloatLit, *BoolLit, *NullLit, *ArrayLit:
		return true
	case *UnaryExpr:
		switch v.X.(type) {
		case *IntLit, *FloatLit, *BoolLit, *NullLit:
			return true
		}
		return false
	case *BinExpr:
		if v.Op == "." {
			// Конкатенация литералов даёт строку (или ошибку, если операнд
			// не конвертируется) — "Illegal class name" не эмитится.
			return false
		}
		return constClassNameNonString(v.L) && constClassNameNonString(v.R)
	}
	return false
}

// postfixTail разбирает остаток postfix-цепочки для уже разобранного выражения.
func (p *Parser) postfixTail(x Expr) (Expr, error) {
	for {
		switch {
		case p.accept(TOp, "("):
			args, err := p.argList()
			if err != nil {
				return nil, err
			}
			x = &DynCallExpr{Name: x, Args: args}
		case p.accept(TOp, "["):
			var key Expr
			if !p.is(TOp, "]") {
				k, err := p.expr()
				if err != nil {
					return nil, err
				}
				key = k
			}
			if err := p.expect(TOp, "]"); err != nil {
				return nil, err
			}
			x = &IndexExpr{Arr: x, Key: key}
		case p.accept(TOp, "?->"):
			member, err := p.memberAccess(&NullsafeBase{})
			if err != nil {
				return nil, err
			}
			p.noIncDec++
			body, err := p.postfixTail(member)
			p.noIncDec--
			if err != nil {
				return nil, err
			}
			ns := &NullsafeExpr{Recv: x, Body: body}
			// Постфиксные ++/-- относятся ко всей nullsafe-цепочке
			// (`$a?->b++` — ошибка write context, а не часть звена).
			if p.is(TOp, "++") || p.is(TOp, "--") {
				op := p.next().Val
				return &IncDecExpr{Op: op, Pre: false, X: ns}, nil
			}
			return ns, nil
		case p.accept(TOp, "->"):
			member, err := p.memberAccess(x)
			if err != nil {
				return nil, err
			}
			x = member
		case p.is(TOp, "::"):
			// x::name(...) / x::CONST / x::$prop / x::${expr} / x::$var(...)
			// Литеральное/константное не-строковое имя класса слева от `::`
			// Zend отвергает на этапе компиляции (bug70782, bug77530).
			if constClassNameNonString(x) {
				line := 0
				if p.exprLines != nil {
					line = p.exprLines[x]
				}
				if line <= 0 {
					line = p.lineAt(p.cur().Pos)
				}
				return nil, &fatalError{msg: "Illegal class name", line: line}
			}
			p.next()
			t := p.next()
			clsName := classRefName(x)
			// Класс задан произвольным выражением (`('A' . '')::$b`, `$a[0]::C`):
			// сохраняем выражение и резолвим класс в рантайме.
			var clsExpr Expr
			if clsName == "" {
				clsExpr = x
			}
			if t.Kind == TVar {
				if t.Val == "$" {
					ne, err := p.dynAfterDollar()
					if err != nil {
						return nil, err
					}
					// `Class::$$var(...)` / `Class::${expr}(...)` — вызов
					// статического метода с динамическим именем.
					if p.accept(TOp, "(") {
						args, err := p.argList()
						if err != nil {
							return nil, err
						}
						x = &StaticCallExpr{Class: clsName, ClassExpr: clsExpr, NameExpr: ne, Args: args}
						break
					}
					x = &StaticPropExpr{Class: clsName, ClassExpr: clsExpr, NameExpr: ne}
					break
				}
				name := strings.TrimPrefix(t.Val, "$")
				if p.accept(TOp, "(") {
					args, err := p.argList()
					if err != nil {
						return nil, err
					}
					x = &StaticCallExpr{Class: clsName, ClassExpr: clsExpr, NameExpr: &VarExpr{Name: t.Val}, Args: args}
				} else {
					x = &StaticPropExpr{Class: clsName, ClassExpr: clsExpr, Name: name}
				}
				break
			}
			name := t.Val
			if t.Kind == TOp && t.Val == "{" {
				// `Class::{expr}(...)` / `Class::{expr}` — динамическое имя
				// статического члена или константы (`{` уже прочитана как t).
				ne, err := p.expr()
				if err != nil {
					return nil, err
				}
				if err := p.expect(TOp, "}"); err != nil {
					return nil, err
				}
				if p.accept(TOp, "(") {
					// Zend: литеральное не-строковое имя метода — ошибка
					// компиляции "Method name must be a string".
					if !isMethodNameExpr(ne) {
						return nil, &fatalError{msg: "Method name must be a string", line: p.lineAt(t.Pos)}
					}
					args, err := p.argList()
					if err != nil {
						return nil, err
					}
					x = &StaticCallExpr{Class: clsName, ClassExpr: clsExpr, NameExpr: ne, Args: args}
				} else {
					x = &ConstExpr{Name: clsName, ClassExpr: clsExpr, NameExpr: ne}
				}
				break
			}
			if p.accept(TOp, "(") {
				args, err := p.argList()
				if err != nil {
					return nil, err
				}
				x = &StaticCallExpr{Class: clsName, ClassExpr: clsExpr, Name: name, Args: args}
			} else if strings.EqualFold(name, "class") {
				// `Foo::class` — не константа класса, а ключевое слово (PHP 8).
				x = &ClassKeywordExpr{Class: clsName, ClassExpr: clsExpr}
			} else {
				// Имя константы класса/кейса enum регистрозависимо.
				cname := tokName(t)
				if clsExpr != nil {
					x = &ConstExpr{Name: cname, ClassExpr: clsExpr}
				} else {
					x = &ConstExpr{Name: clsName + "::" + cname}
				}
			}
		case p.is(TOp, "{"):
			// PHP 8.0 удалил доступ к смещению строк/массивов через фигурные
			// скобки — это ошибка компиляции (не разбора).
			return nil, &fatalError{
				msg:  "Array and string offset access syntax with curly braces is no longer supported",
				line: p.lineAt(p.cur().Pos),
			}
		case p.is(TOp, "++") || p.is(TOp, "--"):
			if p.noIncDec > 0 {
				return x, nil
			}
			op := p.next().Val
			x = &IncDecExpr{Op: op, Pre: false, X: x}
		default:
			return x, nil
		}
	}
}

func (p *Parser) argList() ([]Expr, error) {
	var args []Expr
	namedSeen := false
	spreadSeen := false
	if p.accept(TOp, ")") {
		return args, nil
	}
	for {
		// Распаковка аргумента (PHP 8): f(...$args) либо first-class callable
		// f(...) — маркер ArgSpreadExpr без выражения.
		if p.accept(TOp, "...") {
			if p.accept(TOp, ")") {
				return []Expr{&ArgSpreadExpr{}}, nil
			}
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			spreadSeen = true
			args = append(args, &ArgSpreadExpr{X: v})
			if !p.accept(TOp, ",") {
				break
			}
			if p.is(TOp, ")") {
				break
			}
			continue
		}
		// Именованный аргумент (PHP 8): name: value. Имя может быть
		// зарезервированным словом (array:, list:, class: и т.п.).
		if (p.cur().Kind == TIdent || p.cur().Kind == TKeyword) && p.peekAt(1).Kind == TOp && p.peekAt(1).Val == ":" {
			name := p.next().Val
			p.next() // :
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			namedSeen = true
			args = append(args, &NamedArgExpr{Name: name, X: v})
			if !p.accept(TOp, ",") {
				break
			}
			if p.is(TOp, ")") {
				break
			}
			continue
		}
		argLine := p.lineAt(p.cur().Pos)
		if len(args) > 0 && p.is(TOp, ",") {
			// `f($a,,` — Zend: unexpected ",", expecting ")".
			return nil, &parseError{msg: `syntax error, unexpected token ",", expecting ")"`, line: argLine}
		}
		a, err := p.expr()
		if err != nil {
			return nil, err
		}
		if namedSeen {
			// Позиционный аргумент после именованного — компайл-фатал Zend
			// (распаковка ...$x после именованных допустима).
			return nil, &fatalError{msg: "Cannot use positional argument after named argument", line: argLine}
		}
		if spreadSeen {
			return nil, &fatalError{msg: "Cannot use positional argument after argument unpacking", line: argLine}
		}
		args = append(args, a)
		if !p.accept(TOp, ",") {
			break
		}
		if p.is(TOp, ")") {
			break
		}
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *Parser) newExpr() (Expr, error) {
	p.next() // new
	// new [attrs] [abstract|final|readonly]* class { ... } — анонимный класс.
	if p.anonClassAhead() {
		return p.anonClass()
	}
	// new class [(args)] [extends X] [implements Y] { ... }
	if p.kw("class") {
		return p.anonClass()
	}
	if p.cur().Kind == TVar {
		t := p.next()
		var ce Expr = &VarExpr{Name: t.Val}
		for {
			if p.accept(TOp, "?->") {
				member, err := p.memberAccess(&NullsafeBase{})
				if err != nil {
					return nil, err
				}
				body, err := p.postfixTail(member)
				if err != nil {
					return nil, err
				}
				ce = &NullsafeExpr{Recv: ce, Body: body}
				break
			}
			if p.accept(TOp, "->") {
				name := p.next().Val
				ce = &PropExpr{Recv: ce, Name: name}
				continue
			}
			// Статическое свойство как имя класса: `new $a::$s`
			// (Zend: new_variable T_PAAMAYIM_NEKUDOTAYIM simple_variable).
			if p.accept(TOp, "::") {
				st := p.next()
				if st.Kind != TVar {
					return nil, p.syntaxErrExpect(st, `variable or "$"`)
				}
				ce = &StaticPropExpr{ClassExpr: ce, Name: strings.TrimPrefix(st.Val, "$")}
				continue
			}
			// Индекс у динамического имени класса: `new $a[++$i][++$i]`
			// (Zend: class-name expression — переменная с индексами).
			if p.accept(TOp, "[") {
				var key Expr
				if !p.is(TOp, "]") {
					var kerr error
					key, kerr = p.expr()
					if kerr != nil {
						return nil, kerr
					}
				}
				if err := p.expect(TOp, "]"); err != nil {
					return nil, err
				}
				ce = &IndexExpr{Arr: ce, Key: key}
				continue
			}
			break
		}
		var args []Expr
		if p.accept(TOp, "(") {
			var err error
			args, err = p.argList()
			if err != nil {
				return nil, err
			}
		}
		return &NewExpr{ClassExpr: ce, Args: args}, nil
	}
	// new (expr) [(args)] — имя класса вычисляется выражением (PHP 8.0+).
	if p.is(TOp, "(") {
		ce, err := p.paren()
		if err != nil {
			return nil, err
		}
		var args []Expr
		if p.accept(TOp, "(") {
			args, err = p.argList()
			if err != nil {
				return nil, err
			}
		}
		return &NewExpr{ClassExpr: ce, Args: args}, nil
	}
	name := p.className()
	// new A::$s [(args)] — имя класса из статического свойства.
	if p.accept(TOp, "::") {
		st := p.next()
		if st.Kind != TVar {
			return nil, p.syntaxErrExpect(st, `variable or "$"`)
		}
		ce := &StaticPropExpr{Class: name, Name: strings.TrimPrefix(st.Val, "$")}
		var args []Expr
		if p.accept(TOp, "(") {
			var err error
			args, err = p.argList()
			if err != nil {
				return nil, err
			}
		}
		return &NewExpr{ClassExpr: ce, Args: args}, nil
	}
	// new class { ... } — пока не поддерживаем
	var args []Expr
	if p.accept(TOp, "(") {
		var err error
		args, err = p.argList()
		if err != nil {
			return nil, err
		}
	}
	return &NewExpr{Class: name, Args: args}, nil
}

// isAnonClassModifier — модификатор анонимного класса (abstract/final —
// ключевые слова, readonly лексер отдаёт как идентификатор).
func isAnonClassModifier(t Token) bool {
	if t.Kind != TKeyword && t.Kind != TIdent {
		return false
	}
	return t.Val == "abstract" || t.Val == "final" || t.Val == "readonly"
}

// anonClassAhead — после `new` идут атрибуты и/или модификаторы, а затем
// `class` (анонимный класс). Без модификаторов/атрибутов возвращает false,
// чтобы `new class` обрабатывался обычным путём.
func (p *Parser) anonClassAhead() bool {
	attrs := p.pos < len(p.toks) && len(p.toks[p.pos].Attrs) > 0
	mods := 0
	for i := 0; i < 16; i++ {
		t := p.peekAt(i)
		if isAnonClassModifier(t) {
			mods++
			continue
		}
		if t.Kind == TKeyword && t.Val == "class" {
			return attrs || mods > 0
		}
		return false
	}
	return false
}

// anonClass разбирает `[attrs] [modifiers] class [(args)] [extends X] [implements Y] { ... }`
// после уже съеденного `new`.
func (p *Parser) anonClass() (Expr, error) {
	anonAttrs := p.parseAttrs(p.takeAttrs())
	abstractLine, finalLine := 0, 0
	readonlyCount := 0
mods:
	for isAnonClassModifier(p.cur()) {
		switch p.cur().Val {
		case "abstract":
			if abstractLine == 0 {
				abstractLine = p.lineAt(p.cur().Pos)
			}
		case "final":
			if finalLine == 0 {
				finalLine = p.lineAt(p.cur().Pos)
			}
		case "readonly":
			readonlyCount++
		default:
			break mods
		}
		if extra := p.parseAttrs(p.takeAttrs()); len(extra) > 0 {
			anonAttrs = append(anonAttrs, extra...)
		}
		p.next()
	}
	if abstractLine > 0 {
		return nil, &fatalError{msg: "Cannot use the abstract modifier on an anonymous class", line: abstractLine}
	}
	if finalLine > 0 {
		return nil, &fatalError{msg: "Cannot use the final modifier on an anonymous class", line: finalLine}
	}
	if readonlyCount > 1 {
		return nil, &fatalError{msg: "Multiple readonly modifiers are not allowed", line: p.lineAt(p.cur().Pos)}
	}
	p.next() // class
	var args []Expr
	if p.accept(TOp, "(") {
		a, err := p.argList()
		if err != nil {
			return nil, err
		}
		args = a
	}
	cd := &ClassDecl{Line: p.lineAt(p.cur().Pos), Attrs: anonAttrs, IsReadonly: readonlyCount > 0}
	if p.accept(TKeyword, "extends") {
		cd.Parent = p.className()
	}
	if p.accept(TKeyword, "implements") {
		for {
			cd.Interfaces = append(cd.Interfaces, p.className())
			if !p.accept(TOp, ",") {
				break
			}
		}
	}
	if err := p.expect(TOp, "{"); err != nil {
		return nil, err
	}
	for !p.is(TOp, "}") {
		if p.is(TEOF, "") {
			return nil, fmt.Errorf("parser: незакрытое тело анонимного класса")
		}
		if p.accept(TOp, ";") {
			continue
		}
		if p.kw("use") {
			if cd.UsesLine == 0 {
				cd.UsesLine = p.lineAt(p.cur().Pos)
			}
			p.next()
			for {
				cd.Uses = append(cd.Uses, p.className())
				if !p.accept(TOp, ",") {
					break
				}
			}
			if p.is(TOp, "{") {
				p.next()
				for !p.is(TOp, "}") && !p.is(TEOF, "") {
					if p.accept(TOp, ";") {
						continue
					}
					ta := &TraitAdapt{}
					first := p.className()
					if p.accept(TOp, "::") {
						ta.Trait = first
						ta.Method = tokName(p.next())
					} else {
						ta.Method = first
					}
					switch {
					case p.kw("insteadof") || p.is(TIdent, "insteadof"):
						p.next()
						for {
							ta.Insteadof = append(ta.Insteadof, p.className())
							if !p.accept(TOp, ",") {
								break
							}
						}
					case p.kw("as"):
						p.next()
						ta.Alias = true
						switch {
						case p.kw("public") || p.kw("protected") || p.kw("private"):
							ta.Vis = p.next().Val
						case p.kw("final"):
							ta.AliasFinal = true
							p.next()
						case p.kw("static"):
							ta.AliasStatic = true
							p.next()
						case p.kw("abstract"):
							ta.AliasAbstract = true
							p.next()
						case p.is(TIdent, "readonly"):
							// Zend: readonly-модификатор к методу неприменим
							// (в т.ч. в позиции имени псевдонима).
							return nil, &fatalError{msg: "Cannot use the readonly modifier on a method", line: p.lineAt(p.cur().Pos)}
						}
						if p.is(TIdent, "") || p.is(TKeyword, "") {
							ta.AliasName = tokName(p.next())
						}
					}
					cd.TraitAdapt = append(cd.TraitAdapt, ta)
					if err := p.endStmt(); err != nil {
						return nil, err
					}
				}
				if p.is(TOp, "}") {
					p.next()
				}
				p.accept(TOp, ";")
				continue
			}
			if err := p.endStmt(); err != nil {
				return nil, err
			}
			continue
		}
		ms, err := p.classMember()
		if err != nil {
			return nil, err
		}
		cd.Members = append(cd.Members, ms...)
	}
	p.next() // }
	return &NewAnonClassExpr{Decl: cd, Args: args}, nil
}

func (p *Parser) primary() (Expr, error) {
	t := p.cur()
	switch t.Kind {
	case TInt:
		p.next()
		return &IntLit{V: t.Num}, nil
	case TFloat:
		p.next()
		return &FloatLit{V: t.Flt, Val: t.Val}, nil
	case TStr:
		p.next()
		if t.Shell {
			// `cmd` — shell_exec с интерполяцией как в двойных кавычках.
			var e Expr
			var err error
			if len(t.Parts) == 0 {
				e = &StrLit{V: t.Val}
			} else {
				e, err = p.interp(t.Parts)
				if err != nil {
					return nil, err
				}
			}
			return &CallExpr{Name: "shell_exec", Args: []Expr{e}}, nil
		}
		if len(t.Parts) == 0 {
			return &StrLit{V: t.Val}, nil
		}
		return p.interp(t.Parts)
	case TVar:
		p.next()
		if t.Val == "$" {
			e, err := p.dynAfterDollar()
			if err != nil {
				return nil, err
			}
			return &VarExpr{NameExpr: e}, nil
		}
		return &VarExpr{Name: t.Val}, nil
	case TOp:
		switch t.Val {
		case "(":
			p.next()
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect(TOp, ")"); err != nil {
				return nil, err
			}
			// Тернарник в скобках — «парентетизированный»: правое вложение
			// без скобок запрещено (см. ternaryExpr).
			if te, ok := x.(*TernaryExpr); ok {
				te.Paren = true
			}
			return x, nil
		case "[":
			return p.arrayLit()
		case "\\":
			return p.nameRefExpr()
		}
	case TKeyword:
		switch t.Val {
		case "true":
			p.next()
			return &BoolLit{V: true}, nil
		case "false":
			p.next()
			return &BoolLit{V: false}, nil
		case "null":
			p.next()
			return &NullLit{}, nil
		case "array":
			p.next()
			if p.is(TOp, "(") {
				return p.arrayParen()
			}
		case "list":
			p.next()
			p.listTargetDepth++
			e, err := p.listExpr()
			p.listTargetDepth--
			if err != nil {
				return nil, err
			}
			// list() допустим только как цель присваивания; вложенные list()
			// (внутри внешнего list()) проверяются внешним уровнем, а внутри
			// [] — компиляторным фаталом «Cannot mix [] and list()».
			if p.listTargetDepth == 0 && p.arrayLitDepth == 0 && !p.is(TOp, "=") {
				return nil, &parseError{msg: `syntax error, unexpected token ")", expecting "="`, line: p.lineAt(p.cur().Pos)}
			}
			return e, nil
		case "function":
			return p.closureExpr(false)
		case "new":
			// обрабатывается в unaryExpr; сюда не попадает
		case "namespace":
			// `namespace\Foo` — относительное имя в текущем пространстве имён.
			if next := p.peekAt(1); next.Kind == TOp && next.Val == "\\" {
				return p.nameRefExpr()
			}
		case "static":
			// может быть static function() {} / static fn() => ... /
			// static::method() / static::$prop
			if next := p.peekAt(1); next.Kind == TKeyword && next.Val == "function" {
				attrs := p.takeAttrs()
				p.next() // static
				p.moveAttrsToNext(attrs)
				return p.closureExpr(true)
			}
			if next, n2 := p.peekAt(1), p.peekAt(2); next.Kind == TIdent && next.Val == "fn" &&
				(n2.Val == "(" || (n2.Kind == TOp && n2.Val == "&" && p.peekAt(3).Val == "(")) {
				attrs := p.takeAttrs()
				p.next() // static
				p.moveAttrsToNext(attrs)
				return p.arrowFnExpr(true)
			}
			p.next()
			return &ConstExpr{Name: "static"}, nil
		}
	case TIdent:
		if t.Val == "yield" {
			return p.yieldExpr()
		}
		if t.Val == "match" && p.peekAt(1).Kind == TOp && p.peekAt(1).Val == "(" {
			return p.matchExpr()
		}
		if t.Val == "fn" && (p.peekAt(1).Val == "(" || (p.peekAt(1).Kind == TOp && p.peekAt(1).Val == "&" && p.peekAt(2).Val == "(")) {
			return p.arrowFnExpr(false)
		}
		return p.nameRefExpr()
	}
	return nil, p.syntaxErr(t)
}

// matchExpr разбирает выражение match (subject) { conds => body, ... }.
func (p *Parser) matchExpr() (Expr, error) {
	p.next() // match
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	subj, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	if err := p.expect(TOp, "{"); err != nil {
		return nil, err
	}
	me := &MatchExpr{Subject: subj}
	for !p.is(TOp, "}") {
		if p.is(TEOF, "") {
			return nil, fmt.Errorf("parser: незакрытое выражение match")
		}
		if p.accept(TOp, ",") {
			continue
		}
		armLine := p.lineAt(p.cur().Pos)
		var arm MatchArm
		if p.kw("default") {
			p.next()
			arm.Default = true
			for p.accept(TOp, ",") {
			}
		} else {
			for {
				c, err := p.expr()
				if err != nil {
					return nil, err
				}
				arm.Conds = append(arm.Conds, c)
				if p.accept(TOp, ",") {
					if p.is(TOp, "=>") {
						break
					}
					continue
				}
				break
			}
		}
		if err := p.expect(TOp, "=>"); err != nil {
			return nil, err
		}
		body, err := p.expr()
		if err != nil {
			return nil, err
		}
		arm.Body = body
		arm.Line = armLine
		me.Arms = append(me.Arms, arm)
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.expect(TOp, "}"); err != nil {
		return nil, err
	}
	return me, nil
}

// yieldExpr разбирает выражение yield: `yield`, `yield $v`, `yield $k => $v`,
// `yield from $iterable`. Вызывается после того, как `yield` уже распознан как
// текущий токен (идентификатор).
func (p *Parser) yieldExpr() (Expr, error) {
	p.next() // yield
	ye := &YieldExpr{}
	// `yield from ...`
	if p.is(TIdent, "from") {
		p.next()
		e, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		ye.From = true
		ye.Val = e
		return ye, nil
	}
	// `yield;` / `yield` в конце выражения — без значения.
	if p.yieldEnd() {
		return ye, nil
	}
	// Значение yield — на уровне присваивания: `yield $x or die` разбирается как
	// `(yield $x) or die` (yield связывает сильнее and/or/xor).
	first, err := p.assignExpr()
	if err != nil {
		return nil, err
	}
	if p.accept(TOp, "=>") {
		val, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		ye.Key = first
		ye.Val = val
		return ye, nil
	}
	ye.Val = first
	return ye, nil
}

// yieldEnd сообщает, что `yield` не имеет следующих за ним key/value
// (например, `yield;`, `yield)` и т.п.).
func (p *Parser) yieldEnd() bool {
	if p.is(TEOF, "") || p.is(TInlineHTML, "") {
		return true
	}
	if p.cur().Kind == TKeyword {
		switch p.cur().Val {
		case "and", "or", "xor", "instanceof", "as":
			return true
		}
		return false
	}
	if p.cur().Kind != TOp {
		return false
	}
	switch p.cur().Val {
	case ";", ")", ",", "]", "}", ":", "?", "=>", "->",
		"*", "/", "%", ".", "==", "===", "!=", "!==", "<>", "<", ">", "<=", ">=", "<=>",
		"&&", "||", "??", "&", "|", "^", "<<", ">>", "**",
		"=", "+=", "-=", "*=", "/=", ".=", "%=", "&=", "|=", "^=", "<<=", ">>=":
		return true
	}
	return false
}

// dynMemberName разбирает динамическое имя члена после `->`/`::`.
// Варианты: $var (значение переменной), {expr}, ${expr}, $$var.
func (p *Parser) dynMemberName() (Expr, error) {
	if p.is(TOp, "{") {
		return p.braceName()
	}
	t := p.cur()
	if t.Kind == TVar && t.Val == "$" {
		p.next()
		return p.dynAfterDollarMember()
	}
	p.next()
	return &VarExpr{Name: t.Val}, nil
}

// dynAfterDollarMember разбирает имя члена после одиночного `$`:
// `${expr}` — переменная с именем expr (её значение), `$$var` — обычная
// переменная-переменная.
func (p *Parser) dynAfterDollarMember() (Expr, error) {
	if p.is(TOp, "{") {
		e, err := p.braceName()
		if err != nil {
			return nil, err
		}
		return &VarExpr{NameExpr: e}, nil
	}
	inner, err := p.dynAfterDollar()
	if err != nil {
		return nil, err
	}
	// `$obj->$$var`: имя члена — значение ${$var} (ещё один уровень
	// разыменования к имени из dynAfterDollar).
	return &VarExpr{NameExpr: inner}, nil
}

// dynAfterDollar разбирает продолжение после одиночного `$`:
// `{expr}` (${expr}), `$var` ($$var) или ещё один `$` ($$$var).
func (p *Parser) dynAfterDollar() (Expr, error) {
	if p.is(TOp, "{") {
		return p.braceName()
	}
	if p.cur().Kind == TVar {
		t := p.cur()
		if t.Val == "$" {
			p.next()
			inner, err := p.dynAfterDollar()
			if err != nil {
				return nil, err
			}
			return &VarExpr{NameExpr: inner}, nil
		}
		p.next()
		return &VarExpr{Name: t.Val}, nil
	}
	return nil, fmt.Errorf("parser: ожидалось имя переменной после $, получено %q", p.cur().Val)
}

// braceName разбирает `{ expr }` после `->`/`::`/`$`.
func (p *Parser) braceName() (Expr, error) {
	p.next() // {
	e, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(TOp, "}"); err != nil {
		return nil, err
	}
	return e, nil
}

// asListTarget превращает массив-литерал в цель-деструктуризацию для
// `[...] = ...`; для остальных выражений возвращает исходное.
func asListTarget(l Expr) Expr {
	al, ok := l.(*ArrayLit)
	if !ok {
		return l
	}
	return arrayLitToList(al)
}

func arrayLitToList(al *ArrayLit) *ListExpr {
	list := &ListExpr{Style: listStyleShort}
	if al.Long {
		list.Style = listStyleArray
	}
	for _, el := range al.Elems {
		if el.Val == nil {
			list.Elems = append(list.Elems, ListElem{})
			continue
		}
		le := ListElem{Key: el.Key, Target: asListTarget(el.Val), ByRef: el.ByRef, Spread: el.Spread}
		list.Elems = append(list.Elems, le)
	}
	return list
}

// listExpr разбирает list($a, $b, ...) с пропусками и ключами.
func (p *Parser) listExpr() (Expr, error) {
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	list := &ListExpr{Style: listStyleList}
	for !p.is(TOp, ")") {
		if p.accept(TOp, ",") {
			list.Elems = append(list.Elems, ListElem{})
			continue
		}
		var el ListElem
		if p.accept(TOp, "&") {
			el.ByRef = true
		}
		first, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		if p.accept(TOp, "=>") {
			// list("k" => $v)
			el.Key = first
			if p.accept(TOp, "&") {
				el.ByRef = true
			}
			if !p.is(TOp, ",") && !p.is(TOp, ")") {
				t, err := p.assignExpr()
				if err != nil {
					return nil, err
				}
				el.Target = asListTarget(t)
			}
		} else {
			el.Target = asListTarget(first)
		}
		list.Elems = append(list.Elems, el)
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.expect(TOp, ")"); err != nil {
		return nil, err
	}
	return list, nil
}

func (p *Parser) arrayLit() (Expr, error) {
	if err := p.expect(TOp, "["); err != nil {
		return nil, err
	}
	// list() внутри [] — компайл-фатал «Cannot mix [] and list()» в
	// компиляторе, а не Parse error на месте list().
	p.arrayLitDepth++
	e, err := p.arrayElems("]")
	p.arrayLitDepth--
	if err != nil {
		return nil, err
	}
	if al, ok := e.(*ArrayLit); ok {
		al.Long = false
	}
	return e, nil
}

func (p *Parser) arrayParen() (Expr, error) {
	if err := p.expect(TOp, "("); err != nil {
		return nil, err
	}
	e, err := p.arrayElems(")")
	if err != nil {
		return nil, err
	}
	if al, ok := e.(*ArrayLit); ok {
		al.Long = true
	}
	return e, nil
}

func (p *Parser) arrayElems(end string) (Expr, error) {
	arr := &ArrayLit{}
	for !p.is(TOp, end) {
		if p.is(TOp, ",") {
			// пропуск элемента — только для цели-деструктуризации [$a,,$c];
			// в значении-массиве это ошибка компиляции (проверяет компилятор).
			// Zend указывает строку предыдущего элемента (или самого пропуска).
			ln := p.lineAt(p.cur().Pos)
			if n := len(arr.Elems); n > 0 && arr.Elems[n-1].Line > 0 {
				ln = arr.Elems[n-1].Line
			}
			p.next()
			arr.Elems = append(arr.Elems, ArrayElem{Line: ln})
			continue
		}
		elemLine := p.lineAt(p.cur().Pos)
		var el ArrayElem
		el.Line = elemLine
		if p.accept(TOp, "...") {
			v, err := p.expr()
			if err != nil {
				return nil, err
			}
			el.Spread = true
			el.Val = v // spread: обрабатывается компилятором
			arr.Elems = append(arr.Elems, el)
		} else {
			var first Expr
			var err error
			if p.accept(TOp, "&") {
				el.ByRef = true
			}
			first, err = p.expr()
			if err != nil {
				// Ключевое слово в позиции элемента массива: LALR-состояние
				// принимает здесь ещё и "]", поэтому Zend добавляет
				// «expecting "]"» (Zend/tests/grammar/regression_012).
				if pe, ok := err.(*parseError); ok {
					if t := p.cur(); t.Kind == TKeyword {
						return nil, p.syntaxErrExpect(t, `"]"`)
					}
					_ = pe
				}
				return nil, err
			}
			if p.accept(TOp, "=>") {
				if p.accept(TOp, "&") {
					el.ByRef = true
				}
				v, err := p.expr()
				if err != nil {
					return nil, err
				}
				el.Key = first
				el.Val = v
			} else {
				el.Val = first
			}
			arr.Elems = append(arr.Elems, el)
		}
		if !p.accept(TOp, ",") {
			break
		}
	}
	if err := p.expect(TOp, end); err != nil {
		return nil, err
	}
	return arr, nil
}
