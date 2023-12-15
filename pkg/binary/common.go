package binary

import (
	"fmt"
	"slices"
	"unsafe"
)

type slice struct {
	Data     uintptr
	Len, Cap int
}

func swap[T uint16 | uint32 | uint64](b []byte) (res []byte) {
	res = make([]byte, len(b))
	copy(res, b)

	size := int(unsafe.Sizeof(T(0)))

	for i := 0; i < len(res)/size; i++ {
		slices.Reverse(res[i*size : (i+1)*size])
	}
	return
}

func convert[E uint16 | uint32 | uint64, S ~[]E](b []byte, s S) {
	eSize := int(unsafe.Sizeof(E(0)))

	if len(b)%eSize != 0 {
		panic(fmt.Errorf("slice has to be multiply of %T size", E(0)))
	}

	_ = s[len(b)/eSize-1]

	res := (*slice)(unsafe.Pointer(&b))
	res.Len = len(b) / eSize
	res.Cap = len(b) / eSize

	for i, e := range *(*S)(unsafe.Pointer(res)) {
		s[i] = e
	}
}
