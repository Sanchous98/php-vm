package vm

import (
	"encoding/hex"
	"strconv"
)

func boolString(b bool) string { return strconv.FormatBool(b) }

func hexString(s string) string { return hex.EncodeToString([]byte(s)) }

func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

// sum выполняет сложение в рантайме, чтобы компилятор Go не свернул его как
// константу с произвольной точностью (иначе 0.1+0.2 дало бы ровно 0.3).
//
//go:noinline
func sum(a, b float64) float64 { return a + b }
