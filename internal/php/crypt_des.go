package php

// Порт crypt_freesec.c (php-src ext/standard/crypt_freesec.c) — традиционный
// DES crypt и extended DES.

import "strings"

var (
	desIP = [64]int{
		58, 50, 42, 34, 26, 18, 10, 2, 60, 52, 44, 36, 28, 20, 12, 4,
		62, 54, 46, 38, 30, 22, 14, 6, 64, 56, 48, 40, 32, 24, 16, 8,
		57, 49, 41, 33, 25, 17, 9, 1, 59, 51, 43, 35, 27, 19, 11, 3,
		61, 53, 45, 37, 29, 21, 13, 5, 63, 55, 47, 39, 31, 23, 15, 7,
	}
	desKeyPerm = [56]int{
		57, 49, 41, 33, 25, 17, 9, 1, 58, 50, 42, 34, 26, 18,
		10, 2, 59, 51, 43, 35, 27, 19, 11, 3, 60, 52, 44, 36,
		63, 55, 47, 39, 31, 23, 15, 7, 62, 54, 46, 38, 30, 22,
		14, 6, 61, 53, 45, 37, 29, 21, 13, 5, 28, 20, 12, 4,
	}
	desKeyShifts = [16]int{1, 1, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 1}
	desCompPerm  = [48]int{
		14, 17, 11, 24, 1, 5, 3, 28, 15, 6, 21, 10,
		23, 19, 12, 4, 26, 8, 16, 7, 27, 20, 13, 2,
		41, 52, 31, 37, 47, 55, 30, 40, 51, 45, 33, 48,
		44, 49, 39, 56, 34, 53, 46, 42, 50, 36, 29, 32,
	}
	desSbox = [8][64]int{
		{
			14, 4, 13, 1, 2, 15, 11, 8, 3, 10, 6, 12, 5, 9, 0, 7,
			0, 15, 7, 4, 14, 2, 13, 1, 10, 6, 12, 11, 9, 5, 3, 8,
			4, 1, 14, 8, 13, 6, 2, 11, 15, 12, 9, 7, 3, 10, 5, 0,
			15, 12, 8, 2, 4, 9, 1, 7, 5, 11, 3, 14, 10, 0, 6, 13,
		},
		{
			15, 1, 8, 14, 6, 11, 3, 4, 9, 7, 2, 13, 12, 0, 5, 10,
			3, 13, 4, 7, 15, 2, 8, 14, 12, 0, 1, 10, 6, 9, 11, 5,
			0, 14, 7, 11, 10, 4, 13, 1, 5, 8, 12, 6, 9, 3, 2, 15,
			13, 8, 10, 1, 3, 15, 4, 2, 11, 6, 7, 12, 0, 5, 14, 9,
		},
		{
			10, 0, 9, 14, 6, 3, 15, 5, 1, 13, 12, 7, 11, 4, 2, 8,
			13, 7, 0, 9, 3, 4, 6, 10, 2, 8, 5, 14, 12, 11, 15, 1,
			13, 6, 4, 9, 8, 15, 3, 0, 11, 1, 2, 12, 5, 10, 14, 7,
			1, 10, 13, 0, 6, 9, 8, 7, 4, 15, 14, 3, 11, 5, 2, 12,
		},
		{
			7, 13, 14, 3, 0, 6, 9, 10, 1, 2, 8, 5, 11, 12, 4, 15,
			13, 8, 11, 5, 6, 15, 0, 3, 4, 7, 2, 12, 1, 10, 14, 9,
			10, 6, 9, 0, 12, 11, 7, 13, 15, 1, 3, 14, 5, 2, 8, 4,
			3, 15, 0, 6, 10, 1, 13, 8, 9, 4, 5, 11, 12, 7, 2, 14,
		},
		{
			2, 12, 4, 1, 7, 10, 11, 6, 8, 5, 3, 15, 13, 0, 14, 9,
			14, 11, 2, 12, 4, 7, 13, 1, 5, 0, 15, 10, 3, 9, 8, 6,
			4, 2, 1, 11, 10, 13, 7, 8, 15, 9, 12, 5, 6, 3, 0, 14,
			11, 8, 12, 7, 1, 14, 2, 13, 6, 15, 0, 9, 10, 4, 5, 3,
		},
		{
			12, 1, 10, 15, 9, 2, 6, 8, 0, 13, 3, 4, 14, 7, 5, 11,
			10, 15, 4, 2, 7, 12, 9, 5, 6, 1, 13, 14, 0, 11, 3, 8,
			9, 14, 15, 5, 2, 8, 12, 3, 7, 0, 4, 10, 1, 13, 11, 6,
			4, 3, 2, 12, 9, 5, 15, 10, 11, 14, 1, 7, 6, 0, 8, 13,
		},
		{
			4, 11, 2, 14, 15, 0, 8, 13, 3, 12, 9, 7, 5, 10, 6, 1,
			13, 0, 11, 7, 4, 9, 1, 10, 14, 3, 5, 12, 2, 15, 8, 6,
			1, 4, 11, 13, 12, 3, 7, 14, 10, 15, 6, 8, 0, 5, 9, 2,
			6, 11, 13, 8, 1, 4, 10, 7, 9, 5, 0, 15, 14, 2, 3, 12,
		},
		{
			13, 2, 8, 4, 6, 15, 11, 1, 10, 9, 3, 14, 5, 0, 12, 7,
			1, 15, 13, 8, 10, 3, 7, 4, 12, 5, 6, 11, 0, 14, 9, 2,
			7, 11, 4, 1, 9, 12, 14, 2, 0, 6, 10, 13, 15, 3, 5, 8,
			2, 1, 14, 7, 4, 10, 8, 13, 15, 12, 9, 0, 3, 5, 6, 11,
		},
	}
	desPbox = [32]int{
		16, 7, 20, 21, 29, 12, 28, 17, 1, 15, 23, 26, 5, 18, 31, 10,
		2, 8, 24, 14, 32, 27, 3, 9, 19, 13, 30, 6, 22, 11, 4, 25,
	}
	desBits32 = [32]uint32{
		0x80000000, 0x40000000, 0x20000000, 0x10000000,
		0x08000000, 0x04000000, 0x02000000, 0x01000000,
		0x00800000, 0x00400000, 0x00200000, 0x00100000,
		0x00080000, 0x00040000, 0x00020000, 0x00010000,
		0x00008000, 0x00004000, 0x00002000, 0x00001000,
		0x00000800, 0x00000400, 0x00000200, 0x00000100,
		0x00000080, 0x00000040, 0x00000020, 0x00000010,
		0x00000008, 0x00000004, 0x00000002, 0x00000001,
	}
	desBits8 = [8]uint32{0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01}

	desMSbox        [4][4096]uint32
	desPSbox        [4][256]uint32
	desIPMaskL      [8][256]uint32
	desIPMaskR      [8][256]uint32
	desFPMaskL      [8][256]uint32
	desFPMaskR      [8][256]uint32
	desKeyPermMaskL [8][128]uint32
	desKeyPermMaskR [8][128]uint32
	desCompMaskL    [8][128]uint32
	desCompMaskR    [8][128]uint32
)

func desBits28() []uint32 { return desBits32[4:] }
func desBits24() []uint32 { return desBits32[8:] }

func init() {
	var invKeyPerm [64]int
	var invCompPerm [56]int
	var initPerm, finalPerm [64]int
	var uSbox [8][64]int
	var unPbox [32]int

	bits28 := desBits28()
	bits24 := desBits24()

	for i := 0; i < 8; i++ {
		for j := 0; j < 64; j++ {
			b := (j & 0x20) | ((j & 1) << 4) | ((j >> 1) & 0xf)
			uSbox[i][j] = desSbox[i][b]
		}
	}
	for b := 0; b < 4; b++ {
		for i := 0; i < 64; i++ {
			for j := 0; j < 64; j++ {
				desMSbox[b][(i<<6)|j] = uint32(uSbox[b<<1][i]<<4) | uint32(uSbox[(b<<1)+1][j])
			}
		}
	}
	for i := 0; i < 64; i++ {
		finalPerm[i] = desIP[i] - 1
		initPerm[finalPerm[i]] = i
		invKeyPerm[i] = 255
	}
	for i := 0; i < 56; i++ {
		invKeyPerm[desKeyPerm[i]-1] = i
		invCompPerm[i] = 255
	}
	for i := 0; i < 48; i++ {
		invCompPerm[desCompPerm[i]-1] = i
	}
	for k := 0; k < 8; k++ {
		for i := 0; i < 256; i++ {
			var il, ir, fl, fr uint32
			for j := 0; j < 8; j++ {
				inbit := 8*k + j
				if i&int(desBits8[j]) != 0 {
					if obit := initPerm[inbit]; obit < 32 {
						il |= desBits32[obit]
					} else {
						ir |= desBits32[obit-32]
					}
					if obit := finalPerm[inbit]; obit < 32 {
						fl |= desBits32[obit]
					} else {
						fr |= desBits32[obit-32]
					}
				}
			}
			desIPMaskL[k][i], desIPMaskR[k][i] = il, ir
			desFPMaskL[k][i], desFPMaskR[k][i] = fl, fr
		}
		for i := 0; i < 128; i++ {
			var il, ir, cl, cr uint32
			for j := 0; j < 7; j++ {
				inbit := 8*k + j
				if i&int(desBits8[j+1]) != 0 {
					if obit := invKeyPerm[inbit]; obit != 255 {
						if obit < 28 {
							il |= bits28[obit]
						} else {
							ir |= bits28[obit-28]
						}
					}
				}
				inbit2 := 7*k + j
				if i&int(desBits8[j+1]) != 0 {
					if obit := invCompPerm[inbit2]; obit != 255 {
						if obit < 24 {
							cl |= bits24[obit]
						} else {
							cr |= bits24[obit-24]
						}
					}
				}
			}
			desKeyPermMaskL[k][i], desKeyPermMaskR[k][i] = il, ir
			desCompMaskL[k][i], desCompMaskR[k][i] = cl, cr
		}
	}
	for i := 0; i < 32; i++ {
		unPbox[desPbox[i]-1] = i
	}
	for b := 0; b < 4; b++ {
		for i := 0; i < 256; i++ {
			var p uint32
			for j := 0; j < 8; j++ {
				if i&int(desBits8[j]) != 0 {
					p |= desBits32[unPbox[8*b+j]]
				}
			}
			desPSbox[b][i] = p
		}
	}
}

type desData struct {
	oldRawkey0, oldRawkey1 uint32
	saltbits               uint32
	oldSalt                uint32
	enKeysl, enKeysr       [16]uint32
	deKeysl, deKeysr       [16]uint32
}

const desAscii64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func desAsciiToBin(ch byte) int {
	sch := int(int8(ch))
	retval := sch - '.'
	if sch >= 'A' {
		retval = sch - ('A' - 12)
		if sch >= 'a' {
			retval = sch - ('a' - 38)
		}
	}
	return retval & 0x3f
}

func desAsciiUnsafe(ch byte) bool { return ch == 0 || ch == '\n' || ch == ':' }

func (d *desData) setupSalt(salt uint32) {
	if salt == d.oldSalt {
		return
	}
	d.oldSalt = salt
	var saltbits uint32
	saltbit := uint32(1)
	obit := uint32(0x800000)
	for i := 0; i < 24; i++ {
		if salt&saltbit != 0 {
			saltbits |= obit
		}
		saltbit <<= 1
		obit >>= 1
	}
	d.saltbits = saltbits
}

func (d *desData) setkey(key []byte) {
	rawkey0 := uint32(key[3]) | uint32(key[2])<<8 | uint32(key[1])<<16 | uint32(key[0])<<24
	rawkey1 := uint32(key[7]) | uint32(key[6])<<8 | uint32(key[5])<<16 | uint32(key[4])<<24
	if (rawkey0|rawkey1) != 0 && rawkey0 == d.oldRawkey0 && rawkey1 == d.oldRawkey1 {
		return
	}
	d.oldRawkey0, d.oldRawkey1 = rawkey0, rawkey1
	k0 := desKeyPermMaskL[0][rawkey0>>25] |
		desKeyPermMaskL[1][(rawkey0>>17)&0x7f] |
		desKeyPermMaskL[2][(rawkey0>>9)&0x7f] |
		desKeyPermMaskL[3][(rawkey0>>1)&0x7f] |
		desKeyPermMaskL[4][rawkey1>>25] |
		desKeyPermMaskL[5][(rawkey1>>17)&0x7f] |
		desKeyPermMaskL[6][(rawkey1>>9)&0x7f] |
		desKeyPermMaskL[7][(rawkey1>>1)&0x7f]
	k1 := desKeyPermMaskR[0][rawkey0>>25] |
		desKeyPermMaskR[1][(rawkey0>>17)&0x7f] |
		desKeyPermMaskR[2][(rawkey0>>9)&0x7f] |
		desKeyPermMaskR[3][(rawkey0>>1)&0x7f] |
		desKeyPermMaskR[4][rawkey1>>25] |
		desKeyPermMaskR[5][(rawkey1>>17)&0x7f] |
		desKeyPermMaskR[6][(rawkey1>>9)&0x7f] |
		desKeyPermMaskR[7][(rawkey1>>1)&0x7f]
	shifts := 0
	for round := 0; round < 16; round++ {
		shifts += desKeyShifts[round]
		t0 := (k0 << shifts) | (k0 >> (28 - shifts))
		t1 := (k1 << shifts) | (k1 >> (28 - shifts))
		cl := desCompMaskL[0][(t0>>21)&0x7f] | desCompMaskL[1][(t0>>14)&0x7f] |
			desCompMaskL[2][(t0>>7)&0x7f] | desCompMaskL[3][t0&0x7f] |
			desCompMaskL[4][(t1>>21)&0x7f] | desCompMaskL[5][(t1>>14)&0x7f] |
			desCompMaskL[6][(t1>>7)&0x7f] | desCompMaskL[7][t1&0x7f]
		cr := desCompMaskR[0][(t0>>21)&0x7f] | desCompMaskR[1][(t0>>14)&0x7f] |
			desCompMaskR[2][(t0>>7)&0x7f] | desCompMaskR[3][t0&0x7f] |
			desCompMaskR[4][(t1>>21)&0x7f] | desCompMaskR[5][(t1>>14)&0x7f] |
			desCompMaskR[6][(t1>>7)&0x7f] | desCompMaskR[7][t1&0x7f]
		d.enKeysl[round], d.deKeysl[15-round] = cl, cl
		d.enKeysr[round], d.deKeysr[15-round] = cr, cr
	}
}

func (d *desData) doDes(lIn, rIn uint32, count int) (uint32, uint32) {
	var kl, kr *[16]uint32
	if count > 0 {
		kl, kr = &d.enKeysl, &d.enKeysr
	} else {
		count = -count
		kl, kr = &d.deKeysl, &d.deKeysr
	}
	l := desIPMaskL[0][lIn>>24] | desIPMaskL[1][(lIn>>16)&0xff] |
		desIPMaskL[2][(lIn>>8)&0xff] | desIPMaskL[3][lIn&0xff] |
		desIPMaskL[4][rIn>>24] | desIPMaskL[5][(rIn>>16)&0xff] |
		desIPMaskL[6][(rIn>>8)&0xff] | desIPMaskL[7][rIn&0xff]
	r := desIPMaskR[0][lIn>>24] | desIPMaskR[1][(lIn>>16)&0xff] |
		desIPMaskR[2][(lIn>>8)&0xff] | desIPMaskR[3][lIn&0xff] |
		desIPMaskR[4][rIn>>24] | desIPMaskR[5][(rIn>>16)&0xff] |
		desIPMaskR[6][(rIn>>8)&0xff] | desIPMaskR[7][rIn&0xff]
	saltbits := d.saltbits
	var f uint32
	for count > 0 {
		count--
		for round := 0; round < 16; round++ {
			r48l := ((r & 0x00000001) << 23) | ((r & 0xf8000000) >> 9) |
				((r & 0x1f800000) >> 11) | ((r & 0x01f80000) >> 13) | ((r & 0x001f8000) >> 15)
			r48r := ((r & 0x0001f800) << 7) | ((r & 0x00001f80) << 5) |
				((r & 0x000001f8) << 3) | ((r & 0x0000001f) << 1) | ((r & 0x80000000) >> 31)
			f = (r48l ^ r48r) & saltbits
			r48l ^= f ^ kl[round]
			r48r ^= f ^ kr[round]
			f = desPSbox[0][desMSbox[0][r48l>>12]] |
				desPSbox[1][desMSbox[1][r48l&0xfff]] |
				desPSbox[2][desMSbox[2][r48r>>12]] |
				desPSbox[3][desMSbox[3][r48r&0xfff]]
			f ^= l
			l = r
			r = f
		}
		r = l
		l = f
	}
	lOut := desFPMaskL[0][l>>24] | desFPMaskL[1][(l>>16)&0xff] |
		desFPMaskL[2][(l>>8)&0xff] | desFPMaskL[3][l&0xff] |
		desFPMaskL[4][r>>24] | desFPMaskL[5][(r>>16)&0xff] |
		desFPMaskL[6][(r>>8)&0xff] | desFPMaskL[7][r&0xff]
	rOut := desFPMaskR[0][l>>24] | desFPMaskR[1][(l>>16)&0xff] |
		desFPMaskR[2][(l>>8)&0xff] | desFPMaskR[3][l&0xff] |
		desFPMaskR[4][r>>24] | desFPMaskR[5][(r>>16)&0xff] |
		desFPMaskR[6][(r>>8)&0xff] | desFPMaskR[7][r&0xff]
	return lOut, rOut
}

func (d *desData) cipher(in []byte, salt uint32, count int) []byte {
	d.setupSalt(salt)
	rawl := uint32(in[3]) | uint32(in[2])<<8 | uint32(in[1])<<16 | uint32(in[0])<<24
	rawr := uint32(in[7]) | uint32(in[6])<<8 | uint32(in[5])<<16 | uint32(in[4])<<24
	lOut, rOut := d.doDes(rawl, rawr, count)
	out := make([]byte, 8)
	out[0] = byte(lOut >> 24)
	out[1] = byte(lOut >> 16)
	out[2] = byte(lOut >> 8)
	out[3] = byte(lOut)
	out[4] = byte(rOut >> 24)
	out[5] = byte(rOut >> 16)
	out[6] = byte(rOut >> 8)
	out[7] = byte(rOut)
	return out
}

// cryptDESExtended реализует _crypt_extended_r. Возвращает "" при ошибке.
func cryptDESExtended(key, setting string) string {
	d := &desData{}
	keybuf := make([]byte, 8)
	ki := 0
	for i := 0; i < 8; i++ {
		var c byte
		if ki < len(key) {
			c = key[ki]
		}
		keybuf[i] = c << 1
		if c != 0 {
			ki++
		}
	}
	d.setkey(keybuf)

	var count uint32
	var salt uint32
	var out []byte
	if len(setting) > 0 && setting[0] == '_' {
		if len(setting) < 9 {
			return ""
		}
		count = 0
		for i := 1; i < 5; i++ {
			value := desAsciiToBin(setting[i])
			if desAscii64[value] != setting[i] {
				return ""
			}
			count |= uint32(value) << uint((i-1)*6)
		}
		if count == 0 {
			return ""
		}
		for i := 5; i < 9; i++ {
			value := desAsciiToBin(setting[i])
			if desAscii64[value] != setting[i] {
				return ""
			}
			salt |= uint32(value) << uint((i-5)*6)
		}
		// encrypt key with itself
		for ki < len(key) && key[ki] != 0 {
			keybuf = d.cipher(keybuf, 0, 1)
			q := 0
			for q < 8 && ki < len(key) && key[ki] != 0 {
				keybuf[q] ^= key[ki] << 1
				ki++
				q++
			}
			d.setkey(keybuf)
		}
		out = make([]byte, 0, 20)
		out = append(out, setting[:9]...)
	} else {
		count = 25
		if len(setting) < 2 || desAsciiUnsafe(setting[0]) || desAsciiUnsafe(setting[1]) {
			return ""
		}
		salt = uint32(desAsciiToBin(setting[1]))<<6 | uint32(desAsciiToBin(setting[0]))
		out = append(out, setting[0], setting[1])
	}
	d.setupSalt(salt)
	r0, r1 := d.doDes(0, 0, int(count))
	l := r0 >> 8
	out = append(out,
		desAscii64[(l>>18)&0x3f],
		desAscii64[(l>>12)&0x3f],
		desAscii64[(l>>6)&0x3f],
		desAscii64[l&0x3f])
	l = (r0 << 16) | ((r1 >> 16) & 0xffff)
	out = append(out,
		desAscii64[(l>>18)&0x3f],
		desAscii64[(l>>12)&0x3f],
		desAscii64[(l>>6)&0x3f],
		desAscii64[l&0x3f])
	l = r1 << 2
	out = append(out,
		desAscii64[(l>>12)&0x3f],
		desAscii64[(l>>6)&0x3f],
		desAscii64[l&0x3f])
	return string(out)
}

func desValidSaltChar(c byte) bool {
	return (c >= '.' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// cryptDES — диспетчер DES как в php_crypt.
func cryptDES(pw, salt string) string {
	if len(salt) == 0 {
		return ""
	}
	if salt[0] == '_' || (len(salt) >= 2 && desValidSaltChar(salt[0]) && desValidSaltChar(salt[1])) {
		r := cryptDESExtended(pw, salt)
		if r == "" || strings.HasPrefix(salt, "*0") {
			return ""
		}
		return r
	}
	return ""
}
