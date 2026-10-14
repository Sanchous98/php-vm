package php

// Порт crypt_blowfish.c (php-src ext/standard/crypt_blowfish.c).

const bfN = 16

const bfItoa64 = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var bfAtoi64 = [0x60]int{
	64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 0, 1,
	54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 64, 64, 64, 64, 64,
	64, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 64, 64, 64, 64, 64,
	64, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42,
	43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 64, 64, 64, 64, 64,
}

// flags_by_subtype: индекс — setting[2]-'a'.
var bfFlagsBySubtype = [26]uint32{2, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 4, 0}

type bfCtx struct {
	S [4][256]uint32
	P [18]uint32
}

func bfSwap(x []uint32) {
	for i := range x {
		tmp := x[i]
		tmp = (tmp << 16) | (tmp >> 16)
		x[i] = ((tmp & 0x00FF00FF) << 8) | ((tmp >> 8) & 0x00FF00FF)
	}
}

func bfSafeAtoi64(src byte) (int, bool) {
	tmp := int(src) - 0x20
	if tmp < 0 || tmp >= 0x60 {
		return 0, false
	}
	v := bfAtoi64[tmp]
	if v > 63 {
		return 0, false
	}
	return v, true
}

// bfDecode декодирует base64 из src в size байт dst (как 4-символьные группы).
func bfDecode(dst []byte, src string, size int) bool {
	di := 0
	si := 0
	next := func() (int, bool) {
		if si >= len(src) {
			return 0, false
		}
		v, ok := bfSafeAtoi64(src[si])
		si++
		return v, ok
	}
	for {
		c1, ok := next()
		if !ok {
			return false
		}
		c2, ok := next()
		if !ok {
			return false
		}
		dst[di] = byte((c1 << 2) | ((c2 & 0x30) >> 4))
		di++
		if di >= size {
			break
		}
		c3, ok := next()
		if !ok {
			return false
		}
		dst[di] = byte(((c2 & 0x0F) << 4) | ((c3 & 0x3C) >> 2))
		di++
		if di >= size {
			break
		}
		c4, ok := next()
		if !ok {
			return false
		}
		dst[di] = byte(((c3 & 0x03) << 6) | c4)
		di++
		if di >= size {
			break
		}
	}
	return true
}

func bfEncode(dst []byte, src []byte, size int) int {
	di := 0
	si := 0
	for {
		c1 := int(src[si])
		si++
		dst[di] = bfItoa64[c1>>2]
		di++
		c1 = (c1 & 0x03) << 4
		if si >= size {
			dst[di] = bfItoa64[c1]
			di++
			break
		}
		c2 := int(src[si])
		si++
		c1 |= c2 >> 4
		dst[di] = bfItoa64[c1]
		di++
		c1 = (c2 & 0x0f) << 2
		if si >= size {
			dst[di] = bfItoa64[c1]
			di++
			break
		}
		c2 = int(src[si])
		si++
		c1 |= c2 >> 6
		dst[di] = bfItoa64[c1]
		di++
		dst[di] = bfItoa64[c2&0x3f]
		di++
		if si >= size {
			break
		}
	}
	return di
}

// bfRound изменяет R, используя L.
func (c *bfCtx) bfRound(L, R uint32, n int) uint32 {
	t1 := c.S[3][L&0xFF]
	t2 := c.S[2][(L>>8)&0xFF]
	t3 := c.S[1][(L>>16)&0xFF] + c.S[0][(L>>24)&0xFF]
	t3 ^= t2
	R ^= c.P[n+1]
	t3 += t1
	R ^= t3
	return R
}

func (c *bfCtx) encrypt(L, R uint32) (uint32, uint32) {
	L ^= c.P[0]
	for n := 0; n < 16; n++ {
		if n&1 == 0 {
			R = c.bfRound(L, R, n)
		} else {
			L = c.bfRound(R, L, n)
		}
	}
	tmp4 := R
	R = L
	L = tmp4 ^ c.P[bfN+1]
	return L, R
}

func (c *bfCtx) body() {
	L, R := uint32(0), uint32(0)
	for i := 0; i < bfN+2; i += 2 {
		L, R = c.encrypt(L, R)
		c.P[i], c.P[i+1] = L, R
	}
	// S-box, flat index pi over 1024 words, группы по 4.
	pi := 0
	for {
		L, R = c.encrypt(L, R)
		c.S[pi/256][pi%256] = L
		c.S[(pi+1)/256][(pi+1)%256] = R
		L, R = c.encrypt(L, R)
		c.S[(pi+2)/256][(pi+2)%256] = L
		c.S[(pi+3)/256][(pi+3)%256] = R
		pi += 4
		if pi >= 1023 {
			break
		}
	}
}

func bfSetKey(key string, expanded, initial *[18]uint32, flags uint32) {
	bug := flags & 1
	safety := (flags & 2) << 15
	var sign, diff uint32
	ptr := 0
	for i := 0; i < bfN+2; i++ {
		var tmp0, tmp1 uint32
		for j := 0; j < 4; j++ {
			var ch byte
			if ptr < len(key) {
				ch = key[ptr]
			}
			tmp0 = tmp0<<8 | uint32(ch)
			tmp1 = tmp1<<8 | uint32(int32(int8(ch)))
			if j > 0 {
				sign |= tmp1 & 0x80
			}
			if ch == 0 {
				ptr = 0
			} else {
				ptr++
			}
		}
		diff |= tmp0 ^ tmp1
		if bug == 1 {
			expanded[i] = tmp1
			initial[i] = bfInitP[i] ^ tmp1
		} else {
			expanded[i] = tmp0
			initial[i] = bfInitP[i] ^ tmp0
		}
	}
	diff |= diff >> 16
	diff &= 0xffff
	diff += 0xffff
	sign <<= 9
	sign &= ^diff & safety
	initial[0] ^= sign
}

var bfMagicW = [6]uint32{0x4F727068, 0x65616E42, 0x65686F6C, 0x64657253, 0x63727944, 0x6F756274}

// bfCrypt возвращает "" при ошибке.
func bfCrypt(key, setting string) string {
	if len(setting) < 7 ||
		setting[0] != '$' || setting[1] != '2' ||
		setting[2] < 'a' || setting[2] > 'z' ||
		bfFlagsBySubtype[setting[2]-'a'] == 0 ||
		setting[3] != '$' ||
		setting[4] < '0' || setting[4] > '3' ||
		setting[5] < '0' || setting[5] > '9' ||
		(setting[4] == '3' && setting[5] > '1') ||
		setting[6] != '$' {
		return ""
	}
	count := uint32(1) << uint((setting[4]-'0')*10+(setting[5]-'0'))
	if count < 16 {
		return ""
	}
	var saltBytes [16]byte
	if !bfDecode(saltBytes[:], setting[7:], 16) {
		return ""
	}
	var salt [4]uint32
	for i := 0; i < 4; i++ {
		salt[i] = uint32(saltBytes[i*4]) | uint32(saltBytes[i*4+1])<<8 |
			uint32(saltBytes[i*4+2])<<16 | uint32(saltBytes[i*4+3])<<24
	}
	bfSwap(salt[:])

	flags := bfFlagsBySubtype[setting[2]-'a']
	var expanded, initial [18]uint32
	bfSetKey(key, &expanded, &initial, flags)

	var ctx bfCtx
	ctx.S = bfInitS
	ctx.P = initial

	L, R := uint32(0), uint32(0)
	for i := 0; i < bfN+2; i += 2 {
		L ^= salt[i&2]
		R ^= salt[(i&2)+1]
		L, R = ctx.encrypt(L, R)
		ctx.P[i], ctx.P[i+1] = L, R
	}
	pi := 0
	for {
		L ^= salt[2]
		R ^= salt[3]
		L, R = ctx.encrypt(L, R)
		ctx.S[pi/256][pi%256] = L
		ctx.S[(pi+1)/256][(pi+1)%256] = R
		L ^= salt[0]
		R ^= salt[1]
		L, R = ctx.encrypt(L, R)
		ctx.S[(pi+2)/256][(pi+2)%256] = L
		ctx.S[(pi+3)/256][(pi+3)%256] = R
		pi += 4
		if pi >= 1023 {
			break
		}
	}

	for {
		for i := 0; i < bfN+2; i += 2 {
			ctx.P[i] ^= expanded[i]
			ctx.P[i+1] ^= expanded[i+1]
		}
		done := false
		for {
			ctx.body()
			if done {
				break
			}
			done = true
			t1, t2, t3, t4 := salt[0], salt[1], salt[2], salt[3]
			for i := 0; i < bfN; i += 4 {
				ctx.P[i] ^= t1
				ctx.P[i+1] ^= t2
				ctx.P[i+2] ^= t3
				ctx.P[i+3] ^= t4
			}
			ctx.P[16] ^= t1
			ctx.P[17] ^= t2
		}
		count--
		if count == 0 {
			break
		}
	}

	var output [6]uint32
	for i := 0; i < 6; i += 2 {
		L = bfMagicW[i]
		R = bfMagicW[i+1]
		for k := 0; k < 64; k++ {
			L, R = ctx.encrypt(L, R)
		}
		output[i], output[i+1] = L, R
	}

	out := make([]byte, 60)
	copy(out, setting[:28])
	v, ok := bfSafeAtoi64(setting[28])
	if !ok {
		return ""
	}
	out[28] = bfItoa64[v&0x30]
	bfSwap(output[:])
	var tmp [31]byte
	raw := make([]byte, 24)
	for i := 0; i < 6; i++ {
		raw[i*4] = byte(output[i])
		raw[i*4+1] = byte(output[i] >> 8)
		raw[i*4+2] = byte(output[i] >> 16)
		raw[i*4+3] = byte(output[i] >> 24)
	}
	n := bfEncode(tmp[:], raw, 23)
	if n != 31 {
		return ""
	}
	copy(out[29:], tmp[:])
	return string(out)
}

// cryptBlowfish — диспетчер Blowfish для php_crypt. "" при ошибке/неприменимости.
func cryptBlowfish(pw, salt string) string {
	if len(salt) >= 4 && salt[0] == '$' && salt[1] == '2' && salt[3] == '$' && salt[2] != 0 {
		return bfCrypt(pw, salt)
	}
	return ""
}
