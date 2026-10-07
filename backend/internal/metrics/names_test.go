package metrics

import (
	"reflect"
	"testing"
)

func TestNumberDuplicatesNumbersOnlyRepeatedNames(t *testing.T) {
	got := NumberDuplicates([]string{"coretemp", "acpitz", "coretemp", "nvme", "coretemp"})

	want := []string{"coretemp 1", "acpitz", "coretemp 2", "nvme", "coretemp 3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NumberDuplicates() = %q, want %q", got, want)
	}
	if got := NumberDuplicates(nil); len(got) != 0 {
		t.Errorf("NumberDuplicates(nil) = %q, want it empty", got)
	}
}
