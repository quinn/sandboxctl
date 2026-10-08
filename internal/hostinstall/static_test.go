package hostinstall

import (
	"debug/elf"
	"testing"
)

func TestHasELFInterpreter(t *testing.T) {
	tests := []struct {
		name     string
		programs []*elf.Prog
		want     bool
	}{
		{name: "static executable", programs: []*elf.Prog{{ProgHeader: elf.ProgHeader{Type: elf.PT_LOAD}}}},
		{name: "dynamic executable", programs: []*elf.Prog{{ProgHeader: elf.ProgHeader{Type: elf.PT_INTERP}}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasELFInterpreter(test.programs); got != test.want {
				t.Fatalf("hasELFInterpreter() = %t, want %t", got, test.want)
			}
		})
	}
}
