package mpesaclient_test

import (
	"testing"

	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0712345678", "254712345678"},
		{"+254712345678", "254712345678"},
		{"254712345678", "254712345678"},
		{"712345678", "254712345678"},
		{"0110345678", "254110345678"},
		{"+254 712 345 678", "254712345678"},
		{"0712-345-678", "254712345678"},
	}
	for _, tt := range tests {
		if got := mpesaclient.NormalizePhone(tt.in); got != tt.want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestValidPhone(t *testing.T) {
	valid := []string{"0712345678", "+254712345678", "254112345678", "712345678"}
	for _, p := range valid {
		if !mpesaclient.ValidPhone(p) {
			t.Errorf("ValidPhone(%q) = false, want true", p)
		}
	}

	invalid := []string{"", "12345", "0812345678", "25471234567890", "abcdefghij"}
	for _, p := range invalid {
		if mpesaclient.ValidPhone(p) {
			t.Errorf("ValidPhone(%q) = true, want false", p)
		}
	}
}
