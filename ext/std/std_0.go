package std

import (
	"fmt"
	"php-vm/internal/vm"
	"strconv"
	"time"
)

func bin2hex(ctx *vm.FunctionContext) vm.String {
	var s string
	vm.ParseParameters(ctx, &s)

	if ui, err := strconv.ParseUint(s, 2, 64); err == nil {
		return vm.String(fmt.Sprintf("%x", ui))
	}

	return ""
}

func sleep(ctx *vm.FunctionContext) vm.Int {
	var sec int
	vm.ParseParameters(ctx, &sec)
	time.Sleep(time.Duration(sec))
	return vm.Int(0)
}

func usleep(ctx *vm.FunctionContext) (_ vm.Null) {
	var millisec int
	vm.ParseParameters(ctx, &millisec)
	time.Sleep(time.Duration(millisec))
	return
}
