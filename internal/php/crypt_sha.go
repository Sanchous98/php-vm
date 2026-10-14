package php

// SHA-crypt ($5$ / $6$) — порт php_sha256_crypt_r / php_sha512_crypt_r.

import (
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"strconv"
	"strings"
)

func cryptSHA(key, salt string, kind int) string {
	prefix := "$5$"
	ds := 32
	var newh func() hash.Hash = sha256.New
	if kind == 6 {
		prefix = "$6$"
		ds = 64
		newh = sha512.New
	}
	s := salt
	if strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	rounds := 5000
	roundsCustom := false
	if strings.HasPrefix(s, "rounds=") {
		num := s[len("rounds="):]
		end := 0
		for end < len(num) && num[end] >= '0' && num[end] <= '9' {
			end++
		}
		if end > 0 && end < len(num) && num[end] == '$' {
			if v, err := strconv.ParseUint(num[:end], 10, 64); err == nil {
				if v < 1000 || v > 999999999 {
					return "*0"
				}
				rounds = int(v)
				roundsCustom = true
				s = num[end+1:]
			}
		}
	}
	saltLen := 0
	for saltLen < len(s) && s[saltLen] != '$' && saltLen < 16 {
		saltLen++
	}
	salt = s[:saltLen]
	keyLen := len(key)
	kb := []byte(key)
	sb := []byte(salt)

	// ctx = key + salt
	ctx := newh()
	ctx.Write(kb)
	ctx.Write(sb)

	alt := newh()
	alt.Write(kb)
	alt.Write(sb)
	alt.Write(kb)
	altResult := alt.Sum(nil)

	cnt := keyLen
	for ; cnt > ds; cnt -= ds {
		ctx.Write(altResult)
	}
	if cnt > 0 {
		ctx.Write(altResult[:cnt])
	}

	for c := keyLen; c > 0; c >>= 1 {
		if c&1 != 0 {
			ctx.Write(altResult)
		} else {
			ctx.Write(kb)
		}
	}
	altResult = ctx.Sum(nil)

	// P sequence
	alt = newh()
	for c := 0; c < keyLen; c++ {
		alt.Write(kb)
	}
	tempResult := alt.Sum(nil)
	pBytes := repeatTrunc(tempResult, keyLen)

	// S sequence
	alt = newh()
	for c := 0; c < 16+int(altResult[0]); c++ {
		alt.Write(sb)
	}
	tempResult = alt.Sum(nil)
	sBytes := repeatTrunc(tempResult, saltLen)

	for c := 0; c < rounds; c++ {
		ctx = newh()
		if c&1 != 0 {
			ctx.Write(pBytes)
		} else {
			ctx.Write(altResult)
		}
		if c%3 != 0 {
			ctx.Write(sBytes)
		}
		if c%7 != 0 {
			ctx.Write(pBytes)
		}
		if c&1 != 0 {
			ctx.Write(altResult)
		} else {
			ctx.Write(pBytes)
		}
		altResult = ctx.Sum(nil)
	}

	var b strings.Builder
	b.WriteString(prefix)
	if roundsCustom {
		b.WriteString("rounds=" + strconv.Itoa(rounds) + "$")
	}
	b.WriteString(salt)
	b.WriteByte('$')

	b64 := func(b2, b1, b0 byte, n int) {
		w := uint32(b2)<<16 | uint32(b1)<<8 | uint32(b0)
		for ; n > 0; n-- {
			b.WriteByte(itoa64[w&0x3f])
			w >>= 6
		}
	}
	if kind == 6 {
		ai := func(i int) byte { return altResult[i] }
		b64(ai(0), ai(21), ai(42), 4)
		b64(ai(22), ai(43), ai(1), 4)
		b64(ai(44), ai(2), ai(23), 4)
		b64(ai(3), ai(24), ai(45), 4)
		b64(ai(25), ai(46), ai(4), 4)
		b64(ai(47), ai(5), ai(26), 4)
		b64(ai(6), ai(27), ai(48), 4)
		b64(ai(28), ai(49), ai(7), 4)
		b64(ai(50), ai(8), ai(29), 4)
		b64(ai(9), ai(30), ai(51), 4)
		b64(ai(31), ai(52), ai(10), 4)
		b64(ai(53), ai(11), ai(32), 4)
		b64(ai(12), ai(33), ai(54), 4)
		b64(ai(34), ai(55), ai(13), 4)
		b64(ai(56), ai(14), ai(35), 4)
		b64(ai(15), ai(36), ai(57), 4)
		b64(ai(37), ai(58), ai(16), 4)
		b64(ai(59), ai(17), ai(38), 4)
		b64(ai(18), ai(39), ai(60), 4)
		b64(ai(40), ai(61), ai(19), 4)
		b64(ai(62), ai(20), ai(41), 4)
		b64(0, 0, ai(63), 2)
	} else {
		ai := func(i int) byte { return altResult[i] }
		b64(ai(0), ai(10), ai(20), 4)
		b64(ai(21), ai(1), ai(11), 4)
		b64(ai(12), ai(22), ai(2), 4)
		b64(ai(3), ai(13), ai(23), 4)
		b64(ai(24), ai(4), ai(14), 4)
		b64(ai(15), ai(25), ai(5), 4)
		b64(ai(6), ai(16), ai(26), 4)
		b64(ai(27), ai(7), ai(17), 4)
		b64(ai(18), ai(28), ai(8), 4)
		b64(ai(9), ai(19), ai(29), 4)
		b64(0, ai(31), ai(30), 3)
	}
	return b.String()
}

func repeatTrunc(src []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		k := n - len(out)
		if k > len(src) {
			k = len(src)
		}
		out = append(out, src[:k]...)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
