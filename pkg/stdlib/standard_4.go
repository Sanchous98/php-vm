package stdlib

import (
	"fmt"
	"time"
)

func Microtime(asFloat bool) (float64, string) {
	loc, _ := time.LoadLocation("UTC")
	now := time.Now().In(loc)
	micSeconds := float64(now.Nanosecond()) / 1000000000

	if asFloat {
		return float64(now.Unix()) + micSeconds, ""
	}

	return 0, fmt.Sprintf("%s %d", now.Format("0.99999999"), now.Unix())
}
