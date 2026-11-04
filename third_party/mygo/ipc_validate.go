package mygo

// 本仓删掉了 TypeScript 生成器, 但"能过边界的类型必须能编成 JSON"这条检查留下:
// 它挡的错误(绑了个返回 chan 的方法)要到运行期才炸, 而那时前端拿到的是
// 一个永远 reject 的 Promise。判据比原实现简化 —— 只否决坚决编不了的种类,
// 结构体字段递归看一眼, 其余交给 encoding/json 自己判。

import (
	"fmt"
	"reflect"
	"time"
)

var timeType = reflect.TypeFor[time.Time]()

// isServiceName reports whether s is usable as a JavaScript identifier,
// which is what a bound service's name becomes on the page.
func isServiceName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '$' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z'):
		case i > 0 && '0' <= r && r <= '9':
		default:
			return false
		}
	}
	return true
}

// validateJSONType reports an error if values of type t cannot cross the
// IPC boundary.
func validateJSONType(t reflect.Type) error {
	return walkJSONType(t, make(map[reflect.Type]bool))
}

func walkJSONType(t reflect.Type, seen map[reflect.Type]bool) error {
	if t == nil || seen[t] {
		return nil
	}
	seen[t] = true
	// 接口值与时间各自有专门的编码路径, 不必往里看
	if t == errorType || t == timeType || t.Kind() == reflect.Interface {
		return nil
	}
	switch t.Kind() {
	case reflect.Chan, reflect.Func, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return fmt.Errorf("type %s cannot be encoded as JSON", t)
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return walkJSONType(t.Elem(), seen)
	case reflect.Map:
		if k := t.Key(); k.Kind() != reflect.String && !isNumberKind(k.Kind()) {
			return fmt.Errorf("map key type %s cannot be encoded as JSON", k)
		}
		return walkJSONType(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if err := walkJSONType(f.Type, seen); err != nil {
				return fmt.Errorf("field %s: %w", f.Name, err)
			}
		}
	}
	return nil
}

func isNumberKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
