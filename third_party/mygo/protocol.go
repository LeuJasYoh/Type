//go:build !js

package mygo

// ─── 自定义 scheme (mygo://) ──────────────────────────
// 本仓的 scheme 层不走 net/http: 一次请求整份返回。上游那套流式响应、
// range 请求与 http.Handler 扩展点是给"用 Go 现生成大文件"的 app 的, 跟着
// net/http 一起在产物里占 2.4MB; Type 只用 LoadHTML 送一张 93KB 的内嵌页面,
// 一样也用不上。要恢复, 从上游取回 protocol.go 与 frontend.go 的实现。

import (
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/url"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/egoist/mygo/internal/platform"
)

var reservedSchemes = map[string]bool{
	"http": true, "https": true, "file": true, "about": true, "data": true, "blob": true,
	"javascript": true, "ws": true, "wss": true, "ftp": true, "mailto": true, "webkit": true,
}

// schemeReply 是一次 scheme 请求的完整响应: 状态、头、整份 body。
type schemeReply struct {
	status int
	header map[string][]string
	body   []byte
}

type schemeHandler func(p string) schemeReply

// ProtocolModule registers custom URL schemes served by Go.
type ProtocolModule struct {
	mu       sync.RWMutex
	handlers map[string]schemeHandler
}

// Protocol serves custom URL schemes such as app:// from Go.
var Protocol = &ProtocolModule{handlers: map[string]schemeHandler{}}

// HandleFunc serves scheme with fn, which gets the request path and returns
// the whole response at once. Schemes must be registered before the windows
// that use them are created. Streaming a large body chunk by chunk is not
// supported here: the fork answers in one Go, which is all Type needs.
//
// The mygo scheme serves the app's frontend (see SetFrontend) unless it is
// handled here.
func (p *ProtocolModule) HandleFunc(scheme string, fn func(p string) (status int, header map[string][]string, body []byte)) error {
	scheme = strings.ToLower(scheme)
	if !validScheme(scheme) {
		return fmt.Errorf("mygo: invalid scheme %q", scheme)
	}
	if reservedSchemes[scheme] {
		return fmt.Errorf("mygo: scheme %q is reserved", scheme)
	}
	p.mu.Lock()
	p.handlers[scheme] = func(p string) schemeReply {
		status, header, body := fn(p)
		return schemeReply{status: status, header: header, body: body}
	}
	p.mu.Unlock()
	return nil
}

// IsHandled reports whether scheme has a handler.
func (p *ProtocolModule) IsHandled(scheme string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.handlers[strings.ToLower(scheme)]
	return ok
}

// schemes returns the schemes windows must handle: the registered ones and
// the frontend's.
func (p *ProtocolModule) schemes() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.handlers)+1)
	for s := range p.handlers {
		out = append(out, s)
	}
	if _, ok := p.handlers[frontendScheme]; !ok {
		out = append(out, frontendScheme)
	}
	return out
}

func validScheme(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func (h *windowHandler) SchemeRequest(req *platform.SchemeRequest) {
	Protocol.serve(req)
}

// serve answers a request of a custom scheme off the main thread, then hands
// the whole response to the webview on it.
func (p *ProtocolModule) serve(req *platform.SchemeRequest) {
	scheme := strings.ToLower(schemeOf(req.URL))
	p.mu.RLock()
	h := p.handlers[scheme]
	p.mu.RUnlock()
	if h == nil && scheme == frontendScheme {
		h = frontendContent
	}
	go func() {
		reply := schemeReply{status: 404}
		if h != nil {
			reply = callScheme(h, req.URL)
		}
		postMain(func() {
			r := req.Responder
			r.Respond(reply.status, reply.header)
			if len(reply.body) > 0 {
				r.Write(reply.body)
			}
			r.Finish()
		})
	}()
}

// callScheme runs a handler, turning a panic into a 500 instead of losing the
// response: the webview waits for one either way.
func callScheme(h schemeHandler, rawURL string) (reply schemeReply) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("mygo: panic serving %s: %v\n%s", rawURL, r, debug.Stack())
			reply = schemeReply{status: 500}
		}
	}()
	return h(requestPath(rawURL))
}

// schemeOf returns the scheme of rawURL, lowercased.
func schemeOf(rawURL string) string {
	scheme, _, _ := strings.Cut(rawURL, ":")
	return scheme
}

// requestPath returns the path of a scheme URL: "app://host/a?b" -> "/a".
func requestPath(rawURL string) string {
	rest := rawURL
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[j:]
		} else {
			rest = "/"
		}
	}
	if k := strings.IndexAny(rest, "?#"); k >= 0 {
		rest = rest[:k]
	}
	if unescaped, err := url.PathUnescape(rest); err == nil {
		return unescaped
	}
	return rest
}

// frontendContent serves mygo://localhost/ from the embedded frontend, falling
// back to index.html for unknown paths without an extension so client-side
// routers work.
func frontendContent(p string) schemeReply {
	frontend.Lock()
	fsys := frontend.fsys
	frontend.Unlock()
	if fsys == nil && IsDev() {
		// `mygo dev` without a dev server: the files on disk.
		if dir := os.Getenv("MYGO_FRONTEND_DIST"); dir != "" {
			fsys = os.DirFS(dir)
		}
	}
	if fsys == nil {
		return schemeReply{
			status: 404,
			header: map[string][]string{"Content-Type": {"text/html; charset=utf-8"}},
			body:   []byte(noFrontendPage),
		}
	}
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		if path.Ext(name) != "" {
			return schemeReply{status: 404}
		}
		data, err = fs.ReadFile(fsys, "index.html")
		if err != nil {
			return schemeReply{status: 404}
		}
		name = "index.html"
	}
	return schemeReply{
		status: 200,
		header: map[string][]string{"Content-Type": {contentTypeOf(name)}},
		body:   data,
	}
}

func contentTypeOf(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

const noFrontendPage = `<!doctype html>
<meta charset="utf-8">
<title>No frontend</title>
<style>body{font:15px/1.5 system-ui,sans-serif;margin:3em;color:#444}code{font-size:90%}</style>
<h1>No frontend</h1>
<p>Call SetFrontend with the files of the page this window shows.</p>
`

// 本仓删掉了下载器: Type 的界面不产生下载, WebView2 的下载事件在这里直接
// 取消, 免得平台层的事件回调无处可去。要恢复下载功能, 从上游取回 downloads.go
// 与本文件原来的 downloadScheme/downloadWriter。
func (h *windowHandler) SchemeDownload(rawURL string) {}

func (h *windowHandler) DownloadStarted(url, suggestedName string) string { return "" }

func (h *windowHandler) DownloadFinished(url, path string, err error) {}
