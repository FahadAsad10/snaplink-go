// Package shortid generates and validates short link codes.
package shortid

import (
	"crypto/rand"
	"math/big"
	"regexp"
)

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/l/I

var validCode = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// New returns a random code of length n drawn from an unambiguous alphabet.
func New(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b), nil
}

// Valid reports whether a custom code is allowed.
func Valid(code string) bool {
	return validCode.MatchString(code)
}
