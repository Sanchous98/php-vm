package phpt

import "testing"

func TestBasic(t *testing.T) {
	tests := [...]PhpT{
		{Test: "Trivial \"Hello World\" test", File: "<?php echo \"Hello World\"?>", Expect: "Hello World"},
	}

	for _, test := range &tests {
		test.RunTest(t)
	}
}
