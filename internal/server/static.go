package server

import "net/http"

// Version 是静态资源的缓存版本号；发版时与 main.go 的 version 常量保持一致。
const Version = "0.3.0-m4"

// cacheStatic 给静态响应加 ETag 缓存头：每次请求带 If-None-Match 校验，
// 版本变了 ETag 变 → 浏览器自动重取，解决"改了代码用户还看旧版"。
func (s *Server) cacheStatic(h http.Handler) http.Handler {
	etag := `"` + Version + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}
