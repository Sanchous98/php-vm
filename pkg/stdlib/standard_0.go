package stdlib

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func Bin2hex(s string) string {
	if ui, err := strconv.ParseUint(s, 2, 64); err == nil {
		return fmt.Sprintf("%x", ui)
	}

	return ""
}

func Sleep(s int) {
	time.Sleep(time.Duration(s) * time.Second)
}

func Usleep(m int) {
	time.Sleep(time.Duration(m) * time.Millisecond)
}

//func Constant(name string) any

func TimeNanosleep(seconds, nanoseconds int) error {
	if nanoseconds >= 10e9 {
		return fmt.Errorf("nanoseconds must be less than a billion")
	}

	time.Sleep(time.Duration(seconds)*time.Second + time.Duration(nanoseconds))
	return nil
}

func TimeSleepUntil(timestamp int64) (err error) {
	t := time.Unix(timestamp, 0)

	if t.Before(t) {
		err = fmt.Errorf("specified timestamp is in the past")
	}

	time.Sleep(time.Until(t))
	return
}

func WordWrap(line string, width int, wordBreak string, cutLongWords bool) string {
	if wordBreak == "" {
		wordBreak = "\n"
	}
	var buf strings.Builder
	buf.Grow(len(line))
	var current int
	var wordbuf, spacebuf bytes.Buffer
	for _, char := range line {
		if char == '\n' {
			if wordbuf.Len() == 0 {
				if current+spacebuf.Len() > width {
					current = 0
				} else {
					current += spacebuf.Len()
					spacebuf.WriteTo(&buf)
				}
				spacebuf.Reset()
			} else {
				current += spacebuf.Len() + wordbuf.Len()
				spacebuf.WriteTo(&buf)
				spacebuf.Reset()
				wordbuf.WriteTo(&buf)
				wordbuf.Reset()
			}
			buf.WriteRune(char)
			current = 0
		} else if cutLongWords && wordbuf.Len() >= width {
			current += spacebuf.Len() + wordbuf.Len()
			spacebuf.WriteTo(&buf)
			spacebuf.Reset()
			wordbuf.WriteTo(&buf)
			wordbuf.Reset()
			wordbuf.WriteRune(char)
		} else if unicode.IsSpace(char) {
			if spacebuf.Len() == 0 || wordbuf.Len() > 0 {
				current += spacebuf.Len() + wordbuf.Len()
				spacebuf.WriteTo(&buf)
				spacebuf.Reset()
				wordbuf.WriteTo(&buf)
				wordbuf.Reset()
			}
			spacebuf.WriteRune(char)
		} else {
			wordbuf.WriteRune(char)
			if current+spacebuf.Len()+wordbuf.Len() > width && wordbuf.Len() < width {
				buf.WriteString(wordBreak)
				current = 0
				spacebuf.Reset()
			}
		}
	}

	if wordbuf.Len() == 0 {
		if current+spacebuf.Len() <= width {
			spacebuf.WriteTo(&buf)
		}
	} else {
		spacebuf.WriteTo(&buf)
		wordbuf.WriteTo(&buf)
	}
	return buf.String()
}
