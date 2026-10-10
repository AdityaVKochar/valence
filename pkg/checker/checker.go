package checker

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

type Result struct {
	OK      bool
	Message string
}

type Checker interface {
	Check(expected, actual io.Reader) (Result, error)
}

var ErrUnknown = errors.New("checker: unknown checker")

func Parse(spec string) (Checker, error) {
	switch {
	case spec == "exact":
		return Exact{}, nil
	case spec == "tokens":
		return Tokens{}, nil
	case strings.HasPrefix(spec, "float:"):
		eps, err := strconv.ParseFloat(strings.TrimPrefix(spec, "float:"), 64)
		if err != nil || eps <= 0 || eps >= 1 || math.IsNaN(eps) {
			return nil, fmt.Errorf("%w: %q needs an epsilon between 0 and 1", ErrUnknown, spec)
		}
		return Float{Epsilon: eps}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknown, spec)
}

func CheckFiles(c Checker, expectedPath, actualPath string) (Result, error) {
	e, err := os.Open(expectedPath)
	if err != nil {
		return Result{}, err
	}
	defer e.Close()
	a, err := os.Open(actualPath)
	if err != nil {
		return Result{}, err
	}
	defer a.Close()
	return c.Check(e, a)
}

type Exact struct{}

func (Exact) Check(expected, actual io.Reader) (Result, error) {
	e := &crlfReader{r: bufio.NewReaderSize(expected, 64<<10)}
	a := &crlfReader{r: bufio.NewReaderSize(actual, 64<<10)}
	var pos int64
	for {
		eb, eerr := e.ReadByte()
		ab, aerr := a.ReadByte()
		if eerr != nil && eerr != io.EOF {
			return Result{}, eerr
		}
		if aerr != nil && aerr != io.EOF {
			return Result{}, aerr
		}
		if eerr == io.EOF && aerr == io.EOF {
			return Result{OK: true}, nil
		}
		if eerr == nil && aerr == nil && eb == ab {
			pos++
			continue
		}
		eRest, err := onlyNewlines(e, eb, eerr)
		if err != nil {
			return Result{}, err
		}
		aRest, err := onlyNewlines(a, ab, aerr)
		if err != nil {
			return Result{}, err
		}
		if eRest && aRest {
			return Result{OK: true}, nil
		}
		return Result{Message: fmt.Sprintf("output differs at byte %d", pos)}, nil
	}
}

func onlyNewlines(r *crlfReader, first byte, firstErr error) (bool, error) {
	if firstErr == io.EOF {
		return true, nil
	}
	if first != '\n' {
		return false, nil
	}
	for {
		b, err := r.ReadByte()
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if b != '\n' {
			return false, nil
		}
	}
}

type crlfReader struct {
	r *bufio.Reader
}

func (c *crlfReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err != nil || b != '\r' {
		return b, err
	}
	next, err := c.r.Peek(1)
	if err == nil && next[0] == '\n' {
		return c.r.ReadByte()
	}
	return '\r', nil
}

type Tokens struct{}

func (Tokens) Check(expected, actual io.Reader) (Result, error) {
	return compareTokens(expected, actual, func(e, a []byte) bool { return bytes.Equal(e, a) })
}

type Float struct {
	Epsilon float64
}

func (f Float) Check(expected, actual io.Reader) (Result, error) {
	return compareTokens(expected, actual, func(e, a []byte) bool {
		ef, eerr := strconv.ParseFloat(string(e), 64)
		if eerr != nil {
			return bytes.Equal(e, a)
		}
		af, aerr := strconv.ParseFloat(string(a), 64)
		if aerr != nil {
			return false
		}
		return floatsClose(ef, af, f.Epsilon)
	})
}

func floatsClose(expected, actual, eps float64) bool {
	switch {
	case math.IsNaN(expected):
		return math.IsNaN(actual)
	case math.IsInf(expected, 0):
		return expected == actual
	case math.IsNaN(actual) || math.IsInf(actual, 0):
		return false
	}
	diff := math.Abs(expected - actual)
	return diff <= eps || diff <= eps*math.Abs(expected)
}

func compareTokens(expected, actual io.Reader, equal func(e, a []byte) bool) (Result, error) {
	e := &tokenizer{r: bufio.NewReaderSize(expected, 64<<10)}
	a := &tokenizer{r: bufio.NewReaderSize(actual, 64<<10)}
	for n := 1; ; n++ {
		et, eerr := e.next()
		at, aerr := a.next()
		if eerr != nil && eerr != io.EOF {
			return Result{}, eerr
		}
		if aerr != nil && aerr != io.EOF {
			return Result{}, aerr
		}
		switch {
		case eerr == io.EOF && aerr == io.EOF:
			return Result{OK: true}, nil
		case eerr == io.EOF:
			return Result{Message: fmt.Sprintf("extra output after %d tokens", n-1)}, nil
		case aerr == io.EOF:
			return Result{Message: fmt.Sprintf("output ended after %d tokens", n-1)}, nil
		}
		if !equal(et, at) {
			return Result{Message: fmt.Sprintf("token %d differs", n)}, nil
		}
	}
}

type tokenizer struct {
	r   *bufio.Reader
	buf []byte
}

func (t *tokenizer) next() ([]byte, error) {
	t.buf = t.buf[:0]
	for {
		b, err := t.r.ReadByte()
		if err != nil {
			if err == io.EOF && len(t.buf) > 0 {
				return t.buf, nil
			}
			return nil, err
		}
		if isSpace(b) {
			if len(t.buf) > 0 {
				return t.buf, nil
			}
			continue
		}
		t.buf = append(t.buf, b)
	}
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}
