package typing

import "testing"

// 纯函数用例: 不依赖平台, 这些判断决定"走剪贴板还是走逐字符"与字符间隔档位

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
