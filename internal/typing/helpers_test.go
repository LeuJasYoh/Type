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

// containsCJK 决定剪贴板路径的开工提示说"检测到中文"还是"检测到非英文字符":
// 走剪贴板的判据是"含非 ASCII", 两边错开一格就是一句假话
func TestContainsCJK(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"中文", true},
		{"你好，世界。", true},
		{"ＡＢＣ", true},      // 全角拉丁
		{"１２３、", true},     // 全角数字与全角标点
		{"ひらがな", true},     // 平假名
		{"カタカナ", true},     // 片假名
		{"한국어", true},      // 韩文音节
		{"中英 mixed", true}, // 混排: 只要有一个就算
		{"hello", false},
		{"123", false},
		{"", false},
		{"\t\r\n", false},
		{"café", false},   // 带音符的拉丁字母: 非 ASCII 但不是中文
		{"Привет", false}, // 西里尔字母
		{"😀🎉", false},     // emoji
		{"Ω≈ç√", false},   // 希腊字母与数字符号
	}
	for _, c := range cases {
		if got := containsCJK(c.s); got != c.want {
			t.Errorf("containsCJK(%q) = %v, want %v", c.s, got, c.want)
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
