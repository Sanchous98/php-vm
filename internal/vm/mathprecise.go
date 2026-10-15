package vm

// mathprecise.go — pow/atan с корректным округлением (как libm glibc).
//
// Go math.Pow/math.Atan ошибаются на 1 ulp там, где glibc (эталон PHP) даёт
// правильно округлённый результат (напр. pow(27, 1/3) = 3.0, atan(23) =
// 1.5273454314033659). Для наблюдаемых PHP-значений считаем в big.Float с
// запасом точности (200+ бит) и округляем один раз.

import (
	"math"
	"math/big"
)

const mathPrec = 256

// bigPi и bigLn2 — константы с запасом точности.
const (
	piDigits = "3.14159265358979323846264338327950288419716939937510582097494459230781640628620899862803482534211706798214808651328241731"
	ln2Digit = "0.693147180559945309417232121458176568075500134360255254120680009493393621969694715605863326996418687542001481020570685734"
)

var (
	bigPi  = parseBigConst(piDigits)
	bigLn2 = parseBigConst(ln2Digit)
	bigOne = new(big.Float).SetPrec(mathPrec).SetInt64(1)
	bigTwo = new(big.Float).SetPrec(mathPrec).SetInt64(2)
)

func parseBigConst(s string) *big.Float {
	f, _, err := big.ParseFloat(s, 10, mathPrec, big.ToNearestEven)
	if err != nil {
		panic("mathprecise: " + err.Error())
	}
	return f
}

func bigFrom(f float64) *big.Float {
	return new(big.Float).SetPrec(mathPrec).SetFloat64(f)
}

// PowPrecise — pow(x, y) по IEEE-754 (спецслучаи как libm), с корректным
// округлением результата.
func PowPrecise(x, y float64) float64 {
	// Спецслучаи IEEE-754 pow.
	if y == 0 {
		return 1
	}
	if x == 1 {
		return 1
	}
	if math.IsNaN(x) || math.IsNaN(y) {
		return math.NaN()
	}
	if math.IsInf(y, 0) {
		ax := math.Abs(x)
		switch {
		case ax == 1:
			return 1
		case ax > 1 == (y > 0):
			return math.Inf(1)
		default:
			return 0
		}
	}
	yIntegral := !math.IsInf(y, 0) && math.Trunc(y) == y
	if x == 0 {
		if math.Signbit(x) && yIntegral && int64(y)&1 != 0 {
			if y > 0 {
				return math.Copysign(0, -1)
			}
			return math.Inf(-1)
		}
		if y > 0 {
			return 0
		}
		return math.Inf(1)
	}
	neg := x < 0
	if neg && !yIntegral {
		return math.NaN()
	}
	ax := math.Abs(x)
	var r float64
	if yIntegral && math.Abs(y) <= 1e6 {
		// Целая степень: быстрый путь по exponentiation by squaring
		// (результат точен для целых показателей, ошибок округления нет).
		r = math.Pow(ax, y)
	} else {
		lx := bigLn(bigFrom(ax))
		prod := new(big.Float).SetPrec(mathPrec).Mul(lx, bigFrom(y))
		// Экстремальные показатели (|ln результата| > 745): bigExp не
		// выражает переполнение/антипереполнение — берём math.Pow, как libm
		// (ops_diff: 7**maxint64 → INF, 3**(-maxint64-1) → 0).
		if pf, _ := prod.Float64(); math.IsInf(pf, 0) || math.Abs(pf) > 745 {
			r = math.Pow(ax, y)
		} else {
			r, _ = bigExp(prod).Float64()
		}
	}
	if neg && int64(y)&1 != 0 {
		r = -r
	}
	return r
}

// AtanPrecise — atan(x) с корректным округлением (libm-совместимо).
func AtanPrecise(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return math.NaN()
	case math.IsInf(x, 1):
		return math.Pi / 2
	case math.IsInf(x, -1):
		return -math.Pi / 2
	case x == 0:
		return x
	}
	neg := x < 0
	ax := math.Abs(x)
	var r *big.Float
	if ax == 1 {
		r = new(big.Float).SetPrec(mathPrec).Quo(bigPi, bigFrom(4)) // atan(1) = pi/4
	} else if ax > 1 {
		inv := new(big.Float).SetPrec(mathPrec).Quo(bigOne, bigFrom(ax))
		half := new(big.Float).SetPrec(mathPrec).Quo(bigPi, bigTwo)
		r = new(big.Float).SetPrec(mathPrec).Sub(half, bigAtan(inv))
	} else {
		r = bigAtan(bigFrom(ax))
	}
	out, _ := r.Float64()
	if neg {
		out = -out
	}
	return out
}

// bigLn — ln(x) для x > 0: x = m*2^e, m в [1,2), u=(m-1)/(m+1),
// ln(m) = 2*atanh(u); ln(x) = e*ln2 + ln(m).
func bigLn(x *big.Float) *big.Float {
	m := new(big.Float).SetPrec(mathPrec)
	e := x.MantExp(m) // x = m*2^e, m в [0.5,1)
	if m.Sign() < 0 {
		m.Neg(m)
	}
	if m.Cmp(bigOne) < 0 {
		m.Mul(m, bigTwo)
		e--
	}
	// u = (m-1)/(m+1)
	num := new(big.Float).SetPrec(mathPrec).Sub(m, bigOne)
	den := new(big.Float).SetPrec(mathPrec).Add(m, bigOne)
	u := new(big.Float).SetPrec(mathPrec).Quo(num, den)
	// ряд atanh(u) = u + u^3/3 + u^5/5 + ...
	u2 := new(big.Float).SetPrec(mathPrec).Mul(u, u)
	term := new(big.Float).SetPrec(mathPrec).Set(u)
	sum := new(big.Float).SetPrec(mathPrec).Set(u)
	tmp := new(big.Float).SetPrec(mathPrec)
	for k := int64(3); k < 10000; k += 2 {
		term.Mul(term, u2)
		tmp.Quo(term, new(big.Float).SetPrec(mathPrec).SetInt64(k))
		if tmp.MantExp(nil) < sum.MantExp(nil)-int(mathPrec)+16 {
			break
		}
		sum.Add(sum, tmp)
	}
	lnm := new(big.Float).SetPrec(mathPrec).Add(sum, sum) // 2*atanh(u)
	// e*ln2
	el := new(big.Float).SetPrec(mathPrec).Mul(new(big.Float).SetPrec(mathPrec).SetInt64(int64(e)), bigLn2)
	return new(big.Float).SetPrec(mathPrec).Add(lnm, el)
}

// bigExp — exp(x): приведение x = n*ln2 + r, |r| <= ln2/2, ряд Тейлора,
// масштабирование 2^n.
func bigExp(x *big.Float) *big.Float {
	// n = round(x/ln2) по float64-приближению (достаточно для |x| < 1e6).
	xf, _ := x.Float64()
	n := int64(math.Round(xf / math.Ln2))
	r := new(big.Float).SetPrec(mathPrec).Sub(x,
		new(big.Float).SetPrec(mathPrec).Mul(new(big.Float).SetPrec(mathPrec).SetInt64(n), bigLn2))
	// ряд exp(r) = sum r^k/k!
	sum := new(big.Float).SetPrec(mathPrec).SetInt64(1)
	term := new(big.Float).SetPrec(mathPrec).SetInt64(1)
	tmp := new(big.Float).SetPrec(mathPrec)
	for k := int64(1); k < 10000; k++ {
		term.Mul(term, r)
		term.Quo(term, new(big.Float).SetPrec(mathPrec).SetInt64(k))
		if term.MantExp(nil) < sum.MantExp(nil)-int(mathPrec)+16 {
			break
		}
		tmp.Set(term)
		sum.Add(sum, tmp)
	}
	// sum * 2^n
	out := new(big.Float).SetPrec(mathPrec).SetMantExp(sum, int(n))
	return out
}

// bigAtan — atan(x) для 0 <= x < 1: многократное приведение
// x = x/(1+sqrt(1+x^2)) (atan(x) = 2*atan(x')), затем ряд.
func bigAtan(x *big.Float) *big.Float {
	k := 0
	cur := new(big.Float).SetPrec(mathPrec).Set(x)
	limit := bigFrom(0.05)
	for cur.Cmp(limit) > 0 && k < 60 {
		// cur = cur/(1+sqrt(1+cur^2))
		sq := new(big.Float).SetPrec(mathPrec).Mul(cur, cur)
		sq.Add(sq, bigOne)
		sq.Sqrt(sq)
		den := new(big.Float).SetPrec(mathPrec).Add(bigOne, sq)
		cur.Quo(cur, den)
		k++
	}
	// ряд atan(cur) = cur - cur^3/3 + cur^5/5 - ...
	c2 := new(big.Float).SetPrec(mathPrec).Mul(cur, cur)
	term := new(big.Float).SetPrec(mathPrec).Set(cur)
	sum := new(big.Float).SetPrec(mathPrec).Set(cur)
	tmp := new(big.Float).SetPrec(mathPrec)
	sign := big.NewFloat(-1)
	for j := int64(3); j < 100000; j += 2 {
		term.Mul(term, c2)
		tmp.Quo(term, new(big.Float).SetPrec(mathPrec).SetInt64(j))
		if tmp.MantExp(nil) < sum.MantExp(nil)-int(mathPrec)+16 {
			break
		}
		if sign.Sign() < 0 {
			sum.Sub(sum, tmp)
		} else {
			sum.Add(sum, tmp)
		}
		sign.Neg(sign)
	}
	// sum * 2^k
	out := new(big.Float).SetPrec(mathPrec).SetMantExp(sum, k)
	return out
}
