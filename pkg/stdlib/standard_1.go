package stdlib

import (
	"path/filepath"
	"slices"
	"strings"
	_ "unsafe"
)

//go:linkname StrToUpper strings.ToUpper
func StrToUpper(s string) string

//go:linkname StrToLower strings.ToLower
func StrToLower(s string) string

//go:linkname StrPos strings.Index
func StrPos(str, sub string) int

func StrIPos(str, sub string) int {
	return StrPos(StrToLower(str), StrToLower(sub))
}

//go:linkname StrRPos strings.LastIndex
func StrRPos(str, sub string) int

func StrRIPos(str, sub string) int {
	return StrRPos(StrToLower(str), StrToLower(sub))
}

func StrRev(str string) string {
	res := []rune(str)
	slices.Reverse(res)
	return string(res)
}

func Nl2Br(str string) string {
	return strings.ReplaceAll(str, "\n", "<br />")
}

//go:linkname Ext path/filepath.Ext
func Ext(str string) string

func Basename(str, trimSuffix string) string {
	res := filepath.Base(str)

	if len(trimSuffix) > 0 {
		res = strings.TrimSuffix(res, trimSuffix)
	}

	return res
}

//go:linkname Dirname path/filepath.Dir
func Dirname(str string) string

func StripSlashes(str string) string {
	return strings.ReplaceAll(str, "\\", "")
}

func StripCSlashes(str string) string {
	var res strings.Builder
	runes := []rune(str)

	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' {
			switch runes[i+1] {
			case 'a':
				i++
				res.WriteString("\a")
			case 'b':
				i++
				res.WriteString("\b")
			case 'f':
				i++
				res.WriteString("\f")
			case 'n':
				i++
				res.WriteString("\n")
			case 'r':
				i++
				res.WriteString("\r")
			case 't':
				i++
				res.WriteString("\t")
			case 'v':
				i++
				res.WriteString("\v")
			}
		} else {
			res.WriteRune(runes[i])
		}
	}

	return res.String()
}

func StrStr(haystack, needle string, beforeNeedle bool) (string, bool) {
	if beforeNeedle {
		return strings.CutPrefix(haystack, needle)
	}

	return strings.CutSuffix(haystack, needle)
}

func StrIStr(haystack, needle string, beforeNeedle bool) (string, bool) {
	if beforeNeedle {
		if !strings.HasPrefix(StrToLower(haystack), StrToLower(needle)) {
			return "", false
		}

		return haystack[len(needle):], true
	}

	if !strings.HasSuffix(StrToLower(haystack), StrToLower(needle)) {
		return "", false
	}

	return haystack[:len(haystack)-len(needle)], true
}
