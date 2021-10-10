package server

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/Mrs4s/go-cqhttp/coolq"
	"github.com/Mrs4s/go-cqhttp/modules/config"
)

// runPprof 启动 pprof 性能分析服务器
func runPprof(_ *coolq.CQBot, node yaml.Node) {
	var conf config.PprofServer
	switch err := node.Decode(&conf); {
	case err != nil:
		log.Warn("读取pprof配置失败 :", err)
		fallthrough
	case conf.Disabled:
		return
	}

	addr := fmt.Sprintf("%s:%d", conf.Host, conf.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	server := http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Infof("pprof debug 服务器已启动: %v/debug/pprof", addr)
		log.Warnf("警告: pprof 服务不支持鉴权, 请不要运行在公网.")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(err)
			log.Infof("pprof 服务启动失败, 请检查端口是否被占用.")
			log.Warnf("将在五秒后退出.")
			time.Sleep(time.Second * 5)
			os.Exit(1)
		}
	}()
}
