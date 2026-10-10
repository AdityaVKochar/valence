package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

var invalidHandleChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func baseHandle(login string) string {
	h := invalidHandleChars.ReplaceAllString(strings.TrimSpace(login), "-")
	h = strings.Trim(h, "-")
	if len(h) > 28 {
		h = h[:28]
	}
	if len(h) < 3 {
		h = "user-" + h
		h = strings.TrimSuffix(h, "-")
	}
	if len(h) < 3 {
		h += "-1"
	}
	return h
}

func uniqueHandle(ctx context.Context, login string, taken func(context.Context, string) (bool, error)) (string, error) {
	base := baseHandle(login)
	for i := 1; i <= 50; i++ {
		h := base
		if i > 1 {
			h = base + "-" + strconv.Itoa(i)
		}
		t, err := taken(ctx, h)
		if err != nil {
			return "", err
		}
		if !t {
			return h, nil
		}
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	return base + "-" + hex.EncodeToString(b[:]), nil
}
