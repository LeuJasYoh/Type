package main

import "testing"

func TestUtf16Units(t *testing.T) {
	cases := []struct {
		r    rune
		want []uint16
	}{
		{'A', []uint16{0x41}},
		{'中', []uint16{0x4E2D}},
		{'\uFF01', []uint16{0xFF01}},         // 全角 !
		{0x1F600, []uint16{0xD83D, 0xDE00}},  // 😀 代理对
		{0x10000, []uint16{0xD800, 0xDC00}},  // U+10000 下界
		{0x10FFFF, []uint16{0xDBFF, 0xDFFF}}, // U+10FFFF 上界
		{0xFFFF, []uint16{0xFFFF}},
	}
	for _, c := range cases {
		got := utf16Units(c.r)
		if len(got) != len(c.want) {
			t.Errorf("utf16Units(%U) len = %d, want %d", c.r, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("utf16Units(%U)[%d] = %#x, want %#x", c.r, i, got[i], c.want[i])
			}
		}
	}
}

func TestContainsNonASCII(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"hello", false},
		{"hello world\n", false},
		{"h\xe9llo", true},
		{"中文", true},
		{"123", false},
		{"", false},
		{"\t\r\n", false},
		{"ab\xf0\x9f\x98\x80", true}, // ab😀
	}
	for _, c := range cases {
		if got := containsNonASCII(c.s); got != c.want {
			t.Errorf("containsNonASCII(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestIsCJKPunct(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'\u3001', true}, // 、
		{'\u3002', true}, // 。
		{'\uFF01', true}, // ！
		{'\uFF1F', true}, // ？
		{'\u201C', true}, // “
		{'\u201D', true}, // ”
		{'中', false},
		{'a', false},
		{'1', false},
		{'.', false},
	}
	for _, c := range cases {
		if got := isCJKPunct(c.r); got != c.want {
			t.Errorf("isCJKPunct(%U) = %v, want %v", c.r, got, c.want)
		}
	}
}
