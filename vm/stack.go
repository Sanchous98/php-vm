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
	sp    unsafe.Pointer
	stack [stackSize]T
	size  int
}

func (s *Stack[T]) Init() {
	s.size = int(unsafe.Sizeof(*new(T)))
	s.sp = unsafe.Add(unsafe.Pointer(&s.stack[0]), -s.size)
}
func (s *Stack[T]) Pop() T {
	if uintptr(s.sp) < uintptr(unsafe.Pointer(&s.stack[0])) {
		return *new(T)
	}

	v := *(*T)(s.sp)
	s.MovePointer(-1)
	return v
}
func (s *Stack[T]) Push(v T) {
	if s.sp == unsafe.Pointer(&s.stack[stackSize-1]) {
		panic("stack overflow")
	}

	s.MovePointer(1)
	s.SetTop(v)
}
func (s *Stack[T]) TopIndex() int {
	return int(uintptr(s.sp)-uintptr(unsafe.Pointer(&s.stack[0]))) / s.size
}
func (s *Stack[T]) Slice(offsetX, offsetY int) []T {
	length := s.TopIndex() + 1
	return s.stack[length+offsetX : length+offsetY]
}
func (s *Stack[T]) Sp(pointer int)         { s.sp = unsafe.Add(unsafe.Pointer(&s.stack[0]), pointer*s.size) }
func (s *Stack[T]) Top() T                 { return *(*T)(s.sp) }
func (s *Stack[T]) SetTop(v T)             { *(*T)(s.sp) = v }
func (s *Stack[T]) MovePointer(offset int) { s.sp = unsafe.Add(s.sp, offset*s.size) }
