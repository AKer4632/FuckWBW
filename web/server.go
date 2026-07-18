package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"fkw/pipeline"
)

// 每一次跑的上下文
type runState struct {
	logCh   chan string
	doneCh  chan struct{}
	result  pipeline.Result
	mu      sync.Mutex
	finished bool
}

var (
	runsMu sync.Mutex
	runs   = map[string]*runState{}
)

// ListenAndServe 启动 HTTP 服务并阻塞直到退出
func ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/run", handleRun)
	mux.HandleFunc("/run/stream", handleStream)
	mux.HandleFunc("/run/result", handleResult)
	fmt.Printf("[*] 监听 %s, 打开浏览器访问 http://localhost%s/\n", addr, addr)
	return http.ListenAndServe(addr, mux)
}

func newRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	account := r.FormValue("account")
	password := r.FormValue("password")
	stepStr := r.FormValue("stepNumber")
	if account == "" || password == "" {
		http.Error(w, "账号或密码不能为空", http.StatusBadRequest)
		return
	}
	stepNumber := 18000
	if stepStr != "" {
		if v, err := strconv.Atoi(stepStr); err == nil && v >= 1000 {
			stepNumber = v
		}
	}

	runID := newRunID()
	state := &runState{
		logCh:  make(chan string, 128),
		doneCh: make(chan struct{}),
	}
	runsMu.Lock()
	runs[runID] = state
	runsMu.Unlock()

	// 异步跑
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		logSink := func(line string) {
			select {
			case state.logCh <- line:
			default:
				// 满了就丢，不阻塞流程
			}
		}
		result := pipeline.Run(ctx, logSink, account, password, stepNumber)
		state.mu.Lock()
		state.result = result
		state.finished = true
		state.mu.Unlock()
		close(state.doneCh)
		close(state.logCh)
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"runId": runID})
}

func handleStream(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	runsMu.Lock()
	state, ok := runs[runID]
	runsMu.Unlock()
	if !ok {
		http.Error(w, "unknown runId", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// 先发一帧 hello 触发 onopen
	_, _ = fmt.Fprintf(w, "data: [+] 已建立日志连接 (runId=%s)\n\n", runID)
	flusher.Flush()

	for {
		select {
		case line, more := <-state.logCh:
			if !more {
				_, _ = fmt.Fprintf(w, "data: __DONE__\n\n")
				flusher.Flush()
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonEscape(line))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func handleResult(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	runsMu.Lock()
	state, ok := runs[runID]
	runsMu.Unlock()
	if !ok {
		http.Error(w, "unknown runId", http.StatusNotFound)
		return
	}

	state.mu.Lock()
	finished := state.finished
	result := state.result
	state.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"finished": finished,
		"result":   result,
	})
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// 去掉首尾的引号
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
