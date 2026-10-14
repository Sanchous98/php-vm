package php

import (
	"phpvm/internal/vm"
	"strconv"
	"strings"
	"time"
)

// Файл содержит функции даты и времени.

var (
	dateLoc       = time.UTC
	dateLocName   = "UTC"
	jsonLastError = 0

	// stdClassInfo — дескриптор stdClass для объектов, создаваемых json_decode.
	stdClassInfo = vm.NewClass("stdClass", nil)

	// objKeyOrder хранит порядок ключей объектов, чтобы json_encode
	// воспроизводил порядок вставки (props — Go-map без порядка).
	objKeyOrder = map[*vm.Object][]string{}
)

// --- DATE/TIME ---

func isoWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return wd
}

func ordinalSuffix(n int) string {
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

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func daysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func writePad(b *strings.Builder, v, width int) {
	s := strconv.Itoa(v)
	for i := len(s); i < width; i++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

func swatchBeat(t time.Time) int {
	u := t.UTC()
	secs := u.Hour()*3600 + u.Minute()*60 + u.Second()
	return ((secs + 3600) % 86400) * 1000 / 86400
}

func phpDateFormat(format string, t time.Time, locName string) string {
	return vm.FormatDatePHP(format, t, locName)
}

func dateBuiltin(ctx *vm.Context, utc bool) vm.Value {
	format := arg0(ctx).ToString()
	loc := dateLoc
	name := dateLocName
	if utc {
		loc = time.UTC
		name = "UTC"
	}
	var t time.Time
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		t = time.Unix(arg(ctx, 1).ToInt(), 0).In(loc)
	} else {
		t = time.Now().In(loc)
	}
	return vm.Str(phpDateFormat(format, t, name))
}

func idateBuiltin(ctx *vm.Context) vm.Value {
	format := arg0(ctx).ToString()
	if len(format) != 1 {
		return vm.Bool(false)
	}
	loc := dateLoc
	var t time.Time
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		t = time.Unix(arg(ctx, 1).ToInt(), 0).In(loc)
	} else {
		t = time.Now().In(loc)
	}
	switch format[0] {
	case 'd', 'j':
		return vm.Int(int64(t.Day()))
	case 'N':
		return vm.Int(int64(isoWeekday(t)))
	case 'w':
		return vm.Int(int64(int(t.Weekday())))
	case 'z':
		return vm.Int(int64(t.YearDay() - 1))
	case 'W':
		_, wk := t.ISOWeek()
		return vm.Int(int64(wk))
	case 'm', 'n':
		return vm.Int(int64(int(t.Month())))
	case 't':
		return vm.Int(int64(daysInMonth(t.Year(), int(t.Month()))))
	case 'L':
		if isLeap(t.Year()) {
			return vm.Int(1)
		}
		return vm.Int(0)
	case 'o':
		iy, _ := t.ISOWeek()
		return vm.Int(int64(iy))
	case 'Y':
		return vm.Int(int64(t.Year()))
	case 'y':
		return vm.Int(int64(t.Year() % 100))
	case 'B':
		return vm.Int(int64(swatchBeat(t)))
	case 'g', 'h':
		h := t.Hour() % 12
		if h == 0 {
			h = 12
		}
		return vm.Int(int64(h))
	case 'G', 'H':
		return vm.Int(int64(t.Hour()))
	case 'i':
		return vm.Int(int64(t.Minute()))
	case 's':
		return vm.Int(int64(t.Second()))
	case 'I':
		if t.IsDST() {
			return vm.Int(1)
		}
		return vm.Int(0)
	case 'Z':
		_, off := t.Zone()
		return vm.Int(int64(off))
	case 'U':
		return vm.Int(t.Unix())
	}
	return vm.Bool(false)
}

func mktimeBuiltin(ctx *vm.Context, utc bool) vm.Value {
	loc := dateLoc
	if utc {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	h, m, s := now.Hour(), now.Minute(), now.Second()
	mo, d, y := int(now.Month()), now.Day(), now.Year()
	a := ctx.Args()
	if len(a) > 0 && !a[0].IsNull() {
		h = int(a[0].ToInt())
	}
	if len(a) > 1 && !a[1].IsNull() {
		m = int(a[1].ToInt())
	}
	if len(a) > 2 && !a[2].IsNull() {
		s = int(a[2].ToInt())
	}
	if len(a) > 3 && !a[3].IsNull() {
		mo = int(a[3].ToInt())
	}
	if len(a) > 4 && !a[4].IsNull() {
		d = int(a[4].ToInt())
	}
	if len(a) > 5 && !a[5].IsNull() {
		y = int(a[5].ToInt())
	}
	tt := time.Date(y, time.Month(mo), d, h, m, s, 0, loc)
	return vm.Int(tt.Unix())
}

func checkdateFn(ctx *vm.Context) vm.Value {
	mo := int(arg(ctx, 0).ToInt())
	day := int(arg(ctx, 1).ToInt())
	y := int(arg(ctx, 2).ToInt())
	if y < 1 || y > 32767 || mo < 1 || mo > 12 || day < 1 {
		return vm.Bool(false)
	}
	return vm.Bool(day <= daysInMonth(y, mo))
}

func getdateFn(ctx *vm.Context) vm.Value {
	loc := dateLoc
	var t time.Time
	if len(ctx.Args()) >= 1 && !arg0(ctx).IsNull() {
		t = time.Unix(arg0(ctx).ToInt(), 0).In(loc)
	} else {
		t = time.Now().In(loc)
	}
	a := vm.NewArray()
	a.SetStr("seconds", vm.MakeInt(int64(t.Second())))
	a.SetStr("minutes", vm.MakeInt(int64(t.Minute())))
	a.SetStr("hours", vm.MakeInt(int64(t.Hour())))
	a.SetStr("mday", vm.MakeInt(int64(t.Day())))
	a.SetStr("wday", vm.MakeInt(int64(int(t.Weekday()))))
	a.SetStr("mon", vm.MakeInt(int64(int(t.Month()))))
	a.SetStr("year", vm.MakeInt(int64(t.Year())))
	a.SetStr("yday", vm.MakeInt(int64(t.YearDay()-1)))
	a.SetStr("weekday", vm.MakeString(t.Format("Monday")))
	a.SetStr("month", vm.MakeString(t.Format("January")))
	a.SetInt(0, vm.MakeInt(t.Unix()))
	return vm.MakeArray(a)
}

func localtimeFn(ctx *vm.Context) vm.Value {
	assoc := len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool()
	loc := dateLoc
	var t time.Time
	if len(ctx.Args()) >= 1 && !arg0(ctx).IsNull() {
		t = time.Unix(arg0(ctx).ToInt(), 0).In(loc)
	} else {
		t = time.Now().In(loc)
	}
	isdst := 0
	if t.IsDST() {
		isdst = 1
	}
	vals := []int{t.Second(), t.Minute(), t.Hour(), t.Day(), int(t.Month()) - 1, t.Year() - 1900, int(t.Weekday()), t.YearDay() - 1, isdst}
	a := vm.NewArray()
	if assoc {
		names := []string{"tm_sec", "tm_min", "tm_hour", "tm_mday", "tm_mon", "tm_year", "tm_wday", "tm_yday", "tm_isdst"}
		for i, n := range names {
			a.SetStr(n, vm.MakeInt(int64(vals[i])))
		}
	} else {
		for _, v := range vals {
			a.Append(vm.MakeInt(int64(v)))
		}
	}
	return vm.MakeArray(a)
}

func dateDefaultTimezoneSetFn(ctx *vm.Context) vm.Value {
	name := arg0(ctx).ToString()
	loc, err := time.LoadLocation(name)
	if err != nil {
		return vm.Bool(false)
	}
	dateLoc = loc
	dateLocName = name
	vm.SetDefaultTimezone(loc, name)
	return vm.Bool(true)
}

func phpStrtotime(s string, base time.Time, loc *time.Location) (int64, bool) {
	if s == "" {
		return 0, false
	}
	low := strings.ToLower(s)
	switch low {
	case "now":
		return base.Unix(), true
	case "today", "midnight":
		return time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, loc).Unix(), true
	case "noon":
		return time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, loc).Unix(), true
	case "tomorrow":
		return time.Date(base.Year(), base.Month(), base.Day()+1, 0, 0, 0, 0, loc).Unix(), true
	case "yesterday":
		return time.Date(base.Year(), base.Month(), base.Day()-1, 0, 0, 0, 0, loc).Unix(), true
	}
	if s[0] == '@' {
		n, err := strconv.ParseInt(s[1:], 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if ts, ok := strtotimeRelative(s, base, loc); ok {
		return ts, true
	}
	if ts, ok := strtotimeAbsolute(s, base, loc); ok {
		return ts, true
	}
	return 0, false
}

func dateParseFn(ctx *vm.Context) vm.Value {
	s := strings.TrimSpace(arg0(ctx).ToString())
	a := vm.NewArray()
	setFalse := func(k string) { a.SetStr(k, vm.MakeBool(false)) }
	fields := []string{"year", "month", "day", "hour", "minute", "second", "fraction"}
	for _, f := range fields {
		setFalse(f)
	}
	// warnings/errors
	emptyArr := func() vm.Slot { return vm.MakeArray(vm.NewArray()) }

	fill := func(t time.Time, hasTime bool, frac float64, hasFrac bool) {
		a.SetStr("year", vm.MakeInt(int64(t.Year())))
		a.SetStr("month", vm.MakeInt(int64(int(t.Month()))))
		a.SetStr("day", vm.MakeInt(int64(t.Day())))
		if hasTime {
			a.SetStr("hour", vm.MakeInt(int64(t.Hour())))
			a.SetStr("minute", vm.MakeInt(int64(t.Minute())))
			a.SetStr("second", vm.MakeInt(int64(t.Second())))
			if hasFrac {
				a.SetStr("fraction", vm.MakeFloat(frac))
			} else {
				a.SetStr("fraction", vm.MakeFloat(0))
			}
		}
		a.SetStr("warning_count", vm.MakeInt(0))
		a.SetStr("warnings", emptyArr())
		a.SetStr("error_count", vm.MakeInt(0))
		a.SetStr("errors", emptyArr())
		a.SetStr("is_localtime", vm.MakeBool(false))
	}

	if s != "" && s[0] == '@' {
		if n, err := strconv.ParseInt(s[1:], 10, 64); err == nil {
			t := time.Unix(n, 0).In(dateLoc)
			fill(t, true, 0, false)
			return vm.MakeArray(a)
		}
	}

	// отделяем дробную часть
	frac := 0.0
	hasFrac := false
	base := s
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		base = s[:dot]
		if f, err := strconv.ParseFloat(s[dot:], 64); err == nil {
			frac = f
			hasFrac = true
		}
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02",
		"02 Jan 2006",
		"2 Jan 2006",
		"Jan 2 2006",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, base, dateLoc); err == nil {
			hasTime := strings.Contains(base, ":")
			fill(t, hasTime, frac, hasFrac)
			return vm.MakeArray(a)
		}
	}
	// не удалось разобрать
	a.SetStr("warning_count", vm.MakeInt(1))
	w := vm.NewArray()
	w.Append(vm.MakeString("The timezone could not be found in the database"))
	a.SetStr("warnings", vm.MakeArray(w))
	a.SetStr("error_count", vm.MakeInt(1))
	e := vm.NewArray()
	e.SetInt(0, vm.MakeString("Unexpected character"))
	a.SetStr("errors", vm.MakeArray(e))
	a.SetStr("is_localtime", vm.MakeBool(false))
	return vm.MakeArray(a)
}
