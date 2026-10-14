package php

import "phpvm/internal/vm"

// Сигнатуры встроенных строковых функций для ReflectionFunction.
func init() {
	vm.SetInternalFnSig("htmlspecialchars", "standard", []string{
		"<required> string $string",
		"<optional> int $flags = ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401",
		"<optional> ?string $encoding = null",
		"<optional> bool $double_encode = true",
	}, "string")
	vm.SetInternalFnSig("get_html_translation_table", "standard", []string{
		"<optional> int $table = HTML_SPECIALCHARS",
		"<optional> int $flags = ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401",
		"<optional> string $encoding = \"UTF-8\"",
	}, "array")
}
