package vm

type ErrorLevel int16

const (
	EError ErrorLevel = 1 << iota
	EWarning
	EParse
	ENotice
	ECoreError
	ECoreWarning
	ECompileError
	ECompileWarning
	EUserError
	EUserWarning
	EUserNotice
	EStrict
	ERecoverableError
	EDeprecated
	EUserDeprecated
	EAll = 1<<15 - 1
)

type Throwable interface {
	error

	Level() ErrorLevel
}

type throwable struct {
	level   ErrorLevel
	message string
}

func (t *throwable) Error() string     { return t.message }
func (t *throwable) Level() ErrorLevel { return t.level }

func NewThrowable(message string, level ErrorLevel) Throwable {
	return &throwable{level, message}
}
