package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func jsonResponse(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("writing response: %v", err)
	}
}

func apiError(w http.ResponseWriter, code int, message string) {
	jsonResponse(w, code, map[string]string{"error": message})
}

func newHandler(ctx context.Context, m *monitor, demo bool, webDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		data, err := m.overview(r.Context(), demo)
		if err != nil {
			log.Printf("reading overview: %v", err)
			apiError(w, http.StatusInternalServerError, "读取服务器状态失败")
			return
		}
		jsonResponse(w, http.StatusOK, data)
	})
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		rangeName := query.Get("range")
		var duration time.Duration
		switch rangeName {
		case "", "24h":
			duration = 24 * time.Hour
		case "7d":
			duration = 7 * 24 * time.Hour
		case "30d":
			duration = 30 * 24 * time.Hour
		default:
			apiError(w, http.StatusBadRequest, "无效的时间范围")
			return
		}
		serverID := query.Get("server")
		ids := make([]string, 0, len(m.config.Servers))
		for _, server := range m.config.Servers {
			if serverID == "" || serverID == "all" || serverID == server.ID {
				ids = append(ids, server.ID)
			}
		}
		if len(ids) == 0 {
			apiError(w, http.StatusNotFound, "没有找到该实例")
			return
		}
		page := 1
		if value := query.Get("page"); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 1 {
				apiError(w, http.StatusBadRequest, "无效的页码")
				return
			}
			page = number
		}
		to := time.Now().UTC().Truncate(time.Millisecond)
		step := max(m.config.interval(), (duration / 360).Truncate(time.Second))
		data, err := m.store.history(r.Context(), ids, to.Add(-duration), to, step, page)
		if err != nil {
			log.Printf("reading history: %v", err)
			apiError(w, http.StatusInternalServerError, "读取历史记录失败")
			return
		}
		jsonResponse(w, http.StatusOK, data)
	})
	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Monitor-Refresh") != "1" {
			apiError(w, http.StatusForbidden, "缺少刷新请求标头")
			return
		}
		// Once started, a sample should be saved even if the browser disconnects.
		retry, err := m.collect(ctx)
		switch err {
		case errCollecting:
			apiError(w, http.StatusConflict, "正在采集，请稍候")
		case errCooldown:
			seconds := int(math.Ceil(retry.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			apiError(w, http.StatusTooManyRequests, "采集间隔至少为 1 分钟")
		case nil:
			jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			log.Printf("collecting samples: %v", err)
			apiError(w, http.StatusInternalServerError, "保存采样失败")
		}
	})

	files := os.DirFS(webDir)
	mux.HandleFunc("GET /assets/{name}", func(w http.ResponseWriter, r *http.Request) {
		asset := r.PathValue("name")
		if asset != filepath.Base(asset) || asset == "." || asset == ".." {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, files, "assets/"+asset)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if _, err := os.Stat(filepath.Join(webDir, "index.html")); err != nil {
			http.Error(w, "Build the frontend with npm --prefix web ci && npm --prefix web run build", http.StatusServiceUnavailable)
			return
		}
		http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
	})
	return protect(m.config, mux)
}

func protect(c config, next http.Handler) http.Handler {
	wantUser, wantPass := sha256.Sum256([]byte(c.user)), sha256.Sum256([]byte(c.password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if c.user != "" {
			user, password, ok := r.BasicAuth()
			userHash, passHash := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
			match := subtle.ConstantTimeCompare(userHash[:], wantUser[:]) & subtle.ConstantTimeCompare(passHash[:], wantPass[:])
			if !ok || match != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="VPS Monitor", charset="UTF-8"`)
				apiError(w, http.StatusUnauthorized, "需要登录")
				return
			}
		} else {
			host := r.Host
			if value, _, err := net.SplitHostPort(host); err == nil {
				host = value
			}
			if host != "localhost" && !net.ParseIP(strings.Trim(host, "[]")).IsLoopback() {
				apiError(w, http.StatusForbidden, "仅允许本机访问")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func run() error {
	demo := flag.Bool("demo", false, "show two simulated VPS instances without credentials")
	path := flag.String("config", "config.json", "configuration file")
	listen := flag.String("listen", "", "override the listen address")
	webDir := flag.String("web", "web/dist", "built frontend directory")
	flag.Parse()

	c, err := loadConfig(*path, *demo, *listen)
	if err != nil {
		return err
	}
	db, err := openStore(c.Database, *demo)
	if err != nil {
		return err
	}
	defer db.db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	collector := &monitor{config: c, store: db, fetch: kiwiFetcher()}
	if *demo {
		now := time.Now().UTC().Truncate(time.Millisecond)
		collector.fetch = demoFetcher(now)
		if err := collector.seedDemo(ctx, now); err != nil {
			return err
		}
	}

	server := &http.Server{
		Addr: c.Listen, Handler: newHandler(ctx, collector, *demo, *webDir),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 35 * time.Second, IdleTimeout: time.Minute,
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	log.Printf("VPS Monitor · http://%s (demo=%t)", listener.Addr(), *demo)
	done := make(chan struct{})
	go func() {
		defer close(done)
		collector.run(ctx, !*demo)
	}()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case err := <-serveErr:
		stop()
		<-done
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		<-done
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
