package metrics

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFansAreReadFromHwmon(t *testing.T) {
	sys := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		path = filepath.Join(sys, "class", "hwmon", path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("hwmon0/name", "cpu_thermal\n")
	write("hwmon2/name", "pwmfan\n")
	write("hwmon2/fan1_input", "3120\n")
	write("hwmon3/name", "nct6775\n")
	write("hwmon3/fan2_input", "850\n")
	write("hwmon3/fan2_label", "Case fan\n")
	// A sleeping GPU's fan, which reading would wake.
	write("hwmon4/name", "amdgpu\n")
	write("hwmon4/fan1_input", "0\n")
	write("hwmon4/device/power/runtime_status", "suspended\n")
	t.Setenv("HOST_SYS", sys)

	got := readFans(fanSensors())

	want := []Fan{{Name: "Case fan", RPM: 850}, {Name: "pwmfan fan1", RPM: 3120}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readFans() = %+v, want %+v", got, want)
	}
}
