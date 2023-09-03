package stdlib

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestWordWrap(t *testing.T) {
	type args struct {
		word         string
		width        int
		wordBreak    string
		cutLongWords bool
	}
	tests := [...]struct {
		name string
		args args
		want string
	}{
		{"#1", args{"The quick brown fox jumped over the lazy dog.", 20, "<br />\n", false}, "The quick brown fox<br />\njumped over the lazy<br />\ndog."},
		{"#2", args{"A very long woooooooooooord.", 8, "\n", true}, "A very\nlong\nwooooooo\nooooord."},
		{"#3", args{"A very long woooooooooooooooooord. and something", 8, "\n", false}, "A very\nlong\nwoooooooooooooooooord.\nand\nsomething"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WordWrap(tt.args.word, tt.args.width, tt.args.wordBreak, tt.args.cutLongWords)
			assert.Equalf(t, tt.want, got, "WordWrap() = %v, want %v", got, tt.want)
		})
	}
}
