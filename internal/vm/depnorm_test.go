package vm

import (
	"regexp"
	"strings"
)

// depLineRe — строка депрекации в выводе php/нашего раннера.
var depLineRe = regexp.MustCompile(`(PHP )?Deprecated:[^\n]*\n`)

// normNoDep нормализует вывод для сравнений против php: убирает строки
// депрекаций и все пробелы/переводы строк. Нужно потому, что php -r печатает
// депрекации иначе, чем наш сниппет-раннер («PHP Deprecated … in Command line
// code» vs «Deprecated … in php»), а сами депрекации php-корректны.
func normNoDep(s string) string {
	return strings.Join(strings.Fields(depLineRe.ReplaceAllString(s, "")), "")
}
