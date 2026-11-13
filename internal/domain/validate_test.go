package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want error
	}{
		{"demo", nil}, {"a", nil}, {strings.Repeat("a", 32), nil}, {strings.Repeat("a", 33), ErrInvalidSlug},
		{"-a", ErrInvalidSlug}, {"a-", ErrInvalidSlug}, {"a--b", ErrInvalidSlug}, {"A", ErrInvalidSlug}, {"a_b", ErrInvalidSlug}, {"", ErrInvalidSlug}, {"admin", ErrReservedSlug},
	} {
		t.Run(tc.s, func(t *testing.T) {
			if got := ValidateSlug(tc.s, []string{"admin"}); !errors.Is(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
func TestValidateUsername(t *testing.T) {
	for _, s := range []string{"site-1", "a", "-"} {
		if err := ValidateUsername(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"a:b", "a b", "a\nb", "", "é", strings.Repeat("a", 33)} {
		if !errors.Is(ValidateUsername(s), ErrInvalidUsername) {
			t.Fatalf("accepted %q", s)
		}
	}
}
