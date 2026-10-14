package vm

import "errors"

var (
	errDivByZero     = errors.New("phpvm: division by zero")
	errModByZero     = errors.New("phpvm: modulo by zero")
	errStackOverflow = errors.New("phpvm: stack overflow")
	errUnknownOpcode = errors.New("phpvm: unknown opcode")
	errStepLimit     = errors.New("phpvm: instruction budget exceeded")

	// errArrayTooLarge — превышен предел индекса (недостижим: PHP капает раньше).
	errArrayTooLarge = errors.New("phpvm: array is too large")
	// errNextOccupied — следующий авто-ключ переполняет int64 (PHP_INT_MAX).
	// Сообщение совпадает с PHP.
	errNextOccupied = errors.New("Cannot add element to the array as the next element is already occupied")
	// errIllegalOffset — ключ массива недопустимого типа (array/object).
	errIllegalOffset = errors.New("Illegal offset type")

	// ООП
	errUnknownClass    = errors.New("phpvm: unknown class")
	errUnknownFunc     = errors.New("phpvm: call to undefined function")
	errNotObject       = errors.New("phpvm: value is not an object")
	errUndefinedMethod = errors.New("phpvm: call to undefined method")
	errUnknownStatic   = errors.New("phpvm: access to undeclared static property")
	errUnknownConst    = errors.New("phpvm: undefined constant")
	errAccess          = errors.New("phpvm: call to non-public method")
	errAbstractMethod  = errors.New("phpvm: call to abstract method")
)

var errStackUnderflow = errors.New("phpvm: stack underflow")
