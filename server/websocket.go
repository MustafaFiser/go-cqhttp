package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mrs4s/MiraiGo/utils"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"

	"github.com/Mrs4s/go-cqhttp/coolq"
	"github.com/Mrs4s/go-cqhttp/global"
	"github.com/Mrs4s/go-cqhttp/modules/api"
	"github.com/Mrs4s/go-cqhttp/modules/config"
	"github.com/Mrs4s/go-cqhttp/modules/filter"
)

type webSocketServer struct {
	bot  *coolq.CQBot
	conf *config.WebsocketServer

	mu        sync.Mutex
	eventConn []*wsConn

	token     string
	handshake string
	filter    string
}

// websocketClient WebSocket客户端实例
type websocketClient struct {
	bot       *coolq.CQBot
	mu        sync.Mutex
	universal *wsConn
	event     *wsConn

	token             string
	filter            string
	reconnectInterval time.Duration
	limiter           api.Handler
}

type wsConn struct {
	mu        sync.Mutex
	conn      *websocket.Conn
	apiCaller *api.Caller
}

func (c *wsConn) WriteText(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, b)
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// runWSServer 运行一个正向WS server
func runWSServer(b *coolq.CQBot, node yaml.Node) {
	var conf config.WebsocketServer
	switch err := node.Decode(&conf); {
	case err != nil:
		log.Warn("读取正向Websocket配置失败 :", err)
		fallthrough
	case conf.Disabled:
		return
	}

	s := &webSocketServer{
		bot:    b,
		conf:   &conf,
		token:  conf.AccessToken,
		filter: conf.Filter,
	}
	filter.Add(s.filter)
	addr := fmt.Sprintf("%s:%d", conf.Host, conf.Port)
	s.handshake = fmt.Sprintf(`{"_post_method":2,"meta_event_type":"lifecycle","post_type":"meta_event","self_id":%d,"sub_type":"connect","time":%d}`,
		b.Client.Uin, time.Now().Unix())
	b.OnEventPush(s.onBotPushEvent)
	mux := http.ServeMux{}
	mux.HandleFunc("/event", s.event)
	mux.HandleFunc("/api", s.api)
	mux.HandleFunc("/", s.any)
	log.Infof("CQ WebSocket 服务器已启动: %v", addr)
	log.Fatal(http.ListenAndServe(addr, &mux))
}

// runWSClient 运行一个反向向WS client
func runWSClient(b *coolq.CQBot, node yaml.Node) {
	var conf config.WebsocketReverse
	switch err := node.Decode(&conf); {
	case err != nil:
		log.Warn("读取反向Websocket配置失败 :", err)
		fallthrough
	case conf.Disabled:
		return
	}

	c := &websocketClient{
		bot:    b,
		token:  conf.AccessToken,
		filter: conf.Filter,
	}
	filter.Add(c.filter)
	if conf.ReconnectInterval != 0 {
		c.reconnectInterval = time.Duration(conf.ReconnectInterval) * time.Millisecond
	}
	if conf.RateLimit.Enabled {
		c.limiter = rateLimit(conf.RateLimit.Frequency, conf.RateLimit.Bucket)
	}

	if conf.Universal != "" {
		c.connect("Universal", conf.Universal, &c.universal)
		c.bot.OnEventPush(c.onBotPushEvent("Universal", conf.Universal, &c.universal))
		return // 连接到 Universal 后， 不再连接其他
	}
	if conf.API != "" {
		c.connect("API", conf.API, nil)
	}
	if conf.Event != "" {
		c.connect("Event", conf.Event, &c.event)
		c.bot.OnEventPush(c.onBotPushEvent("Event", conf.Event, &c.event))
	}
}

func (c *websocketClient) connect(typ, url string, conptr **wsConn) {
	log.Infof("开始尝试连接到反向WebSocket %s服务器: %v", typ, url)
	header := http.Header{
		"X-Client-Role": []string{typ},
		"X-Self-ID":     []string{strconv.FormatInt(c.bot.Client.Uin, 10)},
		"User-Agent":    []string{"CQHttp/4.15.0"},
	}
	if c.token != "" {
		header["Authorization"] = []string{"Token " + c.token}
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, header) // nolint
	if err != nil {
		log.Warnf("连接到反向WebSocket %s服务器 %v 时出现错误: %v", typ, url, err)
		if c.reconnectInterval != 0 {
			time.Sleep(c.reconnectInterval)
			c.connect(typ, url, conptr)
		}
		return
	}

	switch typ {
	case "Event", "Universal":
		handshake := fmt.Sprintf(`{"meta_event_type":"lifecycle","post_type":"meta_event","self_id":%d,"sub_type":"connect","time":%d}`, c.bot.Client.Uin, time.Now().Unix())
		err = conn.WriteMessage(websocket.TextMessage, []byte(handshake))
		if err != nil {
			log.Warnf("反向WebSocket 握手时出现错误: %v", err)
		}
	}

	log.Infof("已连接到反向WebSocket %s服务器 %v", typ, url)
	wrappedConn := &wsConn{conn: conn, apiCaller: api.NewCaller(c.bot)}
	if c.limiter != nil {
		wrappedConn.apiCaller.Use(c.limiter)
	}

	if conptr != nil {
		*conptr = wrappedConn
	}

	if typ != "Event" {
		go c.listenAPI(typ, url, wrappedConn)
	}
}

func (c *websocketClient) listenAPI(typ, url string, conn *wsConn) {
	defer func() { _ = conn.Close() }()
	for {
		buffer := global.NewBuffer()
		t, reader, err := conn.conn.NextReader()
		if err != nil {
			log.Warnf("监听反向WS %s时出现错误: %v", typ, err)
			break
		}
		_, err = buffer.ReadFrom(reader)
		if err != nil {
			log.Warnf("监听反向WS %s时出现错误: %v", typ, err)
			break
		}
		if t == websocket.TextMessage {
			go func(buffer *bytes.Buffer) {
				defer global.PutBuffer(buffer)
				conn.handleRequest(c.bot, buffer.Bytes())
			}(buffer)
		} else {
			global.PutBuffer(buffer)
		}
	}
	if c.reconnectInterval != 0 {
		time.Sleep(c.reconnectInterval)
		if typ == "API" { // Universal 不重连，避免多次重连
			go c.connect(typ, url, nil)
		}
	}
}

func (c *websocketClient) onBotPushEvent(typ, url string, conn **wsConn) func(e *coolq.Event) {
	return func(e *coolq.Event) {
		c.mu.Lock()
		defer c.mu.Unlock()

		flt := filter.Find(c.filter)
		if flt != nil && !flt.Eval(gjson.Parse(e.JSONString())) {
			log.Debugf("上报Event %s 到 WS服务器 时被过滤.", e.JSONBytes())
			return
		}

		log.Debugf("向反向WS %s服务器推送Event: %s", typ, e.JSONBytes())
		if err := (*conn).WriteText(e.JSONBytes()); err != nil {
			log.Warnf("向反向WS %s服务器推送 Event 时出现错误: %v", typ, err)
			_ = (*conn).Close()
			if c.reconnectInterval != 0 {
				time.Sleep(c.reconnectInterval)
				c.connect(typ, url, conn)
			}
		}
	}
}

func (s *webSocketServer) event(w http.ResponseWriter, r *http.Request) {
	status := checkAuth(r, s.token)
	if status != http.StatusOK {
		log.Warnf("已拒绝 %v 的 WebSocket 请求: Token鉴权失败(code:%d)", r.RemoteAddr, status)
		w.WriteHeader(status)
		return
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warnf("处理 WebSocket 请求时出现错误: %v", err)
		return
	}

	err = c.WriteMessage(websocket.TextMessage, []byte(s.handshake))
	if err != nil {
		log.Warnf("WebSocket 握手时出现错误: %v", err)
		_ = c.Close()
		return
	}

	log.Infof("接受 WebSocket 连接: %v (/event)", r.RemoteAddr)
	conn := &wsConn{conn: c, apiCaller: api.NewCaller(s.bot)}
	s.mu.Lock()
	s.eventConn = append(s.eventConn, conn)
	s.mu.Unlock()
}

func (s *webSocketServer) api(w http.ResponseWriter, r *http.Request) {
	status := checkAuth(r, s.token)
	if status != http.StatusOK {
		log.Warnf("已拒绝 %v 的 WebSocket 请求: Token鉴权失败(code:%d)", r.RemoteAddr, status)
		w.WriteHeader(status)
		return
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warnf("处理 WebSocket 请求时出现错误: %v", err)
		return
	}

	log.Infof("接受 WebSocket 连接: %v (/api)", r.RemoteAddr)
	conn := &wsConn{conn: c, apiCaller: api.NewCaller(s.bot)}
	if s.conf.RateLimit.Enabled {
		conn.apiCaller.Use(rateLimit(s.conf.RateLimit.Frequency, s.conf.RateLimit.Bucket))
	}
	s.listenAPI(conn)
}

func (s *webSocketServer) any(w http.ResponseWriter, r *http.Request) {
	status := checkAuth(r, s.token)
	if status != http.StatusOK {
		log.Warnf("已拒绝 %v 的 WebSocket 请求: Token鉴权失败(code:%d)", r.RemoteAddr, status)
		w.WriteHeader(status)
		return
	}

	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warnf("处理 WebSocket 请求时出现错误: %v", err)
		return
	}

	err = c.WriteMessage(websocket.TextMessage, []byte(s.handshake))
	if err != nil {
		log.Warnf("WebSocket 握手时出现错误: %v", err)
		_ = c.Close()
		return
	}

	log.Infof("接受 WebSocket 连接: %v (/)", r.RemoteAddr)
	conn := &wsConn{conn: c, apiCaller: api.NewCaller(s.bot)}
	if s.conf.RateLimit.Enabled {
		conn.apiCaller.Use(rateLimit(s.conf.RateLimit.Frequency, s.conf.RateLimit.Bucket))
	}
	s.mu.Lock()
	s.eventConn = append(s.eventConn, conn)
	s.mu.Unlock()
	s.listenAPI(conn)
}

func (s *webSocketServer) listenAPI(c *wsConn) {
	defer func() { _ = c.Close() }()
	for {
		buffer := global.NewBuffer()
		t, reader, err := c.conn.NextReader()
		if err != nil {
			break
		}
		_, err = buffer.ReadFrom(reader)
		if err != nil {
			break
		}

		if t == websocket.TextMessage {
			go func(buffer *bytes.Buffer) {
				defer global.PutBuffer(buffer)
				c.handleRequest(s.bot, buffer.Bytes())
			}(buffer)
		} else {
			global.PutBuffer(buffer)
		}
	}
}

func (c *wsConn) handleRequest(_ *coolq.CQBot, payload []byte) {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("处置WS命令时发生无法恢复的异常：%v\n%s", err, debug.Stack())
			_ = c.Close()
		}
	}()
	j := gjson.Parse(utils.B2S(payload))
	t := strings.TrimSuffix(j.Get("action").Str, "_async")
	log.Debugf("WS接收到API调用: %v 参数: %v", t, j.Get("params").Raw)
	ret := c.apiCaller.Call(t, j.Get("params"))
	if j.Get("echo").Exists() {
		ret["echo"] = j.Get("echo").Value()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	writer, _ := c.conn.NextWriter(websocket.TextMessage)
	_ = json.NewEncoder(writer).Encode(ret)
	_ = writer.Close()
}

func (s *webSocketServer) onBotPushEvent(e *coolq.Event) {
	flt := filter.Find(s.filter)
	if flt != nil && !flt.Eval(gjson.Parse(e.JSONString())) {
		log.Debugf("上报Event %s 到 WS客户端 时被过滤.", e.JSONBytes())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	j := 0
	for i := 0; i < len(s.eventConn); i++ {
		conn := s.eventConn[i]
		log.Debugf("向WS客户端推送Event: %s", e.JSONBytes())
		if err := conn.WriteText(e.JSONBytes()); err != nil {
			_ = conn.Close()
			conn = nil
			continue
		}
		if i != j {
			// i != j means that some connection has been closed.
			// use an in-place removal to avoid copying.
			s.eventConn[j] = conn
		}
		j++
	}
	s.eventConn = s.eventConn[:j]
}
