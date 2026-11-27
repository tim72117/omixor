package web

import (
	"log"
	"net/http"
	"sync"
)

// WarnUntrustedProxy 沒設信任的代理層數(OMIXOR_TRUSTED_PROXY_HOPS)卻收到轉送標頭時,記一次警告:
// 多半在反向代理後面,登入與 MCP 授權的每來源頻率限制會把所有人當成同一個來源。不改請求本身,標頭內容不寫進 log
func WarnUntrustedProxy(next http.Handler, hops int) http.Handler {
	if hops > 0 {
		return next
	}
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
			if r.Header.Get(h) != "" {
				once.Do(func() {
					log.Print("警告:收到反向代理的轉送標頭,但 OMIXOR_TRUSTED_PROXY_HOPS 是 0:所有人在每來源的登入頻率限制裡算成同一個來源。" +
						"前面有反向代理就設成代理層數(一層是 1,代理要帶 X-Forwarded-For);沒有代理可以忽略(此警告只記一次)")
				})
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}
