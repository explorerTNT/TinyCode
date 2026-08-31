package updater

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.0.0", "v1.0.1", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.10.0", "v1.9.0", 1},
		// without v prefix
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		// mixed
		{"v1.0.0", "1.0.1", -1},
		{"1.0.1", "v1.0.0", 1},
		// dev version fallback
		{"dev", "v1.0.0", -1},
		{"v1.0.0", "dev", 1},
	}

	for _, tt := range tests {
		got := Compare(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsDev(t *testing.T) {
	if !IsDev("dev") {
		t.Error("IsDev(\"dev\") = false, want true")
	}
	if !IsDev("") {
		t.Error("IsDev(\"\") = false, want true")
	}
	if IsDev("v1.0.0") {
		t.Error("IsDev(\"v1.0.0\") = true, want false")
	}
}
