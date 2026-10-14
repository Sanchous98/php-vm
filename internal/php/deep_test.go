package php

import (
	"strings"
	"testing"
)

func TestDeepGuardTemp(t *testing.T) {
	cases := map[string]string{
		"paren": strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000),
		"bang":  strings.Repeat("!", 5000) + "1",
		"minus": strings.Repeat("-", 5000) + "1",
		"brack": strings.Repeat("[", 5000),
		"pow":   strings.Repeat("1**", 5000) + "1",
		"tern":  strings.Repeat("1?1:", 5000) + "1",
		"assn":  strings.Repeat("$a=", 5000) + "1",
	}
	for name, s := range cases {
		_, err := Parse(s + ";")
		t.Logf("%s: err=%v", name, err)
	}
}
