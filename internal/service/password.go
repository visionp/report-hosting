package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"report_hosting/internal/config"
)

func validParams(p config.Argon2Params) bool {
	return p.Parallelism > 0 && p.Parallelism <= 16 &&
		p.Memory >= 8*uint32(p.Parallelism) && p.Memory <= 256*1024 &&
		p.Iterations > 0 && p.Iterations <= 10
}
func HashPassword(pw string, p config.Argon2Params) (string, error) {
	if !validParams(p) || len(pw) == 0 || len(pw) > 4096 {
		return "", errors.New("invalid password or Argon2 parameters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(h)), nil
}
func VerifyPassword(hash, pw string) bool {
	if len(pw) > 4096 || len(hash) > 512 {
		return false
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false
	}
	var nums [3]uint64
	for i, prefix := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], prefix) {
			return false
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(params[i], prefix), 10, 32)
		if err != nil {
			return false
		}
		nums[i] = v
	}
	if nums[2] > 255 {
		return false
	}
	p := config.Argon2Params{Memory: uint32(nums[0]), Iterations: uint32(nums[1]), Parallelism: uint8(nums[2])}
	if !validParams(p) {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
