package domain

import "strings"

func ValidateUsername(s string) error {
	if len(s) < 1 || len(s) > 32 {
		return ErrInvalidUsername
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return ErrInvalidUsername
		}
	}
	return nil
}
func ValidateSlug(s string, reserved []string) error {
	if ValidateUsername(s) != nil || s[0] == '-' || s[len(s)-1] == '-' || strings.Contains(s, "--") {
		return ErrInvalidSlug
	}
	for _, name := range reserved {
		if s == name {
			return ErrReservedSlug
		}
	}
	return nil
}
