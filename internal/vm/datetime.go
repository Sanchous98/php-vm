package vm

// Builtin-классы даты/времени PHP: DateTime, DateTimeImmutable, DateTimeZone,
// DateInterval, DatePeriod и интерфейс DateTimeInterface.
//
// DateTime/DateTimeImmutable — нативные объекты (*DateTime), DateTimeZone/
// DateInterval/DatePeriod — тоже нативные (данные в собственных Go-структурах,
// встраивающих Object). Форматтер даты живёт
// здесь (FormatDatePHP) и переиспользуется date()/gmdate() из пакета php —
// так избегаем дублирования логики между пакетами.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// --- Значение часового пояса по умолчанию ---

// defaultLocation/defaultLocationName — часовой пояс, используемый DateTime,
// когда он не задан явно. Синхронизируется с date_default_timezone_set()
// через SetDefaultTimezone.
var (
	defaultLocation     = time.UTC
	defaultLocationName = "UTC"
)

// SetDefaultTimezone задаёт часовой пояс по умолчанию (вызывается пакетом php
// из date_default_timezone_set и при инициализации).
func SetDefaultTimezone(loc *time.Location, name string) {
	if loc == nil {
		return
	}
	defaultLocation = loc
	defaultLocationName = name
}

// DefaultTimezone возвращает текущий пояс по умолчанию (локацию и имя).
func DefaultTimezone() (*time.Location, string) {
	return defaultLocation, defaultLocationName
}

// --- Константы форматов (общие для DateTime/DateTimeImmutable/интерфейса) ---

var dateFormatConsts = []struct {
	name  string
	value string
}{
	{"ATOM", "Y-m-d\\TH:i:sP"},
	{"COOKIE", "l, d-M-Y H:i:s T"},
	{"ISO8601", "Y-m-d\\TH:i:sO"},
	{"RFC822", "D, d M y H:i:s O"},
	{"RFC850", "l, d-M-y H:i:s T"},
	{"RFC1036", "D, d M y H:i:s O"},
	{"RFC1123", "D, d M Y H:i:s O"},
	{"RFC7231", "D, d M Y H:i:s \\G\\M\\T"},
	{"RFC2822", "D, d M Y H:i:s O"},
	{"RFC3339", "Y-m-d\\TH:i:sP"},
	{"RFC3339_EXTENDED", "Y-m-d\\TH:i:s.vP"},
	{"RSS", "D, d M Y H:i:s O"},
	{"W3C", "Y-m-d\\TH:i:sP"},
}

func declareDateFormatConsts(ci *runtimeClass) {
	for _, c := range dateFormatConsts {
		ci.DefineConst(c.name, MakeString(c.value))
	}
}

// --- Данные объектов ---

// DateTime — нативный объект даты/времени. Шапка Object встроена по offset 0,
// данные — конкретные Go-поля. DateTimeImmutable встраивает DateTime (O(1)
// промоушен полей/методов).
type DateTime struct {
	Object
	t    time.Time
	loc  *time.Location
	name string
}

type DateTimeImmutable struct {
	DateTime
}

// dateTimeClass / dateTimeImmutableClass — тонкие нативные дескрипторы
// DateTime и DateTimeImmutable. Оборачивают runtimeClass (методы, константы,
// интерфейсы), но создают и приводят нативный *DateTime.
type dateTimeClass struct{ *runtimeClass }

type dateTimeImmutableClass struct{ *runtimeClass }

// embeddedRuntimeClass отдаёт вложенный дескриптор рефлексии (runtimeCarrier).
func (c *dateTimeClass) embeddedRuntimeClass() *runtimeClass { return c.runtimeClass }

func (c *dateTimeImmutableClass) embeddedRuntimeClass() *runtimeClass { return c.runtimeClass }

// dateTimeMarker отмечает дескрипторы DateTime/DateTimeImmutable (для
// безопасного приведения *Object к *DateTime).
type dateTimeMarker interface{ isDateTimeClass() }

func (*dateTimeClass) isDateTimeClass()          {}
func (*dateTimeImmutableClass) isDateTimeClass() {}

var (
	_ ClassInfo = (*dateTimeClass)(nil)
	_ ClassInfo = (*dateTimeImmutableClass)(nil)
)

func (c *dateTimeClass) New(ctx *Context, args []Slot) *Object {
	d := &DateTime{}
	d.class = c
	d.loc, d.name = defaultLocation, defaultLocationName
	return &d.Object
}
func (c *dateTimeClass) GetProp(o *Object, name string) (Value, bool) { return nil, false }
func (c *dateTimeClass) SetProp(o *Object, name string, v Value)      {}
func (c *dateTimeClass) cloneObject(o *Object) *Object {
	d := objectAs[DateTime](o, c)
	if d == nil {
		return nil
	}
	nd := *d
	nd.class = c
	return &nd.Object
}
func (c *dateTimeClass) isA(target ClassInfo) bool {
	if ClassInfo(c) == target {
		return true
	}
	return c.runtimeClass.isA(target)
}

// IsA — как isA (identity + предки встроенного runtimeClass).
func (c *dateTimeClass) IsA(target ClassInfo) bool { return c.isA(target) }

func (c *dateTimeImmutableClass) New(ctx *Context, args []Slot) *Object {
	d := &DateTimeImmutable{}
	d.class = c
	d.loc, d.name = defaultLocation, defaultLocationName
	return &d.Object
}
func (c *dateTimeImmutableClass) GetProp(o *Object, name string) (Value, bool) { return nil, false }
func (c *dateTimeImmutableClass) SetProp(o *Object, name string, v Value)      {}
func (c *dateTimeImmutableClass) cloneObject(o *Object) *Object {
	d := objectAs[DateTime](o, c)
	if d == nil {
		return nil
	}
	nd := &DateTimeImmutable{}
	nd.DateTime = *d
	nd.class = c
	return &nd.Object
}
func (c *dateTimeImmutableClass) isA(target ClassInfo) bool {
	if ClassInfo(c) == target {
		return true
	}
	return c.runtimeClass.isA(target)
}

// IsA — как isA (identity + предки встроенного runtimeClass).
func (c *dateTimeImmutableClass) IsA(target ClassInfo) bool { return c.isA(target) }

// dateTimeZoneData/dateIntervalData/datePeriodData — нативные данные
// DateTimeZone/DateInterval/DatePeriod. Каждый тип встраивает Object первым
// полем (как DateTime), поэтому шапка и данные разделяют один адрес.
type dateTimeZoneData struct {
	Object
	name string
	loc  *time.Location
}

type dateIntervalData struct {
	Object
	y, m, d, h, i, s int
	invert           bool
	days             int
	hasDays          bool
}

type datePeriodData struct {
	Object
	times []time.Time
	loc   *time.Location
	name  string
	idx   int
}

// --- Вспомогательные ---

func slotString(args []Slot, i int) string {
	if i < 0 || i >= len(args) {
		return ""
	}
	return args[i].ToString()
}

func slotInt(args []Slot, i int, def int64) int64 {
	if i < 0 || i >= len(args) {
		return def
	}
	if args[i].IsNull() {
		return def
	}
	return args[i].ToInt()
}

// NewDateClasses возвращает дескрипторы классов даты/времени в фиксированном
// порядке: DateTimeInterface, DateTime, DateTimeImmutable, DateTimeZone,
// DateInterval, DatePeriod.
func NewDateClasses() []ClassInfo {
	iface := NewInterface("DateTimeInterface")
	dt := &dateTimeClass{runtimeClass: newClassInfo("DateTime", nil)}
	im := &dateTimeImmutableClass{runtimeClass: newClassInfo("DateTimeImmutable", nil)}
	tz := RegisterClass[dateTimeZoneData]("DateTimeZone", nil)
	iv := RegisterClass[dateIntervalData]("DateInterval", nil)
	pr := RegisterClass[datePeriodData]("DatePeriod", nil)

	dt.Implements(iface)
	im.Implements(iface)

	registerDateTimeMembers(dt, dt.runtimeClass, false)
	registerDateTimeMembers(im, im.runtimeClass, true)
	registerDateTimeZoneMembers(tz)
	registerDateIntervalMembers(iv)
	registerDatePeriodMembers(pr)

	declareDateFormatConsts(iface)
	declareDateFormatConsts(dt.runtimeClass)
	declareDateFormatConsts(im.runtimeClass)

	return []ClassInfo{iface, dt, im, tz, iv, pr}
}

// --- DateTime / DateTimeImmutable ---

// thisDateTime извлекает нативный *DateTime из $this (owner — дескриптор,
// которому принадлежат вызываемые методы; проверка isA).
func thisDateTime(ctx *Context, owner ClassInfo) *DateTime {
	return objectAs[DateTime](ctx.ThisObj(), owner)
}

// dateResult применяет изменение: для DateTime мутирует $this, для
// DateTimeImmutable — создаёт копию и возвращает её.
func (ctx *Context) dateResult(d *DateTime, immutable bool, mutate func(*DateTime)) Slot {
	if immutable {
		nd := *d
		mutate(&nd)
		return ctx.vm.slotOf(&nd.Object)
	}
	mutate(d)
	return ctx.this
}

func registerDateTimeMembers(owner ClassInfo, ci *runtimeClass, immutable bool) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeNull(), nil
			}
			t, name, loc, perr := parseDateTimeArgs(ctx.vm, args)
			if perr != nil {
				return Slot{}, perr
			}
			d.t, d.loc, d.name = t, loc, name
			return MakeNull(), nil
		},
	}})

	// format(string $format): string
	ci.Method("format", &Function{Name: "format", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			return MakeString(FormatDatePHP(slotString(args, 0), d.t, d.name)), nil
		},
	}})

	// getTimestamp(): int
	ci.Method("getTimestamp", &Function{Name: "getTimestamp", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			return MakeInt(d.t.Unix()), nil
		},
	}})

	// setTimestamp(int $ts): $this|DateTimeImmutable
	ci.Method("setTimestamp", &Function{Name: "setTimestamp", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			nt := time.Unix(slotInt(args, 0, 0), 0).In(d.loc)
			return ctx.dateResult(d, immutable, func(x *DateTime) { x.t = nt }), nil
		},
	}})

	// modify(string $modifier): $this|DateTimeImmutable|false
	ci.Method("modify", &Function{Name: "modify", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			nt, ok := phpModify(slotString(args, 0), d.t, d.loc)
			if !ok {
				return MakeBool(false), nil
			}
			return ctx.dateResult(d, immutable, func(x *DateTime) { x.t = nt }), nil
		},
	}})

	// add(DateInterval $interval): $this|DateTimeImmutable
	ci.Method("add", &Function{Name: "add", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			iv := ctx.vm.intervalArg(args, 0)
			if d == nil || iv == nil {
				return MakeBool(false), nil
			}
			nt := iv.applyTo(d.t)
			return ctx.dateResult(d, immutable, func(x *DateTime) { x.t = nt }), nil
		},
	}})

	// sub(DateInterval $interval): $this|DateTimeImmutable
	ci.Method("sub", &Function{Name: "sub", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			iv := ctx.vm.intervalArg(args, 0)
			if d == nil || iv == nil {
				return MakeBool(false), nil
			}
			nt := iv.applyNegated(d.t)
			return ctx.dateResult(d, immutable, func(x *DateTime) { x.t = nt }), nil
		},
	}})

	// diff(DateTimeInterface $target): DateInterval
	ci.Method("diff", &Function{Name: "diff", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			od := ctx.vm.dateTimeArg(args, 0)
			if d == nil || od == nil {
				return MakeBool(false), nil
			}
			iv := diffTimes(d.t, od.t)
			return ctx.intervalSlot(iv), nil
		},
	}})

	// getTimezone(): DateTimeZone|false
	ci.Method("getTimezone", &Function{Name: "getTimezone", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			return ctx.zoneSlot(d.name, d.loc), nil
		},
	}})

	// setTimezone(DateTimeZone $tz): $this|DateTimeImmutable
	ci.Method("setTimezone", &Function{Name: "setTimezone", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			z := ctx.vm.zoneArg(args, 0)
			if d == nil || z == nil {
				return MakeBool(false), nil
			}
			nt := d.t.In(z.loc)
			return ctx.dateResult(d, immutable, func(x *DateTime) {
				x.t = nt
				x.loc, x.name = z.loc, z.name
			}), nil
		},
	}})

	// setDate(int $y, int $m, int $d): $this|DateTimeImmutable
	ci.Method("setDate", &Function{Name: "setDate", NumArgs: 3, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			y, mo, da := d.t.Date()
			if len(args) > 0 {
				y = int(slotInt(args, 0, int64(y)))
			}
			if len(args) > 1 {
				mo = time.Month(slotInt(args, 1, int64(mo)))
			}
			if len(args) > 2 {
				da = int(slotInt(args, 2, int64(da)))
			}
			nt := time.Date(y, mo, da, d.t.Hour(), d.t.Minute(), d.t.Second(), d.t.Nanosecond(), d.loc)
			return ctx.dateResult(d, immutable, func(x *DateTime) { x.t = nt }), nil
		},
	}})

	// setTime(int $h, int $i, int $s = 0, int $us = 0): $this|DateTimeImmutable
	ci.Method("setTime", &Function{Name: "setTime", NumArgs: 3, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := thisDateTime(ctx, owner)
			if d == nil {
				return MakeBool(false), nil
			}
			y, mo, da := d.t.Date()
			h := int(slotInt(args, 0, 0))
			mi := int(slotInt(args, 1, 0))
			se := int(slotInt(args, 2, 0))
			ns := 0
			if len(args) > 3 && !args[3].IsNull() {
				ns = int(slotInt(args, 3, 0)) * 1000
			}
			nt := time.Date(y, mo, da, h, mi, se, ns, d.loc)
			return ctx.dateResult(d, immutable, func(x *DateTime) { x.t = nt }), nil
		},
	}})
}

func (ctx *Context) zoneSlot(name string, loc *time.Location) Slot {
	z := ctx.vm.findClass("DateTimeZone")
	if z == nil {
		return MakeNull()
	}
	return nativeObject(ctx.vm, z, &dateTimeZoneData{name: name, loc: loc})
}

func (ctx *Context) intervalSlot(iv *dateIntervalData) Slot {
	c := ctx.vm.findClass("DateInterval")
	if c == nil {
		return MakeNull()
	}
	return nativeObject(ctx.vm, c, iv)
}

// DateDiff реализует date_diff(DateTimeInterface $base, DateTimeInterface $target):
// интервал между двумя нативными датами (для встроенной функции date_diff).
func (vm *VM) DateDiff(a, b Slot) (Slot, error) {
	da := vm.dateTimeArg([]Slot{a}, 0)
	db := vm.dateTimeArg([]Slot{b}, 0)
	if da == nil {
		return Slot{}, NewThrowable("TypeError", "date_diff(): Argument #1 ($baseObject) must be of type DateTimeInterface")
	}
	if db == nil {
		return Slot{}, NewThrowable("TypeError", "date_diff(): Argument #2 ($targetObject) must be of type DateTimeInterface")
	}
	return vm.ctx.intervalSlot(diffTimes(da.t, db.t)), nil
}

func (vm *VM) intervalArg(args []Slot, i int) *dateIntervalData {
	if i < 0 || i >= len(args) {
		return nil
	}
	s := deref(args[i])
	if s.typ != ObjectType {
		return nil
	}
	o := vm.objectOf(s)
	if o == nil {
		return nil
	}
	return objectAs[dateIntervalData](o, vm.findClass("DateInterval"))
}

// dateTimeArg извлекает нативный *DateTime из аргумента (для diff и т.п.).
func (vm *VM) dateTimeArg(args []Slot, i int) *DateTime {
	if i < 0 || i >= len(args) {
		return nil
	}
	s := deref(args[i])
	if s.typ != ObjectType {
		return nil
	}
	o := vm.objectOf(s)
	if o == nil {
		return nil
	}
	if _, ok := o.class.(dateTimeMarker); !ok {
		return nil
	}
	return (*DateTime)(unsafe.Pointer(o))
}

func (vm *VM) zoneArg(args []Slot, i int) *dateTimeZoneData {
	if i < 0 || i >= len(args) {
		return nil
	}
	s := deref(args[i])
	if s.typ != ObjectType {
		return nil
	}
	o := vm.objectOf(s)
	if o == nil {
		return nil
	}
	return objectAs[dateTimeZoneData](o, vm.findClass("DateTimeZone"))
}

// --- Конструктор DateTime: разбор аргументов ---

func parseDateTimeArgs(vm *VM, args []Slot) (time.Time, string, *time.Location, error) {
	loc, name := defaultLocation, defaultLocationName
	if len(args) > 1 {
		if z := vm.zoneArg(args, 1); z != nil {
			loc, name = z.loc, z.name
		}
	}
	s := "now"
	if len(args) > 0 && !args[0].IsNull() {
		s = args[0].ToString()
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = "now"
	}
	if strings.HasPrefix(s, "@") {
		if n, err := strconv.ParseInt(s[1:], 10, 64); err == nil {
			return time.Unix(n, 0).UTC(), "+00:00", time.UTC, nil
		}
	}
	if msg, bad := isoDateRangeError(s); bad {
		return time.Time{}, name, loc, NewThrowable("Exception", msg)
	}
	if t, ok := parseDateString(s, time.Now().In(loc), loc); ok {
		return t, name, loc, nil
	}
	return time.Now().In(loc), name, loc, nil
}

// isoDateRe — ISO-дата (возможно, с временем), для которой Zend проверяет
// диапазоны месяца и дня отдельной диагностикой.
var isoDateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[ T].*)?$`)

// isoDateRangeError повторяет диагностику Zend для ISO-строк с месяцем > 12
// или днём > 31 (bug54043: «Failed to parse time string (9999-11-33) at
// position 9 (3): Unexpected character»).
func isoDateRangeError(s string) (string, bool) {
	m := isoDateRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	bad := false
	pos := -1
	if mm, err := strconv.Atoi(m[2]); err == nil && mm > 12 {
		pos = 6
		bad = true
	}
	if dd, err := strconv.Atoi(m[3]); err == nil && dd > 31 {
		pos = 9
		bad = true
	}
	if !bad || pos >= len(s) {
		return "", false
	}
	return fmt.Sprintf("Failed to parse time string (%s) at position %d (%c): Unexpected character",
		s, pos, s[pos]), true
}

// parseDateString разбирает абсолютную или относительную дату.
func parseDateString(s string, base time.Time, loc *time.Location) (time.Time, bool) {
	low := strings.ToLower(s)
	switch low {
	case "now":
		return base, true
	case "today", "midnight":
		return time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, loc), true
	case "noon":
		return time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, loc), true
	case "tomorrow":
		return time.Date(base.Year(), base.Month(), base.Day()+1, 0, 0, 0, 0, loc), true
	case "yesterday":
		return time.Date(base.Year(), base.Month(), base.Day()-1, 0, 0, 0, 0, loc), true
	}
	// Абсолютные форматы.
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02",
		"02 Jan 2006",
		"2 Jan 2006",
		"02 January 2006",
		"2 January 2006",
		"Jan 2 2006",
		"January 2 2006",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, true
		}
	}
	for _, l := range []string{"15:04:05", "15:04"} {
		if hms, err := time.ParseInLocation(l, s, loc); err == nil {
			return time.Date(base.Year(), base.Month(), base.Day(), hms.Hour(), hms.Minute(), hms.Second(), 0, loc), true
		}
	}
	// Относительные модификаторы: цепочка «+1 day -3 hours ...»
	if t, ok := phpModify(s, base, loc); ok {
		return t, true
	}
	return base, false
}

// phpModify применяет к base относительные модификаторы вида "+1 day",
// "-2 hours", "next monday", "last day of ...". Возвращает false, если строка
// не распознана.
func phpModify(s string, base time.Time, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return base, false
	}
	low := strings.ToLower(s)
	switch low {
	case "now":
		return base, true
	case "today", "midnight":
		return time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, loc), true
	case "noon":
		return time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, loc), true
	case "tomorrow":
		return time.Date(base.Year(), base.Month(), base.Day()+1, 0, 0, 0, 0, loc), true
	case "yesterday":
		return time.Date(base.Year(), base.Month(), base.Day()-1, 0, 0, 0, 0, loc), true
	}
	if strings.HasPrefix(low, "next ") {
		if t, ok := nextWeekday(strings.TrimSpace(low[5:]), base, loc, 1); ok {
			return t, true
		}
	}
	if strings.HasPrefix(low, "last ") {
		if t, ok := nextWeekday(strings.TrimSpace(low[5:]), base, loc, -1); ok {
			return t, true
		}
	}
	// Цепочка числовых модификаторов.
	cur := base
	applied := false
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			break
		}
		sign := 1
		if s[i] == '+' {
			i++
		} else if s[i] == '-' {
			sign = -1
			i++
		} else if s[i] < '0' || s[i] > '9' {
			return base, applied
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i {
			return base, applied
		}
		n, _ := strconv.Atoi(s[i:j])
		i = j
		for i < len(s) && s[i] == ' ' {
			i++
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '+' && s[i] != '-' {
			i++
		}
		unit := strings.ToLower(strings.TrimSuffix(s[start:i], "s"))
		switch unit {
		case "second", "sec":
			cur = cur.Add(time.Duration(sign*n) * time.Second)
		case "minute", "min":
			cur = cur.Add(time.Duration(sign*n) * time.Minute)
		case "hour":
			cur = cur.Add(time.Duration(sign*n) * time.Hour)
		case "day":
			cur = cur.AddDate(0, 0, sign*n)
		case "week":
			cur = cur.AddDate(0, 0, 7*sign*n)
		case "month":
			cur = cur.AddDate(0, sign*n, 0)
		case "year":
			cur = cur.AddDate(sign*n, 0, 0)
		default:
			return base, applied
		}
		applied = true
	}
	return cur, applied
}

// nextWeekday возвращает ближайший день недели wd (dir=1 вперёд, -1 назад).
func nextWeekday(name string, base time.Time, loc *time.Location, dir int) (time.Time, bool) {
	names := map[string]time.Weekday{
		"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
		"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
		"sunday": time.Sunday,
	}
	wd, ok := names[strings.ToLower(name)]
	if !ok {
		return base, false
	}
	t := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, loc)
	diff := (int(wd) - int(t.Weekday()) + 7) % 7
	if dir < 0 {
		diff = -((int(t.Weekday()) - int(wd) + 7) % 7)
		if diff == 0 {
			diff = -7
		}
	} else if diff == 0 {
		diff = 7
	}
	return t.AddDate(0, 0, diff), true
}

// --- DateInterval ---

func (d *dateIntervalData) applyTo(t time.Time) time.Time {
	sign := 1
	if d.invert {
		sign = -1
	}
	t = t.AddDate(sign*d.y, sign*d.m, sign*d.d)
	t = t.Add(time.Duration(sign*d.h)*time.Hour +
		time.Duration(sign*d.i)*time.Minute +
		time.Duration(sign*d.s)*time.Second)
	return t
}

func (d *dateIntervalData) applyNegated(t time.Time) time.Time {
	cp := *d
	cp.invert = !cp.invert
	return cp.applyTo(t)
}

func registerDateIntervalMembers(ci *runtimeClass) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[dateIntervalData]()
			if d == nil {
				return MakeNull(), nil
			}
			if len(args) == 0 {
				return MakeNull(), nil
			}
			parsed, ok := parseISODuration(slotString(args, 0))
			if !ok {
				return MakeNull(), nil
			}
			// Сохраняем встроенную шапку Object (class/props): parsed её не
			// содержит и затирать её нельзя.
			hdr := d.Object
			*d = *parsed
			d.Object = hdr
			return MakeNull(), nil
		},
	}})

	ci.Method("format", &Function{Name: "format", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[dateIntervalData]()
			if d == nil {
				return MakeBool(false), nil
			}
			return MakeString(d.format(slotString(args, 0))), nil
		},
	}})

	// Свойства y/m/d/h/i/s/invert/days.
	ci.Declare("y", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			return MakeInt(int64(d.y))
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("m", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			return MakeInt(int64(d.m))
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("d", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			return MakeInt(int64(d.d))
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("h", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			return MakeInt(int64(d.h))
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("i", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			return MakeInt(int64(d.i))
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("s", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			return MakeInt(int64(d.s))
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("invert", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil {
			if d.invert {
				return MakeInt(1)
			}
			return MakeInt(0)
		}
		return MakeInt(0)
	}, nil)
	ci.Declare("days", func(o *Object) Value {
		if d := objectAs[dateIntervalData](o, nil); d != nil && d.hasDays {
			return MakeInt(int64(d.days))
		}
		return MakeBool(false)
	}, nil)
}

func (d *dateIntervalData) format(f string) string {
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			b.WriteByte(f[i])
			continue
		}
		i++
		if i >= len(f) {
			b.WriteByte('%')
			break
		}
		switch f[i] {
		case 'y':
			b.WriteString(strconv.Itoa(d.y))
		case 'm':
			b.WriteString(strconv.Itoa(d.m))
		case 'd':
			b.WriteString(strconv.Itoa(d.d))
		case 'h':
			b.WriteString(strconv.Itoa(d.h))
		case 'i':
			b.WriteString(strconv.Itoa(d.i))
		case 's':
			b.WriteString(strconv.Itoa(d.s))
		case 'a':
			if d.hasDays {
				b.WriteString(strconv.Itoa(d.days))
			} else {
				b.WriteString("-1")
			}
		case 'R':
			if d.invert {
				b.WriteByte('-')
			} else {
				b.WriteByte('+')
			}
		case 'r':
			if d.invert {
				b.WriteByte('-')
			}
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(f[i])
		}
	}
	return b.String()
}

// parseISODuration разбирает ISO 8601-длительность вида P1Y2M3DT4H5M6S.
func parseISODuration(s string) (*dateIntervalData, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != 'P' && s[0] != 'p') {
		return nil, false
	}
	d := &dateIntervalData{}
	i := 1
	inTime := false
	sawAny := false
	for i < len(s) {
		if s[i] == 'T' || s[i] == 't' {
			inTime = true
			i++
			continue
		}
		neg := false
		if s[i] == '+' || s[i] == '-' {
			neg = s[i] == '-'
			i++
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i {
			return nil, false
		}
		n, _ := strconv.Atoi(s[i:j])
		if neg {
			n = -n
		}
		i = j
		if i >= len(s) {
			return nil, false
		}
		unit := s[i]
		i++
		switch unit {
		case 'Y', 'y':
			d.y += n
		case 'M', 'm':
			if inTime {
				d.i += n
			} else {
				d.m += n
			}
		case 'W', 'w':
			d.d += n * 7
		case 'D', 'd':
			d.d += n
		case 'H', 'h':
			d.h += n
		case 'S', 's':
			d.s += n
		default:
			return nil, false
		}
		sawAny = true
	}
	if !sawAny {
		return nil, false
	}
	return d, true
}

// diffTimes вычисляет DateInterval между двумя моментами.
func diffTimes(a, b time.Time) *dateIntervalData {
	invert := false
	if a.After(b) {
		a, b = b, a
		invert = true
	}
	y, m, dd := 0, 0, 0
	for {
		if !a.AddDate(y+1, 0, 0).After(b) {
			y++
		} else {
			break
		}
	}
	cur := a.AddDate(y, 0, 0)
	for {
		if !cur.AddDate(0, m+1, 0).After(b) {
			m++
		} else {
			break
		}
	}
	cur = cur.AddDate(0, m, 0)
	for {
		if !cur.AddDate(0, 0, dd+1).After(b) {
			dd++
		} else {
			break
		}
	}
	cur = cur.AddDate(0, 0, dd)
	rem := b.Sub(cur)
	if rem < 0 {
		rem = 0
	}
	h := int(rem / time.Hour)
	rem -= time.Duration(h) * time.Hour
	mi := int(rem / time.Minute)
	rem -= time.Duration(mi) * time.Minute
	se := int(rem / time.Second)
	totalDays := int(b.Sub(a).Hours() / 24)
	return &dateIntervalData{
		y: y, m: m, d: dd, h: h, i: mi, s: se,
		invert: invert, days: totalDays, hasDays: true,
	}
}

// --- DateTimeZone ---

func registerDateTimeZoneMembers(ci *runtimeClass) {
	// Константы регионов (как в PHP).
	ci.DefineConst("AFRICA", MakeInt(1))
	ci.DefineConst("AMERICA", MakeInt(2))
	ci.DefineConst("ANTARCTICA", MakeInt(4))
	ci.DefineConst("ARCTIC", MakeInt(8))
	ci.DefineConst("ASIA", MakeInt(16))
	ci.DefineConst("ATLANTIC", MakeInt(32))
	ci.DefineConst("AUSTRALIA", MakeInt(64))
	ci.DefineConst("EUROPE", MakeInt(128))
	ci.DefineConst("INDIAN", MakeInt(256))
	ci.DefineConst("PACIFIC", MakeInt(512))
	ci.DefineConst("UTC", MakeInt(1024))
	ci.DefineConst("ALL", MakeInt(2047))
	ci.DefineConst("ALL_WITH_BC", MakeInt(4095))
	ci.DefineConst("PER_COUNTRY", MakeInt(4096))

	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[dateTimeZoneData]()
			if d == nil {
				return MakeNull(), nil
			}
			name := slotString(args, 0)
			loc, err := loadTimezone(name)
			if err != nil {
				return MakeNull(), nil
			}
			d.name = name
			d.loc = loc
			return MakeNull(), nil
		},
	}})

	ci.Method("getName", &Function{Name: "getName", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[dateTimeZoneData]()
			if d == nil {
				return MakeBool(false), nil
			}
			return MakeString(d.name), nil
		},
	}})

	ci.Method("getOffset", &Function{Name: "getOffset", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[dateTimeZoneData]()
			dt := ctx.vm.dateTimeArg(args, 0)
			if d == nil || dt == nil {
				return MakeBool(false), nil
			}
			_, off := dt.t.In(d.loc).Zone()
			return MakeInt(int64(off)), nil
		},
	}})
}

// loadTimezone разбирает имя пояса или смещение вида "+02:00".
func loadTimezone(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "UTC") {
		return time.UTC, nil
	}
	if name[0] == '+' || name[0] == '-' {
		if off, ok := parseOffset(name); ok {
			return time.FixedZone(name, off), nil
		}
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	return loc, nil
}

// parseOffset разбирает "+HH:MM" / "-HHMM" / "+HH".
func parseOffset(s string) (int, bool) {
	if len(s) < 3 {
		return 0, false
	}
	sign := 1
	if s[0] == '-' {
		sign = -1
	} else if s[0] != '+' {
		return 0, false
	}
	body := s[1:]
	body = strings.Replace(body, ":", "", 1)
	if len(body) != 2 && len(body) != 4 {
		return 0, false
	}
	h, err := strconv.Atoi(body[:2])
	if err != nil {
		return 0, false
	}
	m := 0
	if len(body) == 4 {
		m, err = strconv.Atoi(body[2:])
		if err != nil {
			return 0, false
		}
	}
	return sign * (h*3600 + m*60), true
}

// --- DatePeriod ---

func registerDatePeriodMembers(ci *runtimeClass) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[datePeriodData]()
			if d == nil {
				return MakeNull(), nil
			}
			start := ctx.vm.dateTimeArg(args, 0)
			iv := ctx.vm.intervalArg(args, 1)
			if start == nil || iv == nil {
				return MakeNull(), nil
			}
			d.loc, d.name = start.loc, start.name
			cur := start.t
			if len(args) > 2 {
				if end := ctx.vm.dateTimeArg(args, 2); end != nil {
					// Конец исключающий.
					for guard := 0; cur.Before(end.t) && guard < 100000; guard++ {
						d.times = append(d.times, cur)
						cur = iv.applyTo(cur)
					}
					return MakeNull(), nil
				}
				if n := slotInt(args, 2, 0); n > 0 {
					// n — число повторов после старта (всего n+1 моментов).
					count := int(n) + 1
					for k := 0; k < count && k < 100000; k++ {
						d.times = append(d.times, cur)
						cur = iv.applyTo(cur)
					}
				}
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("rewind", &Function{Name: "rewind", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[datePeriodData](); d != nil {
				d.idx = 0
			}
			return MakeNull(), nil
		},
	}})
	ci.Method("valid", &Function{Name: "valid", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[datePeriodData]()
			return MakeBool(d != nil && d.idx < len(d.times)), nil
		},
	}})
	ci.Method("current", &Function{Name: "current", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[datePeriodData]()
			if d == nil || d.idx >= len(d.times) {
				return MakeBool(false), nil
			}
			c := ctx.vm.findClass("DateTime")
			if c == nil {
				return MakeBool(false), nil
			}
			dt := &DateTime{}
			dt.class = c
			dt.t, dt.loc, dt.name = d.times[d.idx], d.loc, d.name
			return ctx.vm.slotOf(&dt.Object), nil
		},
	}})
	ci.Method("key", &Function{Name: "key", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[datePeriodData]()
			if d == nil {
				return MakeNull(), nil
			}
			return MakeInt(int64(d.idx)), nil
		},
	}})
	ci.Method("next", &Function{Name: "next", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[datePeriodData](); d != nil {
				d.idx++
			}
			return MakeNull(), nil
		},
	}})
}

// --- Форматтер даты (общий для date()/gmdate()/DateTime::format) ---

func dateIsoWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return wd
}

func dateOrdinalSuffix(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

func dateIsLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func dateDaysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func dateWritePad(b *strings.Builder, v, width int) {
	s := strconv.Itoa(v)
	for i := len(s); i < width; i++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

func dateSwatchBeat(t time.Time) int {
	u := t.UTC()
	secs := u.Hour()*3600 + u.Minute()*60 + u.Second()
	return ((secs + 3600) % 86400) * 1000 / 86400
}

// FormatDatePHP форматирует момент t по PHP-формату (date()/DateTime::format).
func FormatDatePHP(format string, t time.Time, locName string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c == '\\' {
			i++
			if i < len(format) {
				b.WriteByte(format[i])
			}
			continue
		}
		switch c {
		case 'd':
			b.WriteString(t.Format("02"))
		case 'D':
			b.WriteString(t.Format("Mon"))
		case 'j':
			b.WriteString(strconv.Itoa(t.Day()))
		case 'l':
			b.WriteString(t.Format("Monday"))
		case 'N':
			b.WriteString(strconv.Itoa(dateIsoWeekday(t)))
		case 'S':
			b.WriteString(dateOrdinalSuffix(t.Day()))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'z':
			b.WriteString(strconv.Itoa(t.YearDay() - 1))
		case 'W':
			_, wk := t.ISOWeek()
			dateWritePad(&b, wk, 2)
		case 'F':
			b.WriteString(t.Format("January"))
		case 'm':
			b.WriteString(t.Format("01"))
		case 'M':
			b.WriteString(t.Format("Jan"))
		case 'n':
			b.WriteString(strconv.Itoa(int(t.Month())))
		case 't':
			b.WriteString(strconv.Itoa(dateDaysInMonth(t.Year(), int(t.Month()))))
		case 'L':
			if dateIsLeap(t.Year()) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		case 'o':
			iy, _ := t.ISOWeek()
			b.WriteString(strconv.Itoa(iy))
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			dateWritePad(&b, t.Year()%100, 2)
		case 'a':
			if t.Hour() < 12 {
				b.WriteString("am")
			} else {
				b.WriteString("pm")
			}
		case 'A':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'g':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(strconv.Itoa(h))
		case 'G':
			b.WriteString(strconv.Itoa(t.Hour()))
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			dateWritePad(&b, h, 2)
		case 'H':
			b.WriteString(t.Format("15"))
		case 'i':
			b.WriteString(t.Format("04"))
		case 's':
			b.WriteString(t.Format("05"))
		case 'u':
			dateWritePad(&b, t.Nanosecond()/1000, 6)
		case 'v':
			dateWritePad(&b, t.Nanosecond()/1000000, 3)
		case 'e':
			b.WriteString(locName)
		case 'I':
			if t.IsDST() {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		case 'O':
			b.WriteString(t.Format("-0700"))
		case 'P':
			b.WriteString(t.Format("-07:00"))
		case 'T':
			b.WriteString(t.Format("MST"))
		case 'Z':
			_, off := t.Zone()
			b.WriteString(strconv.Itoa(off))
		case 'c':
			b.WriteString(FormatDatePHP("Y-m-d\\TH:i:sP", t, locName))
		case 'r':
			b.WriteString(FormatDatePHP("D, d M Y H:i:s O", t, locName))
		case 'U':
			b.WriteString(strconv.FormatInt(t.Unix(), 10))
		case 'B':
			dateWritePad(&b, dateSwatchBeat(t), 3)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
