package vm

import (
	"unsafe"
)

const stackSize = 4096

type stackIface[T any] interface {
	Init()
	Pop() T
	Push(T)
	TopIndex() int
	Slice(int, int) []T
	Sp(int)
	Top() T
	SetTop(T)
	MovePointer(int)
}

type Stack[T any] struct {
	sp           *T
	stack        [stackSize]T
	defaultValue T
}

func (s *Stack[T]) Init() {
	s.sp = (*T)(unsafe.Add(unsafe.Pointer(&s.stack[0]), -unsafe.Sizeof(*s.sp)))
}
func (s *Stack[T]) Pop() T {
	var d T

	if uintptr(unsafe.Pointer(s.sp)) < uintptr(unsafe.Pointer(&s.stack[0])) {
		return d
	}

	v := *s.sp
	*s.sp = d
	s.MovePointer(-1)
	return v
}
func (s *Stack[T]) Push(v T) {
	if s.sp == &s.stack[stackSize-1] {
		panic("stack overflow")
	}

	s.MovePointer(1)
	s.SetTop(v)
}
func (s *Stack[T]) TopIndex() int {
	return int(uintptr(unsafe.Pointer(s.sp))-uintptr(unsafe.Pointer(&s.stack[0]))) / int(unsafe.Sizeof(*s.sp))
}
func (s *Stack[T]) Slice(offsetX, offsetY int) []T {
	length := s.TopIndex() + 1
	return s.stack[length+offsetX : length+offsetY]
}
func (s *Stack[T]) Sp(pointer int) {
	s.sp = (*T)(unsafe.Add(unsafe.Pointer(&s.stack[0]), pointer*int(unsafe.Sizeof(*s.sp))))
}
func (s *Stack[T]) Top() T     { return *(s.sp) }
func (s *Stack[T]) SetTop(v T) { *(s.sp) = v }
func (s *Stack[T]) MovePointer(offset int) {
	s.sp = (*T)(unsafe.Add(unsafe.Pointer(s.sp), offset*int(unsafe.Sizeof(*s.sp))))
}
func (s *Stack[T]) Reset() {
	s.sp = (*T)(unsafe.Add(unsafe.Pointer(&s.stack[0]), -int(unsafe.Sizeof(*s.sp))))
	clear(s.stack[:])
}
