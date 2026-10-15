package php

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"phpvm/internal/vm"
	"strconv"
	"strings"
	"time"
)

// builtins — встроенные функции, доступные компилируемому коду.
// Реестр оставлен в этом файле; тела именованных функций разнесены по builtins_<domain>.go.

// phpSAPIName/phpinfoAsText — эмуляция SAPI харнессом: run-tests.php запускает
// тесты с секциями --CGI--/--GET--/--POST--/--COOKIE--/--POST_RAW-- через
// php-cgi (phpinfo_as_text=0, имя "cgi-fcgi"), остальные — через CLI.
var (
	phpSAPIName   = "cli"
	phpinfoAsText = true
)

// SetSAPIEmulation переключает SAPI-режим для очередного .phpt.
func SetSAPIEmulation(name string, asText bool) {
	phpSAPIName = name
	phpinfoAsText = asText
}

// issetThisFn — isset($this)/empty($this): в объектном контексте true, иначе
// false (без выброса "Using $this when not in object context").
func issetThisFn(ctx *vm.Context) vm.Value {
	return vm.Bool(derefSlot(ctx.ThisSlot()).Type() == vm.ObjectType)
}

func builtins() []*vm.Function {
	// Внутренняя кодировка mbstring — состояние уровня запроса: сбрасываем
	// при каждой компиляции (builtins вызывается один раз на программу).
	mbInternalEncoding = "UTF-8"
	strtokState = ""
	strtokPos = 0
	// Состояние assert()/assert_options() — тоже сбрасывается на запрос.
	assertActiveOpt = 1
	assertWarningOpt = 1
	assertBailOpt = 0
	assertExceptionOpt = 1
	assertCallbackSlot = vm.MakeNull()
	// Состояние error_get_last()/error_clear_last().
	lastErrorArray = nil
	// Каталог по умолчанию для readdir()/rewinddir()/closedir() без аргумента.
	fsLastDir = nil
	// Состояние генератора MT19937 (mt_srand/mt_rand/rand).
	mtReset()
	// Пользовательские константы (для get_defined_constants()).
	userConstNames = userConstNames[:0]
	userConstSet = map[string]bool{}
	fs := []*vm.Function{
		vm.Register("var_dump", varDumpFn),
		vm.Register("var_export", varExportFn),
		vm.Register("print_r", printRFn),
		vm.Register("count", countFn),
		vm.Register("sizeof", countFn),
		vm.Register("gettype", gettypeFn),
		vm.Register("get_debug_type", getDebugTypeFn),
		vm.Register("is_int", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.IntType) }),
		vm.Register("is_integer", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.IntType) }),
		vm.Register("is_long", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.IntType) }),
		vm.Register("is_float", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.FloatType) }),
		vm.Register("is_double", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.FloatType) }),
		vm.Register("is_string", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.StringType) }),
		vm.Register("is_bool", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.BoolType) }),
		vm.Register("is_null", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.NullType) }),
		vm.Register("is_array", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.ArrayType) }),
		vm.Register("is_object", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).Type() == vm.ObjectType) }),
		vm.Register("is_numeric", isNumericFn),
		vm.Register("is_callable", isCallableFn),
		vm.Register("get_class", func(ctx *vm.Context) vm.Value {
			if len(ctx.Args()) == 0 {
				// get_class() без аргумента: класс текущего scope (устаревшее);
				// вне класса — Error (без deprecation).
				if ci := ctx.Self(); ci != nil {
					ctx.VM().Deprecated("Calling get_class() without arguments is deprecated")
					return vm.Str(ci.Name())
				}
				ctx.Throw(vm.NewThrowable("Error", "get_class() without arguments must be called from within a class"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			s := derefSlot(ctx.Args()[0])
			if s.IsClosure() {
				return vm.Str("Closure")
			}
			if s.Type() != vm.ObjectType {
				ctx.Throw(vm.NewThrowable("TypeError",
					"get_class(): Argument #1 ($object) must be of type object, "+reflectTypeName(s)+" given"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			o := ctx.Arg[*vm.Object](0)
			if o == nil {
				return vm.Bool(false)
			}
			return vm.Str(o.ClassName())
		}),
		vm.Register("get_class_methods", getClassMethodsFn),
		vm.Register("get_object_vars", getObjectVarsFn),
		vm.Register("get_class_vars", getClassVarsFn),
		vm.Register("class_exists", classExistsFn),
		vm.Register("spl_autoload_register", splAutoloadRegisterFn),
		vm.Register("spl_autoload_unregister", splAutoloadUnregisterFn),
		vm.Register("spl_autoload_functions", splAutoloadFunctionsFn),
		vm.Register("spl_autoload_call", splAutoloadCallFn),
		vm.Register("class_alias", classAliasFn),
		vm.Register("interface_exists", interfaceExistsFn),
		vm.Register("trait_exists", traitExistsFn),
		vm.Register("enum_exists", enumExistsFn),
		vm.Register("method_exists", methodExistsFn),
		vm.Register("property_exists", propertyExistsFn),
		vm.Register("is_a", isAFn),
		vm.Register("is_subclass_of", isSubclassOfFn),
		vm.Register("get_parent_class", getParentClassFn),
		vm.Register("get_called_class", getCalledClassFn),
		vm.Register("get_declared_classes", getDeclaredClassesFn),
		vm.Register("get_declared_interfaces", getDeclaredInterfacesFn),
		vm.Register("get_declared_traits", getDeclaredTraitsFn),
		vm.Register("class_implements", classImplementsFn),
		vm.Register("class_parents", classParentsFn),
		vm.Register("class_uses", classUsesFn),

		vm.Register("intval", intvalFn),
		vm.Register("floatval", floatvalFn),
		vm.Register("doubleval", floatvalFn),
		vm.Register("strval", func(ctx *vm.Context) vm.Value {
			out, err := ctx.VM().ToStringValue(arg0(ctx))
			if err != nil {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Str(out)
		}),
		vm.Register("chr", func(ctx *vm.Context) vm.Value {
			if len(ctx.Args()) != 1 {
				ctx.Throw(vm.NewThrowable("TypeError", fmt.Sprintf("chr() expects exactly 1 argument, %d given", len(ctx.Args()))))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			v := arg0(ctx)
			if v.Type() == vm.FloatType || v.Type() == vm.StringType {
				ctx.VM().FloatToIntWarn(v)
			}
			return vm.Str(string([]byte{byte(v.ToInt())}))
		}),
		vm.Register("boolval", func(ctx *vm.Context) vm.Value { return vm.Bool(arg0(ctx).ToBool()) }),

		vm.Register("strlen", strlenFn),
		vm.Register("str_repeat", func(ctx *vm.Context) vm.Value {
			s := arg(ctx, 0).ToString()
			n := int(arg(ctx, 1).ToInt())
			if n < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "str_repeat(): Argument #2 ($times) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if n <= 0 || s == "" {
				return vm.Str("")
			}
			if len(s) > 0 && n > (1<<31)/len(s) {
				ctx.Throw(vm.NewThrowable("ValueError", "str_repeat(): Result is too long"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Str(strings.Repeat(s, n))
		}),
		vm.Register("strtoupper", func(ctx *vm.Context) vm.Value { return vm.Str(asciiUpper(arg0(ctx).ToString())) }),
		vm.Register("strtolower", func(ctx *vm.Context) vm.Value { return vm.Str(asciiLower(arg0(ctx).ToString())) }),
		vm.Register("ucfirst", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx).ToString()
			if s == "" {
				return vm.Str("")
			}
			return vm.Str(asciiUpper(s[:1]) + s[1:])
		}),
		vm.Register("lcfirst", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx).ToString()
			if s == "" {
				return vm.Str("")
			}
			return vm.Str(asciiLower(s[:1]) + s[1:])
		}),
		vm.Register("ucwords", ucwordsFn),
		vm.Register("strrev", func(ctx *vm.Context) vm.Value {
			b := []byte(arg0(ctx).ToString())
			for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
				b[i], b[j] = b[j], b[i]
			}
			return vm.Str(string(b))
		}),
		vm.Register("trim", func(ctx *vm.Context) vm.Value {
			return vm.Str(trimString(ctx, "trim", true, true))
		}),
		vm.Register("ltrim", func(ctx *vm.Context) vm.Value {
			return vm.Str(trimString(ctx, "ltrim", true, false))
		}),
		vm.Register("rtrim", func(ctx *vm.Context) vm.Value {
			return vm.Str(trimString(ctx, "rtrim", false, true))
		}),
		vm.Register("chop", func(ctx *vm.Context) vm.Value {
			return vm.Str(trimString(ctx, "rtrim", false, true))
		}),
		vm.Register("substr", substrFn),
		vm.Register("strpos", func(ctx *vm.Context) vm.Value { return strposFn(ctx, false, false) }),
		vm.Register("stripos", func(ctx *vm.Context) vm.Value { return strposFn(ctx, true, false) }),
		vm.Register("strrpos", func(ctx *vm.Context) vm.Value { return strposFn(ctx, false, true) }),
		vm.Register("strripos", func(ctx *vm.Context) vm.Value { return strposFn(ctx, true, true) }),
		vm.Register("strstr", func(ctx *vm.Context) vm.Value {
			return strstrFn(ctx, false)
		}),
		vm.Register("stristr", func(ctx *vm.Context) vm.Value {
			return strstrFn(ctx, true)
		}),
		vm.Register("strchr", func(ctx *vm.Context) vm.Value {
			return strstrFn(ctx, false)
		}),
		vm.Register("str_replace", strReplaceFn),
		vm.Register("str_contains", func(ctx *vm.Context) vm.Value {
			return vm.Bool(strings.Contains(arg(ctx, 0).ToString(), arg(ctx, 1).ToString()))
		}),
		vm.Register("str_starts_with", func(ctx *vm.Context) vm.Value {
			return vm.Bool(strings.HasPrefix(arg(ctx, 0).ToString(), arg(ctx, 1).ToString()))
		}),
		vm.Register("str_ends_with", func(ctx *vm.Context) vm.Value {
			return vm.Bool(strings.HasSuffix(arg(ctx, 0).ToString(), arg(ctx, 1).ToString()))
		}),
		vm.Register("ord", func(ctx *vm.Context) vm.Value {
			if ctx.VM().StrictTypes() && arg(ctx, 0).Type() != vm.StringType {
				ctx.Throw(vm.NewThrowable("TypeError",
					vm.ArgTypeError("ord", 1, "character", "string", phpTypeName(ctx, 0), "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Int(0)
			}
			s := arg0(ctx).ToString()
			if s == "" {
				return vm.Int(0)
			}
			return vm.Int(int64(s[0]))
		}),
		vm.Register("sprintf", sprintfFn),
		vm.Register("vsprintf", vsprintfFn),
		vm.Register("vprintf", vprintfFn),
		vm.Register("printf", func(ctx *vm.Context) vm.Value {
			if len(ctx.Args()) == 0 {
				ctx.Throw(vm.NewThrowable("ArgumentCountError", "printf() expects at least 1 argument, 0 given"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if t := arg(ctx, 0).Type(); t == vm.ArrayType || t == vm.ResourceType || (t == vm.ObjectType && !ctx.VM().IsStringable(arg(ctx, 0))) {
				ctx.Throw(vm.NewThrowable("TypeError",
					vm.ArgTypeError("printf", 1, "format", "string", phpTypeName(ctx, 0), "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			s := sprintfValue(ctx, ctx.Args())
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			ctx.Print(s)
			return vm.Int(int64(len(s)))
		}),

		vm.Register("implode", func(ctx *vm.Context) vm.Value { return vm.Str(implodeFn(ctx)) }),
		vm.Register("join", func(ctx *vm.Context) vm.Value { return vm.Str(joinFn(ctx)) }),
		vm.Register("explode", explodeFn),

		vm.Register("abs", absFn),
		vm.Register("ceil", func(ctx *vm.Context) vm.Value { return floatUnary(ctx, "ceil", "num", math.Ceil) }),
		vm.Register("floor", func(ctx *vm.Context) vm.Value { return floatUnary(ctx, "floor", "num", math.Floor) }),
		vm.Register("round", roundFn),
		vm.Register("sqrt", func(ctx *vm.Context) vm.Value { return floatUnary(ctx, "sqrt", "num", math.Sqrt) }),
		vm.Register("pow", powFn),
		vm.Register("intdiv", func(ctx *vm.Context) vm.Value {
			b := arg(ctx, 1).ToInt()
			if b == 0 {
				ctx.Throw(vm.NewThrowable("DivisionByZeroError", "Division by zero"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			a := arg(ctx, 0).ToInt()
			if a == math.MinInt64 && b == -1 {
				ctx.Throw(vm.NewThrowable("ArithmeticError", "Division of PHP_INT_MIN by -1 is not an integer"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Int(a / b)
		}),
		vm.Register("fmod", func(ctx *vm.Context) vm.Value {
			return vm.Float(math.Mod(arg(ctx, 0).ToFloat(), arg(ctx, 1).ToFloat()))
		}),
		vm.Register("max", func(ctx *vm.Context) vm.Value { return minMax(ctx, false) }),
		vm.Register("min", func(ctx *vm.Context) vm.Value { return minMax(ctx, true) }),
		vm.Register("dechex", func(ctx *vm.Context) vm.Value {
			return vm.Str(strconv.FormatInt(intParam(ctx, 0, "dechex", "num"), 16))
		}),
		vm.Register("decoct", func(ctx *vm.Context) vm.Value {
			return vm.Str(strconv.FormatInt(intParam(ctx, 0, "decoct", "num"), 8))
		}),
		vm.Register("decbin", func(ctx *vm.Context) vm.Value {
			return vm.Str(strconv.FormatInt(intParam(ctx, 0, "decbin", "num"), 2))
		}),
		vm.Register("hexdec", func(ctx *vm.Context) vm.Value {
			str, ok := strParam(ctx, 0, "hexdec", "hex_string")
			if !ok {
				return *new(vm.Value)
			}
			return baseToZval(ctx, str, 16)
		}),
		vm.Register("octdec", func(ctx *vm.Context) vm.Value {
			str, ok := strParam(ctx, 0, "octdec", "octal_string")
			if !ok {
				return *new(vm.Value)
			}
			return baseToZval(ctx, str, 8)
		}),
		vm.Register("bindec", func(ctx *vm.Context) vm.Value {
			str, ok := strParam(ctx, 0, "bindec", "binary_string")
			if !ok {
				return *new(vm.Value)
			}
			return baseToZval(ctx, str, 2)
		}),

		// математика
		vm.Register("pi", func(ctx *vm.Context) vm.Value { return vm.Float(math.Pi) }),
		vm.Register("deg2rad", func(ctx *vm.Context) vm.Value { return vm.Float(arg0(ctx).ToFloat() * math.Pi / 180) }),
		vm.Register("rad2deg", func(ctx *vm.Context) vm.Value { return vm.Float(arg0(ctx).ToFloat() * 180 / math.Pi) }),
		vm.Register("log", logFn),
		vm.Register("log10", func(ctx *vm.Context) vm.Value { return vm.Float(math.Log10(arg0(ctx).ToFloat())) }),
		vm.Register("log2", func(ctx *vm.Context) vm.Value { return vm.Float(math.Log2(arg0(ctx).ToFloat())) }),
		vm.Register("log1p", func(ctx *vm.Context) vm.Value { return vm.Float(math.Log1p(arg0(ctx).ToFloat())) }),
		vm.Register("exp", func(ctx *vm.Context) vm.Value { return vm.Float(math.Exp(arg0(ctx).ToFloat())) }),
		vm.Register("expm1", func(ctx *vm.Context) vm.Value { return vm.Float(math.Expm1(arg0(ctx).ToFloat())) }),
		vm.Register("sin", func(ctx *vm.Context) vm.Value { return vm.Float(math.Sin(arg0(ctx).ToFloat())) }),
		vm.Register("cos", func(ctx *vm.Context) vm.Value { return vm.Float(math.Cos(arg0(ctx).ToFloat())) }),
		vm.Register("tan", func(ctx *vm.Context) vm.Value { return vm.Float(math.Tan(arg0(ctx).ToFloat())) }),
		vm.Register("asin", func(ctx *vm.Context) vm.Value { return vm.Float(math.Asin(arg0(ctx).ToFloat())) }),
		vm.Register("acos", func(ctx *vm.Context) vm.Value { return vm.Float(math.Acos(arg0(ctx).ToFloat())) }),
		vm.Register("atan", func(ctx *vm.Context) vm.Value { return vm.Float(math.Atan(arg0(ctx).ToFloat())) }),
		vm.Register("atan2", func(ctx *vm.Context) vm.Value {
			return vm.Float(math.Atan2(arg(ctx, 0).ToFloat(), arg(ctx, 1).ToFloat()))
		}),
		vm.Register("sinh", func(ctx *vm.Context) vm.Value { return vm.Float(math.Sinh(arg0(ctx).ToFloat())) }),
		vm.Register("cosh", func(ctx *vm.Context) vm.Value { return vm.Float(math.Cosh(arg0(ctx).ToFloat())) }),
		vm.Register("tanh", func(ctx *vm.Context) vm.Value { return vm.Float(math.Tanh(arg0(ctx).ToFloat())) }),
		vm.Register("asinh", func(ctx *vm.Context) vm.Value { return vm.Float(math.Asinh(arg0(ctx).ToFloat())) }),
		vm.Register("acosh", func(ctx *vm.Context) vm.Value { return vm.Float(math.Acosh(arg0(ctx).ToFloat())) }),
		vm.Register("atanh", func(ctx *vm.Context) vm.Value { return vm.Float(math.Atanh(arg0(ctx).ToFloat())) }),
		vm.Register("hypot", func(ctx *vm.Context) vm.Value {
			return vm.Float(math.Hypot(arg(ctx, 0).ToFloat(), arg(ctx, 1).ToFloat()))
		}),
		vm.Register("fdiv", func(ctx *vm.Context) vm.Value {
			return vm.Float(arg(ctx, 0).ToFloat() / arg(ctx, 1).ToFloat())
		}),
		vm.Register("is_finite", func(ctx *vm.Context) vm.Value {
			f := arg0(ctx).ToFloat()
			return vm.Bool(!math.IsInf(f, 0) && !math.IsNaN(f))
		}),
		vm.Register("is_infinite", func(ctx *vm.Context) vm.Value { return vm.Bool(math.IsInf(arg0(ctx).ToFloat(), 0)) }),
		vm.Register("is_nan", func(ctx *vm.Context) vm.Value { return vm.Bool(math.IsNaN(arg0(ctx).ToFloat())) }),
		vm.Register("base_convert", baseConvertFn),
		vm.Register("getrandmax", mtGetrandmaxFn),
		vm.Register("set_time_limit", func(ctx *vm.Context) vm.Value { return vm.Bool(true) }),

		vm.Register("array_keys", arrayKeysFn),
		vm.Register("array_values", arrayValuesFn),
		vm.Register("array_sum", arraySumFn),
		vm.Register("array_product", arrayProductFn),
		vm.Register("array_reverse", arrayReverseFn),
		vm.Register("array_flip", arrayFlipFn),
		vm.Register("array_key_exists", arrayKeyExistsFn),
		vm.Register("key_exists", arrayKeyExistsFn),
		vm.Register("array_search", arraySearchFn),
		vm.Register("in_array", inArrayFn),
		vm.Register("array_fill", arrayFillFn),
		vm.Register("shuffle", shuffleFn),
		vm.Register("array_rand", arrayRandFn),
		vm.Register("range", rangeFn),
		vm.Register("array_merge", arrayMergeFn),
		vm.Register("array_merge_recursive", arrayMergeRecursiveFn),
		vm.Register("array_combine", arrayCombineFn),
		vm.Register("array_slice", arraySliceFn),
		vm.Register("array_unique", arrayUniqueFn),
		vm.Register("array_pad", arrayPadFn),
		vm.Register("array_diff", arrayDiffFn),
		vm.Register("array_diff_assoc", arrayDiffAssocFn),
		vm.Register("array_diff_key", arrayDiffKeyFn),
		vm.Register("array_intersect", arrayIntersectFn),
		vm.Register("array_intersect_assoc", arrayIntersectAssocFn),
		vm.Register("array_intersect_key", arrayIntersectKeyFn),
		vm.Register("array_key_first", arrayKeyFirstFn),
		vm.Register("array_key_last", arrayKeyLastFn),
		vm.Register("array_is_list", arrayIsListFn),
		vm.Register("array_count_values", arrayCountValuesFn),
		vm.Register("array_chunk", arrayChunkFn),
		vm.Register("array_fill_keys", arrayFillKeysFn),
		vm.Register("array_column", arrayColumnFn),
		vm.Register("array_replace", arrayReplaceFn),
		vm.Register("array_replace_recursive", arrayReplaceRecursiveFn),
		vm.Register("array_change_key_case", arrayChangeKeyCaseFn),

		// внутренний указатель массива (current/key — чтение; остальные by-ref)
		vm.Register("key", arrayKeyPtrFn),
		vm.Register("current", arrayCurrentFn),
		vm.Register("next", arrayNextFn),
		vm.Register("prev", arrayPrevFn),
		vm.Register("reset", arrayResetFn),
		vm.Register("end", arrayEndFn),
		vm.Register("each", arrayEachFn),

		// мутирующие array-функции (by-ref первый аргумент)
		vm.Register("array_shift", arrayShiftFn),
		vm.Register("array_pop", arrayPopFn),
		vm.Register("array_push", arrayPushFn),
		vm.Register("array_unshift", arrayUnshiftFn),
		vm.Register("array_splice", arraySpliceFn),
		vm.Register("natsort", natSortFn),
		vm.Register("natcasesort", natCaseSortFn),
		vm.Register("array_multisort", arrayMultisortFn),

		// callable-версии diff/intersect (последний/-ие аргументы — callable)
		vm.Register("array_udiff", arrayUdiffFn),
		vm.Register("array_udiff_assoc", arrayUdiffAssocFn),
		vm.Register("array_udiff_uassoc", arrayUdiffUassocFn),
		vm.Register("array_diff_uassoc", arrayDiffUassocFn),
		vm.Register("array_diff_ukey", arrayDiffUkeyFn),
		vm.Register("array_uintersect", arrayUintersectFn),
		vm.Register("array_uintersect_assoc", arrayUintersectAssocFn),
		vm.Register("array_uintersect_uassoc", arrayUintersectUassocFn),
		vm.Register("array_intersect_uassoc", arrayIntersectUassocFn),
		vm.Register("array_intersect_ukey", arrayIntersectUkeyFn),

		// callable-функции (CallCallable)
		vm.Register("call_user_func", callUserFuncFn),
		vm.Register("call_user_func_array", callUserFuncArrayFn),
		vm.Register("forward_static_call", forwardStaticCallFn),
		vm.Register("forward_static_call_array", forwardStaticCallArrayFn),
		vm.Register("array_map", arrayMapFn),
		vm.Register("array_filter", arrayFilterFn),
		vm.Register("array_reduce", arrayReduceFn),

		// сортировка, мутирующая массив (by-ref)
		vm.Register("sort", sortFn),
		vm.Register("rsort", rsortFn),
		vm.Register("asort", asortFn),
		vm.Register("arsort", arsortFn),
		vm.Register("ksort", ksortFn),
		vm.Register("krsort", krsortFn),
		vm.Register("usort", usortFn),
		vm.Register("uasort", uasortFn),
		vm.Register("uksort", uksortFn),
		vm.Register("array_walk", arrayWalkFn),
		vm.Register("array_walk_recursive", arrayWalkRecursiveFn),

		// константы и рефлексия
		vm.Register("define", defineFn),
		vm.Register("defined", definedFn),
		vm.Register("constant", constantFn),
		vm.Register("__php_const_lookup", constLookupFn),
		vm.Register("__const_expr", constExprConstantFn),
		vm.Register("function_exists", func(ctx *vm.Context) vm.Value {
			return vm.Bool(ctx.VMInstance().HasFunction(strings.TrimPrefix(arg0(ctx).ToString(), "\\")))
		}),
		vm.Register("phpversion", func(ctx *vm.Context) vm.Value { return vm.Str("8.3.0") }),
		vm.Register("zend_version", func(ctx *vm.Context) vm.Value { return vm.Str("4.3.0") }),
		vm.Register("php_sapi_name", func(ctx *vm.Context) vm.Value { return vm.Str(phpSAPIName) }),
		vm.Register("version_compare", versionCompareFn),

		// INI / окружение / заголовки
		vm.Register("ini_get", iniGetFn),
		vm.Register("ini_set", iniSetFn),
		vm.Register("ini_alter", iniSetFn),
		vm.Register("ini_get_all", iniGetAllFn),
		vm.Register("ini_parse_quantity", iniParseQuantityFn),
		vm.Register("get_cfg_var", getCfgVarFn),
		vm.Register("getenv", getenvFn),
		vm.Register("putenv", putenvFn),
		vm.Register("error_reporting", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("header", headerFn),
		vm.Register("setcookie", setcookieFn),
		vm.Register("setrawcookie", setrawcookieFn),
		vm.Register("header_remove", func(ctx *vm.Context) vm.Value { return vm.MakeNull() }),
		vm.Register("headers_sent", func(ctx *vm.Context) vm.Value {
			sent, file, line := ctx.VM().HeadersSent()
			if len(ctx.Args()) >= 1 {
				if s := arg(ctx, 0); s.IsRef() && s.Ref() != nil {
					s.Ref().Set(vm.MakeString(file))
				}
			}
			if len(ctx.Args()) >= 2 {
				if s := arg(ctx, 1); s.IsRef() && s.Ref() != nil {
					s.Ref().Set(vm.MakeInt(int64(line)))
				}
			}
			return vm.Bool(sent)
		}),
		vm.Register("set_include_path", func(ctx *vm.Context) vm.Value {
			old, ok := iniValues["include_path"]
			if !ok {
				old = "."
			}
			p := arg0(ctx).ToString()
			if p == "" {
				return vm.Bool(false)
			}
			iniValues["include_path"] = p
			fsIncludePath = p
			return vm.Str(old)
		}),
		vm.Register("get_include_path", func(ctx *vm.Context) vm.Value {
			if v, ok := iniValues["include_path"]; ok {
				return vm.Str(v)
			}
			return vm.Str(".")
		}),
		vm.Register("get_included_files", getIncludedFilesFn),
		vm.Register("get_required_files", getIncludedFilesFn),
		vm.Register("get_mangled_object_vars", getMangledObjectVarsFn),
		vm.Register("spl_object_id", splObjectIdFn),
		vm.Register("spl_object_hash", splObjectHashFn),
		vm.Register("stream_context_create", streamContextCreateFn),
		vm.Register("stream_context_get_options", streamContextGetOptionsFn),
		vm.Register("stream_context_set_option", streamContextSetOptionFn),
		vm.Register("stream_context_get_default", streamContextGetDefaultFn),
		vm.Register("stream_context_set_options", streamContextSetOptionsFn),
		vm.Register("stream_context_get_params", streamContextGetParamsFn),
		vm.Register("stream_context_set_params", streamContextSetParamsFn),
		vm.Register("stream_context_set_default", streamContextSetDefaultFn),
		vm.Register("get_headers", getHeadersFn),
		vm.Register("stream_get_transports", streamGetTransportsFn),
		vm.Register("set_file_buffer", setFileBufferFn),
		vm.Register("extension_loaded", extensionLoadedFn),
		vm.Register("get_loaded_extensions", getLoadedExtensionsFn),
		vm.Register("get_extension_funcs", getExtensionFuncsFn),
		vm.Register("dl", func(ctx *vm.Context) vm.Value { return vm.Bool(false) }),

		// буферизация вывода
		vm.Register("ob_start", obStartFn),
		vm.Register("ob_end_clean", func(ctx *vm.Context) vm.Value { return vm.Bool(ctx.VM().ObEndClean()) }),
		vm.Register("ob_end_flush", func(ctx *vm.Context) vm.Value { return vm.Bool(ctx.VM().ObEndFlush()) }),
		vm.Register("ob_flush", func(ctx *vm.Context) vm.Value { return vm.Bool(ctx.VM().ObFlush()) }),
		vm.Register("ob_clean", func(ctx *vm.Context) vm.Value { return vm.Bool(ctx.VM().ObClean()) }),
		vm.Register("ob_get_clean", func(ctx *vm.Context) vm.Value {
			s, ok := ctx.VM().ObGetClean()
			return obStringOrFalse(s, ok)
		}),
		vm.Register("ob_get_flush", func(ctx *vm.Context) vm.Value {
			s, ok := ctx.VM().ObGetFlush()
			return obStringOrFalse(s, ok)
		}),
		vm.Register("ob_get_contents", func(ctx *vm.Context) vm.Value {
			s, ok := ctx.VM().ObGetContents()
			return obStringOrFalse(s, ok)
		}),
		vm.Register("ob_get_length", func(ctx *vm.Context) vm.Value {
			n, ok := ctx.VM().ObGetLength()
			if !ok {
				return vm.Bool(false)
			}
			return vm.Int(int64(n))
		}),
		vm.Register("ob_get_level", func(ctx *vm.Context) vm.Value { return vm.Int(int64(ctx.VM().ObGetLevel())) }),
		vm.Register("ob_implicit_flush", func(ctx *vm.Context) vm.Value {
			on := true
			if len(ctx.Args()) >= 1 {
				on = arg0(ctx).ToBool()
			}
			ctx.VM().ObImplicitFlush(on)
			return vm.MakeNull()
		}),
		vm.Register("ob_list_handlers", obListHandlersFn),
		vm.Register("ob_get_status", obGetStatusFn),
		// ob_gzhandler: при Accept-Encoding с gzip отдаём gzip-поток (ob_018),
		// иначе — вывод без сжатия (PHP в этом случае возвращает false).
		vm.Register("ob_gzhandler", func(ctx *vm.Context) vm.Value {
			data := arg0(ctx).ToString()
			ae := strings.ToLower(os.Getenv("HTTP_ACCEPT_ENCODING"))
			if !strings.Contains(ae, "gzip") {
				return vm.Str(data)
			}
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			_, _ = zw.Write([]byte(data))
			_ = zw.Close()
			return vm.MakeString(buf.String())
		}),
		vm.Register("flush", func(ctx *vm.Context) vm.Value { return vm.MakeNull() }),
		vm.Register("set_error_handler", func(ctx *vm.Context) vm.Value {
			cb := vm.MakeNull()
			if len(ctx.Args()) > 0 {
				cb = ctx.Args()[0]
			}
			return ctx.VM().SetErrorHandler(cb)
		}),
		vm.Register("restore_error_handler", func(ctx *vm.Context) vm.Value {
			ctx.VM().SetErrorHandler(vm.MakeNull())
			return vm.MakeNull()
		}),
		vm.Register("set_exception_handler", func(ctx *vm.Context) vm.Value {
			cb := vm.MakeNull()
			if len(ctx.Args()) > 0 {
				cb = ctx.Args()[0]
			}
			return ctx.VM().SetExceptionHandler(cb)
		}),
		vm.Register("restore_exception_handler", func(ctx *vm.Context) vm.Value {
			ctx.VM().SetExceptionHandler(vm.MakeNull())
			return vm.MakeNull()
		}),
		vm.Register("trigger_error", triggerErrorFn),
		vm.Register("user_error", triggerErrorFn),
		vm.Register("error_log", errorLogFn),
		vm.Register("memory_get_usage", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("memory_get_peak_usage", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("gc_mem_caches", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("gc_enable", gcEnableFn),
		vm.Register("gc_disable", gcDisableFn),
		vm.Register("gc_enabled", gcEnabledFn),
		vm.Register("gc_status", gcStatusFn),

		// Внешние команды — заглушки (CLI-совместимый «пустой» результат,
		// чтобы компиляция/исполнение не падали).
		vm.Register("shell_exec", shellExecFn),
		vm.Register("exec", execFn),
		vm.Register("system", systemFn),
		vm.Register("passthru", func(ctx *vm.Context) vm.Value { return vm.MakeNull() }),
		vm.Register("escapeshellcmd", escapeShellCmdFn),
		vm.Register("escapeshellarg", escapeShellArgFn),
		vm.Register("proc_open", procOpenFn),
		vm.Register("proc_close", procCloseFn),
		vm.Register("proc_get_status", procGetStatusFn),
		vm.Register("proc_terminate", procTerminateFn),
		vm.Register("popen", popenFn),
		vm.Register("pclose", pcloseFn),
		vm.Register("getopt", func(ctx *vm.Context) vm.Value { return vm.MakeArray(vm.NewArray()) }),
		vm.Register("phpinfo", phpinfoFn),
		vm.Register("get_browser", getBrowserFn),
		vm.Register("connection_aborted", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("connection_status", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("ignore_user_abort", ignoreUserAbortFn),
		vm.Register("register_shutdown_function", registerShutdownFunctionFn),
		vm.Register("register_tick_function", registerTickFunctionFn),
		vm.Register("unregister_tick_function", unregisterTickFunctionFn),
		vm.Register("session_save_path", func(ctx *vm.Context) vm.Value { return vm.Str("") }),
		vm.Register("session_start", func(ctx *vm.Context) vm.Value {
			// Сессия не поддерживается: как Zend при пустом хранилище —
			// $_SESSION становится пустым массивом (unset_cv05/06).
			ctx.VM().SetSuperglobalSynced("$_SESSION", vm.MakeArray(vm.NewArray()))
			// trans-sid: SID добавляется сканером (session-канал) только при
			// use_trans_sid && !use_only_cookies.
			if iniBoolInt(iniValues["session.use_trans_sid"]) != 0 &&
				iniBoolInt(iniValues["session.use_only_cookies"]) == 0 {
				urlRewriterActivate(ctx, true)
				name := iniValues["session.name"]
				if name == "" {
					name = "PHPSESSID"
				}
				id := sessionIDValue
				if id == "" {
					id = phpSessionGenerateID()
				}
				urlRewriteAddVar(&urlRewriteSess, name, id)
			}
			return vm.Bool(true)
		}),
		vm.Register("session_unset", func(ctx *vm.Context) vm.Value {
			ctx.VM().SetSuperglobalSynced("$_SESSION", vm.MakeArray(vm.NewArray()))
			return vm.MakeNull()
		}),
		vm.Register("get_resources", func(ctx *vm.Context) vm.Value {
			// get_resources(?string $type = null): активные ресурсы (id => resource).
			filter := ""
			if len(ctx.Args()) > 0 {
				if a0 := vm.Deref(arg(ctx, 0)); a0.Type() != vm.NullType {
					filter = a0.ToString()
				}
			}
			arr := vm.NewArray()
			for id, h := range fsHandles {
				if h == nil || h.res == nil || h.res.Closed || h.resSlot.Type() == vm.NullType {
					continue
				}
				if filter != "" && h.res.Kind != filter {
					continue
				}
				arr.Set(vm.MakeIntKey(id), h.resSlot)
			}
			return vm.MakeArray(arr)
		}),
		vm.Register("output_add_rewrite_var", func(ctx *vm.Context) vm.Value {
			// Переменная url_rewriter'а: активирует собственный буфер-сканер
			// (URL-Rewriter) и добавляет url-приложение/hidden-поле.
			name := arg0(ctx).ToString()
			value := arg(ctx, 1).ToString()
			urlRewriterActivate(ctx, false)
			urlRewriteAddVar(&urlRewriteOut, name, value)
			return vm.Bool(true)
		}),
		vm.Register("output_reset_rewrite_vars", func(ctx *vm.Context) vm.Value {
			urlRewriteOut.urlApp = ""
			urlRewriteOut.formApp = ""
			return vm.Bool(true)
		}),
		vm.Register("__php_url_rewriter_out", urlRewriterOutFn),
		vm.Register("__php_url_rewriter_sess", urlRewriterSessFn),
		vm.Register("session_commit", func(ctx *vm.Context) vm.Value {
			urlRewriterDeactivate(ctx, true)
			return vm.Bool(true)
		}),
		vm.Register("session_write_close", func(ctx *vm.Context) vm.Value {
			urlRewriterDeactivate(ctx, true)
			return vm.Bool(true)
		}),
		vm.Register("header_register_callback", headerRegisterCallbackFn),
		vm.Register("phpcredits", phpcreditsFn),
		vm.Register("uniqid", uniqidFn),
		vm.Register("debug_zval_dump", debugZvalDumpFn),
		vm.Register("is_scalar", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx)
			switch s.Type() {
			case vm.IntType:
				// Дескрипторы файлов — ресурсы, не скаляры.
				return vm.Bool(fsHandles[s.Int()] == nil)
			case vm.FloatType, vm.StringType, vm.BoolType:
				return vm.Bool(true)
			}
			return vm.Bool(false)
		}),
		vm.Register("func_get_arg", func(ctx *vm.Context) vm.Value {
			m := ctx.VM()
			if len(ctx.Args()) < 1 {
				ctx.Throw(vm.NewThrowable("ArgumentCountError", "func_get_arg() expects exactly 1 argument, 0 given"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if !m.InFunction() {
				if m.InDynamicCall() {
					ctx.Throw(vm.NewThrowable("Error", "Cannot call func_get_arg() dynamically"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
				ctx.Throw(vm.NewThrowable("Error", "func_get_arg() cannot be called from the global scope"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			pos := int(arg0(ctx).ToInt())
			if pos < 0 {
				ctx.Throw(vm.NewThrowable("Error", "func_get_arg(): Argument #1 ($position) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			v, ok := m.FuncArg(pos)
			if !ok {
				ctx.Throw(vm.NewThrowable("Error", "func_get_arg(): Argument #1 ($position) must be less than the number of the arguments passed to the currently executed function"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return v
		}),
		vm.Register("func_get_args", func(ctx *vm.Context) vm.Value {
			m := ctx.VM()
			if !m.InFunction() {
				if m.InDynamicCall() {
					ctx.Throw(vm.NewThrowable("Error", "Cannot call func_get_args() dynamically"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
				ctx.Throw(vm.NewThrowable("Error", "func_get_args() cannot be called from the global scope"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			out := vm.NewArray()
			for i, n := 0, m.FuncArgCount(); i < n; i++ {
				if v, ok := m.FuncArg(i); ok {
					_, _ = out.Append(v)
				}
			}
			return vm.MakeArray(out)
		}),
		vm.Register("func_num_args", func(ctx *vm.Context) vm.Value {
			m := ctx.VM()
			if !m.InFunction() {
				if m.InDynamicCall() {
					ctx.Throw(vm.NewThrowable("Error", "Cannot call func_num_args() dynamically"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
				ctx.Throw(vm.NewThrowable("Error", "func_num_args() must be called from a function context"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Int(int64(m.FuncArgCount()))
		}),
		vm.Register("gc_collect_cycles", gcCollectCyclesFn),
		vm.Register("usleep", func(ctx *vm.Context) vm.Value {
			n := arg0(ctx).ToInt()
			if n < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "usleep(): Argument #1 ($microseconds) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			time.Sleep(time.Duration(n) * time.Microsecond)
			return vm.MakeNull()
		}),
		vm.Register("sleep", func(ctx *vm.Context) vm.Value {
			n := arg0(ctx).ToInt()
			if n < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "sleep(): Argument #1 ($seconds) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if n > 0 {
				time.Sleep(time.Duration(n) * time.Second)
			}
			return vm.Int(0)
		}),
		vm.Register("time", func(ctx *vm.Context) vm.Value { return vm.Int(time.Now().Unix()) }),
		vm.Register("time_nanosleep", func(ctx *vm.Context) vm.Value {
			sec := arg0(ctx).ToInt()
			nsec := int64(0)
			if len(ctx.Args()) >= 2 {
				nsec = arg(ctx, 1).ToInt()
			}
			if sec < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "time_nanosleep(): Argument #1 ($seconds) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			if nsec < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "time_nanosleep(): Argument #2 ($nanoseconds) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.Bool(false)
			}
			if d := time.Duration(sec)*time.Second + time.Duration(nsec); d > 0 {
				time.Sleep(d)
			}
			return vm.Bool(true)
		}),
		vm.Register("microtime", func(ctx *vm.Context) vm.Value {
			now := time.Now()
			asFloat := len(ctx.Args()) >= 1 && arg0(ctx).ToBool()
			if asFloat {
				return vm.Float(float64(now.UnixNano()) / 1e9)
			}
			frac := float64(now.UnixNano()%int64(time.Second)) / 1e9
			return vm.Str(strconv.FormatFloat(frac, 'f', 8, 64) + " " + strconv.FormatInt(now.Unix(), 10))
		}),
		vm.Register("mt_srand", mtSrandFn),
		vm.Register("srand", srandFn),
		vm.Register("mt_rand", mtRandFn),
		vm.Register("rand", randFn),
		vm.Register("mt_getrandmax", mtGetrandmaxFn),
		vm.Register("random_int", randomIntFn),
		vm.Register("random_bytes", randomBytesFn),
		vm.Register("lcg_value", lcgValueFn),

		// строки/числа
		vm.Register("number_format", numberFormatFn),
		vm.Register("str_pad", strPadFn),
		vm.Register("str_split", strSplitFn),
		vm.Register("str_shuffle", func(ctx *vm.Context) vm.Value {
			b := []byte(arg0(ctx).ToString())
			phpShuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
			return vm.Str(string(b))
		}),
		vm.Register("strtok", strtokFn),
		vm.Register("str_word_count", strWordCountFn),
		vm.Register("nl2br", nl2brFn),
		vm.Register("addslashes", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx).ToString()
			var b strings.Builder
			b.Grow(len(s))
			for i := 0; i < len(s); i++ {
				switch s[i] {
				case '\'':
					b.WriteString("\\'")
				case '"':
					b.WriteString("\\\"")
				case '\\':
					b.WriteString("\\\\")
				case 0:
					b.WriteString("\\0")
				default:
					b.WriteByte(s[i])
				}
			}
			return vm.Str(b.String())
		}),
		vm.Register("stripslashes", stripslashesFn),

		// строковые функции (сравнение, поиск, замена)
		// strcmp возвращает разность первых различающихся байт (как C strcmp),
		// strcasecmp/strncmp — знак (-1/0/1).
		vm.Register("strcmp", func(ctx *vm.Context) vm.Value {
			return vm.Int(phpStrCmpRaw(arg(ctx, 0).ToString(), arg(ctx, 1).ToString()))
		}),
		vm.Register("strcasecmp", func(ctx *vm.Context) vm.Value {
			return vm.Int(phpStrCmpRaw(asciiLower(arg(ctx, 0).ToString()), asciiLower(arg(ctx, 1).ToString())))
		}),
		vm.Register("strncmp", func(ctx *vm.Context) vm.Value {
			n := int(arg(ctx, 2).ToInt())
			if n < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "strncmp(): Argument #3 ($length) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Int(phpStrCmpRaw(clipLen(arg(ctx, 0).ToString(), n), clipLen(arg(ctx, 1).ToString(), n)))
		}),
		vm.Register("strncasecmp", func(ctx *vm.Context) vm.Value {
			n := int(arg(ctx, 2).ToInt())
			if n < 0 {
				ctx.Throw(vm.NewThrowable("ValueError", "strncasecmp(): Argument #3 ($length) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Int(phpStrCmpRaw(clipLen(asciiLower(arg(ctx, 0).ToString()), n), clipLen(asciiLower(arg(ctx, 1).ToString()), n)))
		}),
		vm.Register("strrchr", func(ctx *vm.Context) vm.Value {
			h := arg(ctx, 0).ToString()
			n := arg(ctx, 1).ToString()
			if n == "" {
				// пустая игла трактуется как NUL-байт (как в php_strrchr)
				n = "\x00"
			}
			i := strings.LastIndex(h, n[:1])
			if i < 0 {
				return vm.Bool(false)
			}
			if len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool() {
				return vm.Str(h[:i])
			}
			return vm.Str(h[i:])
		}),
		vm.Register("substr_count", substrCountFn),
		vm.Register("str_ireplace", strIReplaceFn),
		vm.Register("str_rot13", func(ctx *vm.Context) vm.Value { return vm.Str(strRot13(arg0(ctx).ToString())) }),
		vm.Register("wordwrap", wordwrapFn),
		vm.Register("chunk_split", chunkSplitFn),
		vm.Register("substr_replace", substrReplaceFn),
		vm.Register("substr_compare", substrCompareFn),
		vm.Register("strtr", strtrFn),
		vm.Register("strnatcmp", func(ctx *vm.Context) vm.Value {
			return vm.Int(strNatCmp(arg(ctx, 0).ToString(), arg(ctx, 1).ToString(), false))
		}),
		vm.Register("strnatcasecmp", func(ctx *vm.Context) vm.Value {
			return vm.Int(strNatCmp(arg(ctx, 0).ToString(), arg(ctx, 1).ToString(), true))
		}),
		vm.Register("strcspn", strcspnFn),
		vm.Register("strspn", strspnFn),
		vm.Register("similar_text", similarTextFn),
		vm.Register("levenshtein", levenshteinFn),
		vm.Register("soundex", func(ctx *vm.Context) vm.Value { return vm.Str(soundex(arg0(ctx).ToString())) }),
		vm.Register("quotemeta", func(ctx *vm.Context) vm.Value { return vm.Str(quotemeta(arg0(ctx).ToString())) }),
		vm.Register("addcslashes", addcslashesFn),
		vm.Register("stripcslashes", stripcslashesFn),
		vm.Register("strip_tags", stripTagsFn),
		vm.Register("highlight_string", highlightStringFn),
		vm.Register("highlight_file", highlightFileFn),
		vm.Register("show_source", highlightFileFn),
		vm.Register("php_strip_whitespace", phpStripWhitespaceFn),
		vm.Register("htmlspecialchars", htmlspecialcharsFn),
		vm.Register("htmlentities", htmlentitiesFn),
		vm.Register("html_entity_decode", htmlEntityDecodeFn),
		vm.Register("htmlspecialchars_decode", htmlSpecialcharsDecodeFn),
		vm.Register("count_chars", countCharsFn),
		vm.Register("bin2hex", func(ctx *vm.Context) vm.Value { return vm.Str(hex.EncodeToString([]byte(arg0(ctx).ToString()))) }),
		vm.Register("hex2bin", hex2binFn),
		vm.Register("base64_encode", func(ctx *vm.Context) vm.Value {
			return vm.Str(base64.StdEncoding.EncodeToString([]byte(arg0(ctx).ToString())))
		}),
		vm.Register("base64_decode", base64DecodeFn),
		vm.Register("urlencode", func(ctx *vm.Context) vm.Value { return vm.Str(phpURLEncode(arg0(ctx).ToString(), false)) }),
		vm.Register("rawurlencode", func(ctx *vm.Context) vm.Value { return vm.Str(phpURLEncode(arg0(ctx).ToString(), true)) }),
		vm.Register("urldecode", func(ctx *vm.Context) vm.Value { return vm.Str(phpURLDecode(arg0(ctx).ToString(), false)) }),
		vm.Register("rawurldecode", func(ctx *vm.Context) vm.Value { return vm.Str(phpURLDecode(arg0(ctx).ToString(), true)) }),
		vm.Register("md5", func(ctx *vm.Context) vm.Value {
			raw := len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool()
			if b, ok := arg0(ctx).ReadOnlyBytes(); ok {
				return vm.Str(phpHashBytes(md5.New(), b, raw))
			}
			return vm.Str(phpHash(md5.New(), arg0(ctx).ToString(), raw))
		}),
		vm.Register("sha1", func(ctx *vm.Context) vm.Value {
			raw := len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool()
			if b, ok := arg0(ctx).ReadOnlyBytes(); ok {
				return vm.Str(phpHashBytes(sha1.New(), b, raw))
			}
			return vm.Str(phpHash(sha1.New(), arg0(ctx).ToString(), raw))
		}),
		vm.Register("md5_file", hashFileFn(false)),
		vm.Register("sha1_file", hashFileFn(true)),
		vm.Register("crc32", func(ctx *vm.Context) vm.Value {
			if b, ok := arg0(ctx).ReadOnlyBytes(); ok {
				return vm.Int(int64(crc32.ChecksumIEEE(b)))
			}
			return vm.Int(int64(crc32.ChecksumIEEE([]byte(arg0(ctx).ToString()))))
		}),
		vm.Register("basename", basenameFn),
		vm.Register("dirname", dirnameFn),
		vm.Register("pathinfo", pathinfoFn),
		vm.Register("pack", packFn),
		vm.Register("unpack", unpackFn),
		vm.Register("serialize", serializeFn),
		vm.Register("unserialize", unserializeFn),
		vm.Register("crypt", cryptFn),
		vm.Register("password_hash", passwordHashFn),
		vm.Register("password_verify", passwordVerifyFn),
		vm.Register("password_get_info", passwordGetInfoFn),
		vm.Register("password_needs_rehash", passwordNeedsRehashFn),
		vm.Register("password_algos", passwordAlgosFn),
		vm.Register("sscanf", sscanfFn),
		vm.Register("metaphone", metaphoneFn),
		vm.Register("str_increment", strIncrementFn),
		vm.Register("str_decrement", strDecrementFn),
		vm.Register("str_getcsv", strGetcsvFn),
		vm.Register("parse_str", parseStrFn),
		vm.Register("hebrev", hebrevFn),
		vm.Register("get_meta_tags", getMetaTagsFn),
		vm.Register("hebrevc", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx).ToString()
			maxChars := 0
			if len(ctx.Args()) >= 2 {
				maxChars = int(arg(ctx, 1).ToInt())
			}
			out := hebrevConvert(s, maxChars)
			out = strings.ReplaceAll(out, "\n", "<br />\n")
			return vm.Str(out)
		}),
		vm.Register("utf8_encode", func(ctx *vm.Context) vm.Value {
			ctx.VM().Deprecated("Function utf8_encode() is deprecated")
			return vm.Str(latin1ToUTF8(arg0(ctx).ToString()))
		}),
		vm.Register("utf8_decode", func(ctx *vm.Context) vm.Value {
			ctx.VM().Deprecated("Function utf8_decode() is deprecated")
			return vm.Str(utf8ToLatin1(arg0(ctx).ToString()))
		}),
		vm.Register("parse_ini_string", parseIniFn),
		vm.Register("strpbrk", strpbrkFn),
		vm.Register("get_html_translation_table", getHTMLTranslationTableFn),
		vm.Register("quoted_printable_encode", func(ctx *vm.Context) vm.Value { return vm.Str(quotedPrintableEncode(argToStringVal(ctx, 0))) }),
		vm.Register("quoted_printable_decode", func(ctx *vm.Context) vm.Value { return vm.Str(quotedPrintableDecode(argToStringVal(ctx, 0))) }),
		vm.Register("convert_uuencode", func(ctx *vm.Context) vm.Value { return vm.Str(convertUUEncode(arg0(ctx).ToString())) }),
		vm.Register("convert_uudecode", func(ctx *vm.Context) vm.Value {
			s, ok := convertUUDecode(arg0(ctx).ToString())
			if !ok {
				ctx.VM().Warning("convert_uudecode(): Argument #1 ($data) is not a valid uuencoded string")
				return vm.Bool(false)
			}
			return vm.Str(s)
		}),

		// --- файловые/дескрипторные встроенные функции (fsAgent) ---
		vm.Register("fopen", fopenFn),
		vm.Register("fclose", fcloseFn),
		vm.Register("fread", freadFn),
		vm.Register("fwrite", fwriteFn),
		vm.Register("fputs", fwriteFn),
		vm.Register("fgets", fgetsFn),
		vm.Register("fgetc", fgetcFn),
		vm.Register("feof", feofFn),
		vm.Register("fseek", fseekFn),
		vm.Register("ftell", ftellFn),
		vm.Register("rewind", rewindFn),
		vm.Register("fflush", fflushFn),
		vm.Register("fpassthru", fpassthruFn),
		vm.Register("fscanf", fscanfFn),
		vm.Register("fprintf", fprintfFn),
		vm.Register("vfprintf", vfprintfFn),
		vm.Register("ftruncate", ftruncateFn),
		vm.Register("flock", flockFn),
		vm.Register("tmpfile", tmpfileFn),
		vm.Register("fstat", fstatFn),
		vm.Register("fsync", func(ctx *vm.Context) vm.Value { return fsyncNamed(ctx, "fsync") }),
		vm.Register("fdatasync", func(ctx *vm.Context) vm.Value { return fsyncNamed(ctx, "fdatasync") }),
		vm.Register("fallocate", func(ctx *vm.Context) vm.Value { return vm.Bool(true) }),
		vm.Register("stream_filter_append", func(ctx *vm.Context) vm.Value { return vm.Bool(true) }),
		vm.Register("stream_filter_prepend", func(ctx *vm.Context) vm.Value { return vm.Bool(true) }),
		vm.Register("stream_filter_remove", func(ctx *vm.Context) vm.Value { return vm.Bool(true) }),
		vm.Register("stream_get_filters", func(ctx *vm.Context) vm.Value { return vm.MakeArray(vm.NewArray()) }),
		vm.Register("file_get_contents", fileGetContentsFn),
		vm.Register("file_put_contents", filePutContentsFn),
		vm.Register("file", fileFn),
		vm.Register("readfile", readfileFn),
		vm.Register("file_exists", fileExistsFn),
		vm.Register("is_file", func(ctx *vm.Context) vm.Value { return isFileKindChecked(ctx, "file") }),
		vm.Register("is_dir", func(ctx *vm.Context) vm.Value { return isFileKindChecked(ctx, "dir") }),
		vm.Register("is_link", func(ctx *vm.Context) vm.Value { return isFileKindChecked(ctx, "link") }),
		vm.Register("is_readable", func(ctx *vm.Context) vm.Value { return accessChecked(ctx, "is_readable", 4) }),
		vm.Register("is_writable", func(ctx *vm.Context) vm.Value { return accessChecked(ctx, "is_writable", 2) }),
		vm.Register("is_writeable", func(ctx *vm.Context) vm.Value { return accessChecked(ctx, "is_writable", 2) }),
		vm.Register("is_executable", func(ctx *vm.Context) vm.Value { return accessChecked(ctx, "is_executable", 1) }),
		vm.Register("filesize", filesizeFn),
		vm.Register("filemtime", func(ctx *vm.Context) vm.Value { return timeFieldFn(ctx, "mtime") }),
		vm.Register("fileatime", func(ctx *vm.Context) vm.Value { return timeFieldFn(ctx, "atime") }),
		vm.Register("filectime", func(ctx *vm.Context) vm.Value { return timeFieldFn(ctx, "ctime") }),
		vm.Register("fileperms", filepermsFn),
		vm.Register("filetype", filetypeFn),
		vm.Register("fileowner", func(ctx *vm.Context) vm.Value { return ownerFieldFn(ctx, true) }),
		vm.Register("filegroup", func(ctx *vm.Context) vm.Value { return ownerFieldFn(ctx, false) }),
		vm.Register("fileinode", fileinodeFn),
		vm.Register("getmypid", func(ctx *vm.Context) vm.Value { return vm.Int(int64(os.Getpid())) }),
		vm.Register("getmyuid", func(ctx *vm.Context) vm.Value { return vm.Int(int64(os.Getuid())) }),
		vm.Register("getmygid", func(ctx *vm.Context) vm.Value { return vm.Int(int64(os.Getgid())) }),
		vm.Register("getmyinode", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("getlastmod", func(ctx *vm.Context) vm.Value { return vm.Int(0) }),
		vm.Register("get_current_user", func(ctx *vm.Context) vm.Value { return vm.Str("") }),
		vm.Register("clearstatcache", func(ctx *vm.Context) vm.Value { return vm.MakeNull() }),
		vm.Register("stat", func(ctx *vm.Context) vm.Value { return statFn(ctx, false) }),
		vm.Register("lstat", func(ctx *vm.Context) vm.Value { return statFn(ctx, true) }),
		vm.Register("readlink", readlinkFn),
		vm.Register("unlink", unlinkFn),
		vm.Register("rename", renameFn),
		vm.Register("copy", copyFn),
		vm.Register("mkdir", mkdirFn),
		vm.Register("rmdir", rmdirFn),
		vm.Register("chmod", chmodFn),
		vm.Register("chown", chownFn),
		vm.Register("chgrp", chgrpFn),
		vm.Register("touch", touchFn),
		vm.Register("realpath", realpathFn),
		vm.Register("sys_get_temp_dir", func(ctx *vm.Context) vm.Value { return vm.Str(sysGetTempDirFunc()) }),
		vm.Register("tempnam", tempnamFn),
		vm.Register("scandir", scandirFn),
		vm.Register("glob", globFn),
		vm.Register("disk_free_space", func(ctx *vm.Context) vm.Value { return diskSpaceFn(ctx, "disk_free_space", true) }),
		vm.Register("diskfreespace", func(ctx *vm.Context) vm.Value { return diskSpaceFn(ctx, "diskfreespace", true) }),
		vm.Register("disk_total_space", func(ctx *vm.Context) vm.Value { return diskSpaceFn(ctx, "disk_total_space", false) }),
		vm.Register("symlink", symlinkFn),
		vm.Register("link", linkFn),
		vm.Register("linkinfo", linkinfoFn),
		vm.Register("is_uploaded_file", isUploadedFileFn),
		vm.Register("move_uploaded_file", moveUploadedFileFn),
		vm.Register("fnmatch", fnmatchFn),
		vm.Register("parse_ini_file", parseIniFileFn),
		vm.Register("fgetcsv", fgetcsvFn),
		vm.Register("fputcsv", fputcsvFn),
		vm.Register("fgetss", func(ctx *vm.Context) vm.Value { return vm.Bool(false) }),
		vm.Register("chdir", chdirFn),
		vm.Register("getcwd", func(ctx *vm.Context) vm.Value {
			d, err := os.Getwd()
			if err != nil {
				return vm.Bool(false)
			}
			return vm.Str(d)
		}),
		vm.Register("opendir", opendirFn),
		vm.Register("dir", dirFn),
		vm.Register("readdir", readdirFn),
		vm.Register("rewinddir", rewinddirFn),
		vm.Register("closedir", closedirFn),
		vm.Register("is_resource", func(ctx *vm.Context) vm.Value {
			return vm.Bool(!vm.ResourceClosed(arg0(ctx)))
		}),
		vm.Register("get_resource_type", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx)
			r := vm.AsResource(s)
			if r == nil {
				ctx.Throw(vm.NewThrowable("TypeError",
					vm.ArgTypeError("get_resource_type", 1, "resource", "resource", phpTypeName(ctx, 0), "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if r.Closed {
				return vm.Str("Unknown")
			}
			return vm.Str(r.Kind)
		}),
		vm.Register("get_resource_id", func(ctx *vm.Context) vm.Value {
			s := arg0(ctx)
			r := vm.AsResource(s)
			if r == nil {
				ctx.Throw(vm.NewThrowable("TypeError",
					vm.ArgTypeError("get_resource_id", 1, "resource", "resource", phpTypeName(ctx, 0), "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.Int(r.ID)
		}),
		vm.Register("stream_get_contents", streamGetContentsFn),
		vm.Register("stream_copy_to_stream", streamCopyToStreamFn),
		vm.Register("stream_get_line", streamGetLineFn),
		vm.Register("stream_set_timeout", streamSetTimeoutFn),
		vm.Register("stream_set_write_buffer", setFileBufferFn),
		vm.Register("stream_set_read_buffer", setReadFileBufferFn),
		vm.Register("stream_set_blocking", streamSetBlockingFn),
		vm.Register("socket_set_timeout", streamSetTimeoutFn),
		vm.Register("socket_set_blocking", streamSetBlockingFn),
		vm.Register("stream_is_local", streamIsLocalFn),
		vm.Register("stream_get_meta_data", streamGetMetaDataFn),
		vm.Register("stream_isatty", streamIsattyFn),
		vm.Register("stream_wrapper_register", streamWrapperRegisterFn),
		vm.Register("stream_register_wrapper", streamWrapperRegisterFn),
		vm.Register("stream_wrapper_unregister", streamWrapperUnregisterFn),
		vm.Register("stream_wrapper_restore", streamWrapperRestoreFn),
		vm.Register("stream_get_wrappers", streamGetWrappersFn),
		vm.Register("stream_resolve_include_path", streamResolveIncludePathFn),
		vm.Register("stream_supports_lock", streamSupportsLockFn),
		vm.Register("stream_set_chunk_size", streamSetChunkSizeFn),
		vm.Register("zend_leak_variable", zendLeakVariableFn),
		vm.Register("session_id", sessionIDFn),
		vm.Register("is_iterable", isIterableFn),
		vm.Register("headers_list", headersListFn),
		vm.Register("ini_restore", iniRestoreFn),
		vm.Register("settype", setTypeFn),
		vm.Register("getopt", getoptFn),
		vm.Register("gzcompress", gzCompressFn),
		vm.Register("gzuncompress", gzUncompressFn),
		vm.Register("gzdeflate", gzDeflateFn),
		vm.Register("gzinflate", gzInflateFn),
		vm.Register("gzencode", gzEncodeFn),
		vm.Register("gzdecode", gzDecodeFn),
		vm.Register("bzdecompress", bzDecompressFn),
		vm.Register("stream_filter_register", streamFilterRegisterFn),
		vm.Register("stream_filter_append", streamFilterAppendFn),
		vm.Register("stream_filter_prepend", streamFilterPrependFn),
		vm.Register("stream_filter_remove", streamFilterRemoveFn),
		vm.Register("stream_get_filters", streamGetFiltersFn),
		vm.Register("stream_bucket_new", streamBucketNewFn),
		vm.Register("stream_bucket_append", streamBucketAppendFn),
		vm.Register("stream_bucket_prepend", streamBucketPrependFn),
		vm.Register("stream_bucket_make_writeable", streamBucketMakeWriteableFn),

		// --- сокеты ---
		vm.Register("stream_socket_server", streamSocketServerFn),
		vm.Register("stream_socket_client", streamSocketClientFn),
		vm.Register("stream_socket_pair", streamSocketPairFn),
		vm.Register("stream_socket_accept", streamSocketAcceptFn),
		vm.Register("stream_socket_get_name", streamSocketGetNameFn),
		vm.Register("stream_socket_shutdown", streamSocketShutdownFn),
		vm.Register("stream_socket_recvfrom", streamSocketRecvfromFn),
		vm.Register("stream_socket_sendto", streamSocketSendtoFn),
		vm.Register("fsockopen", fsockopenFn),
		vm.Register("pfsockopen", pfsockopenFn),
		vm.Register("stream_select", streamSelectFn),
		vm.Register("socket_get_status", socketGetStatusFn),
		vm.Register("getimagesize", getimagesizeFromPathFn),
		vm.Register("getimagesizefromstring", getimagesizefromstringFn),
		vm.Register("image_type_to_mime_type", imageTypeToMimeTypeFn),
		vm.Register("image_type_to_extension", imageTypeToExtensionFn),
		vm.Register("iptcparse", iptcParseFn),
		vm.Register("iptcembed", iptcembedFn),
		vm.Register("mail", mailFn),

		// --- сеть: DNS/IP/cookie/syslog ---
		vm.Register("ip2long", ip2longFn),
		vm.Register("long2ip", long2ipFn),
		vm.Register("gethostbyname", gethostbynameFn),
		vm.Register("gethostbynamel", gethostbynamelFn),
		vm.Register("gethostbyaddr", gethostbyaddrFn),
		vm.Register("dns_check_record", dnsCheckRecordFn),
		vm.Register("checkdnsrr", dnsCheckRecordFn),
		vm.Register("dns_get_record", dnsGetRecordFn),
		vm.Register("dns_get_mx", dnsGetMxFn),
		vm.Register("getmxrr", dnsGetMxFn),
		vm.Register("closelog", closelogFn),
		vm.Register("openlog", openlogFn),
		vm.Register("syslog", syslogFn),
		vm.Register("getprotobyname", getprotobynameFn),
		vm.Register("getservbyname", getservbynameFn),
		vm.Register("getservbyport", getservbyportFn),
		vm.Register("inet_pton", inetPtonFn),
		vm.Register("inet_ntop", inetNtopFn),

		// --- дата/время (dateAgent) ---
		vm.Register("date", func(ctx *vm.Context) vm.Value { return dateBuiltin(ctx, false) }),
		vm.Register("gmdate", func(ctx *vm.Context) vm.Value { return dateBuiltin(ctx, true) }),
		vm.Register("idate", idateBuiltin),
		vm.Register("mktime", func(ctx *vm.Context) vm.Value { return mktimeBuiltin(ctx, false) }),
		vm.Register("gmmktime", func(ctx *vm.Context) vm.Value { return mktimeBuiltin(ctx, true) }),
		vm.Register("checkdate", checkdateFn),
		vm.Register("getdate", getdateFn),
		vm.Register("localtime", localtimeFn),
		vm.Register("date_default_timezone_set", dateDefaultTimezoneSetFn),
		vm.Register("date_default_timezone_get", func(ctx *vm.Context) vm.Value { return vm.Str(dateLocName) }),
		vm.Register("strtotime", strtotimeFn),
		vm.Register("timezone_identifiers_list", func(ctx *vm.Context) vm.Value { return vm.MakeArray(vm.NewArray()) }),
		vm.Register("date_parse", dateParseFn),
		vm.Register("date_diff", func(ctx *vm.Context) vm.Value {
			args := ctx.Args()
			var a, b vm.Slot
			if len(args) > 0 {
				a = args[0]
			}
			if len(args) > 1 {
				b = args[1]
			}
			res, err := ctx.VMInstance().DateDiff(a, b)
			if err != nil {
				if t, ok := err.(*vm.Throwable); ok {
					ctx.Throw(t)
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
			}
			return res
		}),

		// --- JSON (dateAgent) ---
		vm.Register("json_encode", jsonEncodeFn),
		vm.Register("json_decode", jsonDecodeFn),
		vm.Register("json_last_error", func(ctx *vm.Context) vm.Value { return vm.Int(int64(jsonLastError)) }),
		vm.Register("json_last_error_msg", func(ctx *vm.Context) vm.Value { return vm.Str(jsonLastErrorMsg(jsonLastError)) }),

		// --- URL (dateAgent) ---
		vm.Register("parse_url", parseURLFn),
		vm.Register("http_build_query", httpBuildQueryFn),

		// --- PCRE (pregAgent) ---
		vm.Register("preg_quote", pregQuoteFn),
		vm.Register("preg_match", pregMatchFn),
		vm.Register("preg_match_all", pregMatchAllFn),
		vm.Register("preg_replace", pregReplaceFn),
		vm.Register("preg_replace_callback", pregReplaceCallbackFn),
		vm.Register("preg_split", pregSplitFn),
		vm.Register("preg_grep", pregGrepFn),
		vm.Register("preg_last_error", func(ctx *vm.Context) vm.Value { return vm.Int(int64(pregLastError)) }),
		vm.Register("preg_last_error_msg", func(ctx *vm.Context) vm.Value { return vm.Str(pregLastErrorMsg(pregLastError)) }),

		// --- mbstring (mb_*) ---
		vm.Register("mb_internal_encoding", mbInternalEncodingFn),
		vm.Register("mb_parse_str", mbParseStrFn),
		vm.Register("mb_list_encodings", mbListEncodingsFn),
		vm.Register("mb_encoding_aliases", mbEncodingAliasesFn),
		vm.Register("mb_check_encoding", mbCheckEncodingFn),
		vm.Register("mb_detect_encoding", mbDetectEncodingFn),
		vm.Register("mb_convert_encoding", mbConvertEncodingFn),
		vm.Register("mb_convert_variables", mbConvertVariablesFn),
		vm.Register("mb_strlen", mbStrlenFn),
		vm.Register("mb_substr", mbSubstrFn),
		vm.Register("mb_strpos", func(ctx *vm.Context) vm.Value { return mbStrposFn(ctx, false, false) }),
		vm.Register("mb_stripos", func(ctx *vm.Context) vm.Value { return mbStrposFn(ctx, true, false) }),
		vm.Register("mb_strrpos", func(ctx *vm.Context) vm.Value { return mbStrposFn(ctx, false, true) }),
		vm.Register("mb_strripos", func(ctx *vm.Context) vm.Value { return mbStrposFn(ctx, true, true) }),
		vm.Register("mb_strtolower", func(ctx *vm.Context) vm.Value { return mbCaseFn(ctx, false) }),
		vm.Register("mb_strtoupper", func(ctx *vm.Context) vm.Value { return mbCaseFn(ctx, true) }),
		vm.Register("mb_convert_case", mbConvertCaseFn),
		vm.Register("mb_str_split", mbStrSplitFn),
		vm.Register("mb_ord", mbOrdFn),
		vm.Register("mb_chr", mbChrFn),
		vm.Register("mb_substr_count", mbSubstrCountFn),
		vm.Register("mb_strstr", func(ctx *vm.Context) vm.Value { return mbStrstrFn(ctx, false) }),
		vm.Register("mb_stristr", func(ctx *vm.Context) vm.Value { return mbStrstrFn(ctx, true) }),
		vm.Register("mb_strrchr", mbStrrchrFn),
		vm.Register("mb_strwidth", mbStrwidthFn),
		vm.Register("mb_strimwidth", mbStrimwidthFn),

		// --- assert (assertAgent) ---
		vm.Register("assert", assertFn),
		vm.Register("__php_assert1", assert1Fn),
		vm.Register("__php_isset_this", issetThisFn),
		vm.Register("__php_declare_encoding", declareEncodingFn),
		vm.Register("__php_class_alias_conflict", classAliasConflictFn),
		vm.Register("__php_named_arg_error", namedArgErrorFn),
		vm.Register("__php_fcc", fccFn),
		vm.Register("assert_options", assertOptionsFn),

		// --- iconv (miscAgent) ---
		vm.Register("iconv", iconvFn),
		vm.Register("iconv_strlen", iconvStrlenFn),
		vm.Register("iconv_substr", iconvSubstrFn),
		vm.Register("iconv_strpos", func(ctx *vm.Context) vm.Value { return iconvStrposFn(ctx, false) }),
		vm.Register("iconv_strrpos", func(ctx *vm.Context) vm.Value { return iconvStrposFn(ctx, true) }),
		vm.Register("ob_iconv_handler", obIconvHandlerFn),

		// --- переменные / константы / отладка (miscAgent) ---
		vm.Register("get_defined_vars", getDefinedVarsFn),
		vm.Register("compact", compactFn),
		vm.Register("extract", extractFn),
		vm.Register("error_get_last", errorGetLastFn),
		vm.Register("error_clear_last", func(ctx *vm.Context) vm.Value {
			lastErrorArray = nil
			ctx.VM().ClearLastError()
			return vm.MakeNull()
		}),
		vm.Register("debug_backtrace", debugBacktraceFn),
		vm.Register("debug_print_backtrace", func(ctx *vm.Context) vm.Value {
			if s := ctx.VM().DebugBacktraceString(); s != "" {
				ctx.VM().Write(s)
			}
			return vm.MakeNull()
		}),
		vm.Register("get_defined_constants", getDefinedConstantsFn),
		vm.Register("get_defined_functions", getDefinedFunctionsFn),
		vm.Register("__php_dollar_brace_deprecation", func(ctx *vm.Context) vm.Value {
			ctx.VM().Deprecated("Using ${var} in strings is deprecated, use {$var} instead")
			return vm.Str("")
		}),
		vm.Register("__php_dollar_brace_expr_deprecation", func(ctx *vm.Context) vm.Value {
			ctx.VM().Deprecated("Using ${expr} (variable variables) in strings is deprecated, use {${expr}} instead")
			return vm.Str("")
		}),
		vm.Register("setlocale", func(ctx *vm.Context) vm.Value {
			// Упрощённо: возвращаем запрошенную локаль, поддержки каталогов нет.
			if len(ctx.Args()) < 2 {
				return vm.Str("C")
			}
			return vm.Str(arg(ctx, 1).ToString())
		}),
		vm.Register("nl_langinfo", func(ctx *vm.Context) vm.Value { return vm.Str("") }),
		vm.Register("strcoll", func(ctx *vm.Context) vm.Value {
			return vm.Int(phpStrCmp(arg(ctx, 0).ToString(), arg(ctx, 1).ToString()))
		}),

		// --- системная информация (miscAgent) ---
		vm.Register("gethostname", gethostnameFn),
		vm.Register("php_uname", phpUnameFn),
		vm.Register("hrtime", hrtimeFn),
	}
	// Мутирующие массив функции получают его Ref-слотом (by-ref параметр):
	// компилятор подготовит Ref-ячейку, а встроенная функция запишет в неё
	// результат — изменения видны вызывающему коду.
	for _, f := range fs {
		switch f.Name {
		case "sort", "rsort", "asort", "arsort", "ksort", "krsort":
			f.ByRefArgs = []bool{true, false}
			f.ParamNames = []string{"array", "flags"}
		case "usort", "uasort", "uksort":
			f.ByRefArgs = []bool{true, false}
			f.ParamNames = []string{"array", "callback"}
		case "array_walk", "array_walk_recursive":
			f.ByRefArgs = []bool{true, false, false}
			f.ParamNames = []string{"array", "callback", "arg"}
		case "array_keys":
			// array_keys(array $array, mixed $filter_value = UNKNOWN, bool $strict = false)
			f.ParamNames = []string{"array", "filter_value", "strict"}
			f.NumRequired = 1
		case "array_slice":
			// array_slice(array $array, int $offset, ?int $length = null, bool $preserve_keys = false)
			f.ParamNames = []string{"array", "offset", "length", "preserve_keys"}
			f.NumRequired = 2
		case "str_pad":
			// str_pad(string $string, int $length, string $pad_string = " ", int $pad_type = STR_PAD_RIGHT)
			f.ParamNames = []string{"string", "length", "pad_string", "pad_type"}
			f.NumRequired = 2
		case "strlen":
			f.ParamNames = []string{"string"}
			f.NumRequired = 1
		case "array_shift", "array_pop", "natsort", "natcasesort", "shuffle",
			"reset", "end", "next", "prev", "each":
			f.ByRefArgs = []bool{true}
			f.ParamNames = []string{"array"}
		case "array_push", "array_unshift":
			f.ByRefArgs = []bool{true}
			f.ParamNames = []string{"array", "values"}
		case "extract":
			// extract(array &$array, int $flags = EXTR_OVERWRITE, string $prefix = "")
			f.ByRefArgs = []bool{true, false, false}
			f.ParamNames = []string{"array", "flags", "prefix"}
		case "array_splice":
			f.ByRefArgs = []bool{true}
			f.ParamNames = []string{"array", "offset", "length", "replacement"}
		case "array_multisort":
			// Все аргументы-массивы передаются по ссылке (число и позиции
			// заранее неизвестны), флаги-скаляры тоже безопасно обернуть в Ref:
			// read-only временная ячейка без writeback.
			mask := make([]bool, 128)
			for i := range mask {
				mask[i] = true
			}
			f.ByRefArgs = mask
		case "preg_match", "preg_match_all":
			// ($pattern, $subject, &$matches = null, $flags = 0, $offset = 0)
			f.ByRefArgs = []bool{false, false, true}
			if f.Name == "preg_match" {
				f.ParamNames = []string{"pattern", "subject", "matches", "flags", "offset"}
			}
		case "sscanf", "fscanf":
			// ($string/$handle, $format, &...$vars) — все переменные после формата по ссылке.
			mask := make([]bool, 128)
			for i := 2; i < len(mask); i++ {
				mask[i] = true
			}
			f.ByRefArgs = mask
		case "preg_replace", "preg_replace_callback":
			// ($pattern, $replacement|$callback, $subject, $limit = -1, &$count = null)
			f.ByRefArgs = []bool{false, false, false, false, true}
			if f.Name == "preg_replace" {
				f.ParamNames = []string{"pattern", "replacement", "subject", "limit", "count"}
			}
		case "settype":
			// ($var, $type) — переменная по ссылке.
			f.ByRefArgs = []bool{true, false}
		case "getopt":
			// ($short_options, $long_options = [], &$rest_index = null)
			f.ByRefArgs = []bool{false, false, true}
		case "getimagesize", "getimagesizefromstring":
			// ($filename, &$image_info = null)
			f.ByRefArgs = []bool{false, true}
			f.ParamNames = []string{"filename", "image_info"}
		case "file_get_contents":
			f.ParamNames = []string{"filename", "use_include_path", "context", "offset", "length"}
		case "file_put_contents":
			f.ParamNames = []string{"filename", "data", "flags", "context"}
		case "str_replace", "str_ireplace":
			f.ParamNames = []string{"search", "replace", "subject", "count"}
			f.ByRefArgs = []bool{false, false, false, true}
		case "similar_text":
			f.ParamNames = []string{"string1", "string2", "percent"}
			f.ByRefArgs = []bool{false, false, true}
		case "dns_get_mx", "getmxrr":
			// ($hostname, &$hosts, &$weights = null)
			f.ByRefArgs = []bool{false, true, true}
			f.ParamNames = []string{"hostname", "hosts", "weights"}
		case "parse_str", "mb_parse_str":
			f.ParamNames = []string{"string", "result"}
			f.ByRefArgs = []bool{false, true}
		case "exec", "system", "passthru":
			f.ByRefArgs = []bool{false, true, true}
		case "proc_open":
			// ($command, $descriptor_spec, &$pipes, $cwd = null, $env_vars = null, $options = null)
			f.ParamNames = []string{"command", "descriptor_spec", "pipes", "cwd", "env_vars", "options"}
			f.ByRefArgs = []bool{false, false, true, false, false, false}
		case "is_callable":
			// ($value, $syntax_only = false, &$callable_name = null)
			f.ParamNames = []string{"value", "syntax_only", "callable_name"}
			f.ByRefArgs = []bool{false, false, true}
		case "flock":
			// ($stream, $operation, &$would_block = null)
			f.ParamNames = []string{"stream", "operation", "would_block"}
			f.ByRefArgs = []bool{false, false, true}
		case "copy":
			// ($from, $to, $context = null)
			f.ParamNames = []string{"from", "to", "context"}
			f.NumRequired = 2
		case "stream_socket_server":
			// ($address, &$error_code = null, &$error_message = null, $flags = 0, $context = null)
			f.ParamNames = []string{"address", "error_code", "error_message", "flags", "context"}
			f.ByRefArgs = []bool{false, true, true, false, false}
		case "stream_socket_client":
			// ($address, &$error_code = null, &$error_message = null, $timeout = null, $flags = 0, $context = null)
			f.ParamNames = []string{"address", "error_code", "error_message", "timeout", "flags", "context"}
			f.ByRefArgs = []bool{false, true, true, false, false, false}
		case "fsockopen", "pfsockopen":
			// ($hostname, $port = -1, &$error_code = null, &$error_message = null, $timeout = null)
			f.ParamNames = []string{"hostname", "port", "error_code", "error_message", "timeout"}
			f.ByRefArgs = []bool{false, false, true, true, false}
		case "stream_socket_accept":
			// ($socket, $timeout = null, &$peer_name = null)
			f.ParamNames = []string{"socket", "timeout", "peer_name"}
			f.ByRefArgs = []bool{false, false, true}
		case "stream_socket_recvfrom":
			// ($socket, $length, $flags = 0, &$address = null)
			f.ParamNames = []string{"socket", "length", "flags", "address"}
			f.ByRefArgs = []bool{false, false, false, true}
		case "stream_select":
			// (&$read, &$write, &$except, $seconds, $microseconds = null)
			f.ParamNames = []string{"read", "write", "except", "seconds", "microseconds"}
			f.ByRefArgs = []bool{true, true, true, false, false}
		case "mb_convert_variables":
			// ($to_encoding, $from_encoding, &...$vars) — переменные по ссылке
			// начиная с третьего аргумента; первые два by-value.
			mask := make([]bool, 128)
			for i := 2; i < len(mask); i++ {
				mask[i] = true
			}
			f.ByRefArgs = mask
		}
	}
	internalFuncNames = internalFuncNames[:0]
	for _, f := range fs {
		internalFuncNames = append(internalFuncNames, f.Name)
	}
	// Функции эталонного php 8.3, которые VM не реализует, но которые видны в
	// get_defined_functions() (используются в тестах disable_functions).
	internalFuncNames = append(internalFuncNames, referenceOnlyFuncs...)
	// Арность и имена параметров встроенных функций из таблицы (сгенерирована
	// из ReflectionFunction php 8.3): проверка вызова и именованные аргументы.
	for _, f := range fs {
		sig, ok := intrinsicArity[f.Name]
		if !ok {
			continue
		}
		if len(f.ParamNames) == 0 {
			f.ParamNames = sig.Names
		}
		if f.MinArgs == 0 && sig.Required > 0 {
			f.MinArgs = sig.Required
		}
		if sig.Variadic {
			f.NativeVariadic = true
		} else if f.MaxArgs == 0 {
			f.MaxArgs = sig.Total
		}
		if f.Name == "assert" {
			// Компилятор дописывает служебный третий аргумент (текст проверки).
			f.MaxArgs = 3
		}
		f.HasNativeSig = true
		// #[SensitiveParameter] у встроенных функций (php-src 8.3): параметр
		// пароля у crypt/password_hash/password_verify скрывается в трейсах.
		switch f.Name {
		case "crypt", "password_hash", "password_verify":
			f.SensitiveParams = []bool{true}
		}
	}
	// assert() с одним аргументом компилируется в __php_assert1 (служебный
	// текст проверки); в трейсах кадр должен выглядеть как assert(...).
	for _, f := range fs {
		if f.Name == "__php_assert1" {
			f.DisplayName = "assert"
		}
	}
	return fs
}

// defaultArgCheckBuiltin проверяет runtime-дефолт параметра (значение
// константы) по объявленному типу и возвращает приведённое значение.
func defaultArgCheckBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	out, err := ctx.VM().CheckDefaultArg(args[0], int(args[1].ToInt()))
	if err != nil {
		throwCallable(ctx, err)
		return vm.MakeNull()
	}
	return out
}

// namedArgCheckFn — проверка повтора имени именованного аргумента при сборке
// массива аргументов (в т.ч. поверх ключа из распаковки). Zend:
// «Named parameter $x overwrites previous argument».
func namedArgCheckFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) < 2 {
		return vm.MakeNull()
	}
	arr := derefSlot(a[0])
	name := derefSlot(a[1]).ToString()
	if arr.Type() == vm.ArrayType {
		if ar := arr.AsArray(); ar != nil {
			if _, ok := ar.Get(vm.MakeStrKey(name)); ok {
				ctx.Throw(vm.NewThrowable("Error", "Named parameter $"+name+" overwrites previous argument"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		}
	}
	return vm.MakeNull()
}

// namedArgErrorFn — рантайм-исключение для ошибок именованных аргументов
// известной функции (unknown/duplicate/missing required).
func namedArgErrorFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	class, msg := "Error", ""
	if len(a) > 0 {
		class = derefSlot(a[0]).ToString()
	}
	if len(a) > 1 {
		msg = derefSlot(a[1]).ToString()
	}
	ctx.Throw(vm.NewThrowable(class, msg))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.MakeNull()
}

// classAliasConflictFn — предупреждение о повторном объявлении класса при
// конфликте с псевдонимом class_alias.
func classAliasConflictFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) == 0 {
		return vm.MakeNull()
	}
	name := derefSlot(a[0]).ToString()
	if kind, ok := ctx.VM().AliasConflictFor(name); ok {
		ctx.VM().Warning("Cannot declare " + kind + " " + name + ", because the name is already in use")
	}
	return vm.MakeNull()
}

// declareEncodingFn — declare(encoding=...): при выключенном zend.multibyte
// печатает Warning, иначе проверяет литеральность значения и печатает
// Warning "Unsupported encoding [..]" для не-строк.
func declareEncodingFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	if len(a) < 2 {
		return vm.MakeNull()
	}
	if v, ok := IniValue("zend.multibyte"); !ok || iniBoolInt(v) == 0 {
		ctx.VM().Warning("declare(encoding=...) ignored because Zend multibyte feature is turned off by settings")
		return vm.MakeNull()
	}
	if !derefSlot(a[1]).ToBool() {
		ctx.Throw(vm.NewFatal("Encoding must be a literal"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	val := derefSlot(a[0])
	if val.Type() == vm.StringType {
		return vm.MakeNull()
	}
	ctx.VM().Warning("Unsupported encoding [" + vm.FormatFloat(val.ToFloat()) + "]")
	return vm.MakeNull()
}
