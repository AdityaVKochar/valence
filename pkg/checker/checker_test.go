package checker

import (
	"errors"
	"strings"
	"testing"
)

type tc struct {
	name, expected, actual string
	ok                     bool
}

func run(t *testing.T, spec string, cases []tc) {
	t.Helper()
	c, err := Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res, err := c.Check(strings.NewReader(tt.expected), strings.NewReader(tt.actual))
			if err != nil {
				t.Fatal(err)
			}
			if res.OK != tt.ok {
				t.Fatalf("%s(%q, %q) = %+v, want ok=%v", spec, tt.expected, tt.actual, res, tt.ok)
			}
		})
	}
}

func TestExact(t *testing.T) {
	run(t, "exact", []tc{
		{"identical", "3\n", "3\n", true},
		{"missing trailing newline", "3\n", "3", true},
		{"extra trailing newlines", "3\n", "3\n\n\n", true},
		{"CRLF", "1 2\n3\n", "1 2\r\n3\r\n", true},
		{"lone CR is significant", "1\n", "1\r", false},
		{"trailing space matters", "3\n", "3 \n", false},
		{"different value", "3\n", "4\n", false},
		{"blank line inside matters", "a\nb\n", "a\n\nb\n", false},
		{"both empty", "", "", true},
		{"empty output", "3\n", "", false},
		{"empty expected, newline output", "", "\n", true},
		{"extra output", "3\n", "3\n4\n", false},
		{"prefix", "34\n", "3\n", false},
	})
}

func TestTokens(t *testing.T) {
	run(t, "tokens", []tc{
		{"identical", "1 2 3\n", "1 2 3\n", true},
		{"whitespace differs", "1 2 3\n", "  1\n2\t\t3   ", true},
		{"CRLF", "1\n2\n", "1\r\n2\r\n", true},
		{"trailing spaces", "1 2\n", "1 2   \n\n", true},
		{"both empty", "", "  \n", true},
		{"empty output", "1\n", "", false},
		{"missing token", "1 2\n", "1\n", false},
		{"extra token", "1\n", "1 2\n", false},
		{"different token", "1 2\n", "1 3\n", false},
		{"case matters", "YES\n", "yes\n", false},
		{"numbers are compared as text", "1\n", "1.0\n", false},
	})
}

func TestFloat(t *testing.T) {
	run(t, "float:1e-6", []tc{
		{"exact", "3.14159265\n", "3.14159265\n", true},
		{"within absolute", "0.5\n", "0.5000005\n", true},
		{"outside absolute", "0.5\n", "0.500002\n", false},
		{"within relative", "1000000000\n", "1000000500\n", true},
		{"scientific notation", "0.000123\n", "1.23e-4\n", true},
		{"uppercase exponent", "12300\n", "1.23E4\n", true},
		{"words must match", "Case 1: 2.5\n", "Case 1: 2.5000001\n", true},
		{"word differs", "Case 1: 2.5\n", "case 1: 2.5\n", false},
		{"nan expected and printed", "nan\n", "NaN\n", true},
		{"nan printed for number", "2.5\n", "nan\n", false},
		{"inf", "inf\n", "+Inf\n", true},
		{"inf printed for number", "1e300\n", "inf\n", false},
		{"non-number printed", "2.5\n", "abc\n", false},
		{"fewer tokens", "1 2\n", "1\n", false},
		{"more tokens", "1\n", "1 2\n", false},
		{"empty output", "1\n", "", false},
	})
}

func TestParse(t *testing.T) {
	for _, spec := range []string{"", "custom", "float:", "float:abc", "float:0", "float:2", "float:nan"} {
		if _, err := Parse(spec); !errors.Is(err, ErrUnknown) {
			t.Errorf("Parse(%q) = %v, want ErrUnknown", spec, err)
		}
	}
	for _, spec := range []string{"exact", "tokens", "float:1e-6", "float:0.001"} {
		if _, err := Parse(spec); err != nil {
			t.Errorf("Parse(%q) = %v", spec, err)
		}
	}
}

func TestLargeStreamed(t *testing.T) {
	big := strings.Repeat("123456789 ", 1_000_000)
	c, _ := Parse("tokens")
	res, err := c.Check(strings.NewReader(big), strings.NewReader(big+"\n"))
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	res, _ = Exact{}.Check(strings.NewReader(big), strings.NewReader(big[:len(big)-2]+"x "))
	if res.OK {
		t.Fatal("exact accepted a late difference")
	}
}
