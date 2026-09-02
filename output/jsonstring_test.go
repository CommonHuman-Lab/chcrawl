package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func jsonStringTestCases() []string {
	return []string{
		"",
		"abc",
		`he said "hi"`,
		`back\slash`,
		"tab\tnewline\nreturn\r",
		"\b\f",
		string([]byte{0x00, 0x01, 0x02, 0x1f}),
		"<script>alert(1)</script>",
		"a&b",
		"100% <safe> & sound",
		"héllo wörld",
		"\U0001F600\U0001F680 unicode", // emoji
		"\u2028line-sep\u2029para-sep",
		string([]byte{0xff, 0xfe}),     // invalid UTF-8
		string([]byte{'a', 0x80, 'b'}), // invalid UTF-8 continuation byte
		strings.Repeat("x", 10000),     // long string, exercises the fast-path copy
		"\x7f",                         // DEL, must NOT be escaped
		string([]byte{0xe2, 0x80}),     // truncated 3-byte sequence (would-be U+2028)
		"null byte:\x00end",
		"quote'n'apostrophe\"'",
	}
}

func TestAppendJSONString(t *testing.T) {
	for _, s := range jsonStringTestCases() {
		got := appendJSONString(nil, s)
		want, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal(%q): %v", s, err)
		}
		if bytes.Equal(got, want) {
			continue
		}
		if !utf8.ValidString(s) {
			assertJSONDecodesEqual(t, s, got, want)
			continue
		}
		t.Errorf("appendJSONString(%q):\n got  %s\n want %s", s, got, want)
	}
}

func FuzzAppendJSONString(f *testing.F) {
	for _, s := range jsonStringTestCases() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := appendJSONString(nil, s)
		want, err := json.Marshal(s)
		if err != nil {
			t.Skipf("json.Marshal errored on %q: %v", s, err)
		}
		if bytes.Equal(got, want) {
			return
		}
		if !utf8.ValidString(s) {
			assertJSONDecodesEqual(t, s, got, want)
			return
		}
		t.Fatalf("mismatch for %q:\n got  %s\n want %s", s, got, want)
	})
}

func assertJSONDecodesEqual(t *testing.T, s string, got, want []byte) {
	t.Helper()
	var gotStr, wantStr string
	if err := json.Unmarshal(got, &gotStr); err != nil {
		t.Errorf("appendJSONString(%q) produced invalid JSON %s: %v", s, got, err)
		return
	}
	if err := json.Unmarshal(want, &wantStr); err != nil {
		t.Errorf("json.Marshal(%q) produced invalid JSON %s: %v", s, want, err)
		return
	}
	if gotStr != wantStr {
		t.Errorf("appendJSONString(%q) decodes to %q, json.Marshal(%q) decodes to %q", s, gotStr, s, wantStr)
	}
}
