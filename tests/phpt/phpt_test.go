package phpt

import (
	"bytes"
	"context"
	"github.com/stretchr/testify/assert"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"regexp"
	"strings"
	"testing"
)

type PhpT struct {
	Test, File, Expect, Expectf, PostRaw, Credits, SkipIf string
	Get, Post, Ini, Cookie                                map[any]any
	Args, Extensions                                      []any
	Env                                                   []string
}

func expectfToRegex(wanted string) *regexp.Regexp {
	wantedRe := regexp.MustCompile("\r\n").ReplaceAllString(wanted, "\n")
	temp := ""
	r := "%r"
	startOffset := 0
	length := len(wantedRe)

	for startOffset < length {
		start := strings.Index(wantedRe[startOffset:], r)
		end := 0

		if start >= 0 {
			// we have found a start tag
			end = strings.Index(wantedRe[start+2:], r)

			if end == -1 {
				// unbalanced tag, ignore it.
				end = length
				start = length
			}
		} else {
			// no more %r sections
			end = length
			start = length
		}
		// quote a non re portion of the string
		temp += regexp.QuoteMeta(wantedRe[startOffset:start])
		// add the re unquoted.
		if end > start {
			temp += "(" + wantedRe[start+2:end] + ")"
		}
		startOffset = end + 2
	}

	wantedRe = temp
	return regexp.MustCompile(strings.NewReplacer(
		"%e", regexp.QuoteMeta(string(os.PathSeparator)),
		"%s", "[^\r\n]+",
		"%S", "[^\r\n]*",
		"%a", ".+",
		"%A", ".*",
		"%w", "\\s*",
		"%i", "[+-]?\\d+",
		"%d", "\\d+",
		"%x", "[0-9a-fA-F]+",
		"%f", "[+-]?(?:\\d+|(?=\\.\\d))(?:\\.\\d+)?(?:[Ee][+-]?\\d+)?",
		"%c", ".",
		"%0", "\x00",
	).Replace(wantedRe))
}

func (phpt *PhpT) RunTest(t *testing.T) {
	t.Run(phpt.Test, func(t *testing.T) {
		output := bytes.NewBuffer(nil)

		ctx := vm.NewGlobalContext(context.Background(), nil, output)
		comp := compiler.NewCompiler(nil)
		fn := comp.Compile([]byte(phpt.File), &ctx)
		ctx.Run(fn)
		if len(phpt.Expect) > 0 {
			assert.Equal(t, phpt.Expect, output.String())
		}
		if len(phpt.Expectf) > 0 {
			assert.Regexp(t, expectfToRegex(phpt.Expectf), output.String())
		}
	})
}
