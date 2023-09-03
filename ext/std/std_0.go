package std

import (
	"fmt"
	"php-vm/internal/vm"
	"strconv"
	"time"
)

func bin2hex(args ...vm.Value) vm.String {
	s := args[0].(vm.String)

	if ui, err := strconv.ParseUint(string(s), 2, 64); err == nil {
		return vm.String(fmt.Sprintf("%x", ui))
	}

	return ""
}

func sleep(args ...vm.Value) vm.Int {
	time.Sleep(time.Duration(args[0].(vm.Int)))
	return vm.Int(0)
}

func usleep(args ...vm.Value) (_ vm.Null) {
	time.Sleep(time.Duration(args[0].(vm.Int)))
	return
}
