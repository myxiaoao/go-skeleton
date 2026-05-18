package router

import (
	"net/http"
	"net/http/pprof"
	"time"
)

// NewPprofServer 构造独立的 pprof debug 服务器。
//
// 单独起 mux + http.Server 而不是挂到业务 engine 上：
//  1. pprof 暴露的 /debug/pprof/* 风险高（heap dump、goroutine trace），
//     不能跟业务路由共用入口；
//  2. 单独监听端口便于网络层用防火墙隔离，**只**绑 loopback 配合 SSH 隧道访问；
//  3. 业务 engine 的 middleware（鉴权、限流）对 pprof 没意义还会干扰。
//
// 返回的 *http.Server 由调用方负责 ListenAndServe / Shutdown。
// addr 应为 "127.0.0.1:6060" 这种回环地址，不要绑 0.0.0.0。
func NewPprofServer(addr string) *http.Server {
	mux := http.NewServeMux()
	// 注册 net/http/pprof 暴露的标准 endpoint。直接 import _ "net/http/pprof" 会
	// 污染 DefaultServeMux，所以这里走显式注册。
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
