package gotest

import "testing"

func TestCount(t *testing.T) {
	tests := []struct {
		name           string
		out            string
		passed, failed int
	}{
		{"skip counts as failed", `{"Action":"pass","Test":"TestA"}
{"Action":"skip","Test":"TestB"}`, 1, 1},
		{"mixed", `{"Action":"run","Test":"TestA"}
{"Action":"pass","Test":"TestA"}
{"Action":"pass","Test":"TestA/sub"}
{"Action":"fail","Test":"TestB"}
{"Action":"fail","Test":"TestB/sub"}
{"Action":"fail"}`, 1, 1},
		{"build error, no tests ran", `# evalwork/x/hidden
x_test.go:3:2: undefined: nope
{"Action":"fail"}`, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, failed, _ := Count(tt.out)
			if passed != tt.passed || failed != tt.failed {
				t.Errorf("got %d passed, %d failed; want %d and %d", passed, failed, tt.passed, tt.failed)
			}
		})
	}
}
