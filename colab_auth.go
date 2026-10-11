package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var googleAuthURL = regexp.MustCompile(`https://accounts\.google\.com/[^\s]+`)

type colabAuthFlow struct {
	stdin  io.WriteCloser
	done   chan error
	cancel context.CancelFunc
	url    string
}

func sameOrigin(r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}

func (a *App) connectColab(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apiError(w, 403, "请求来源无效")
		return
	}
	state, link, err := a.startColabAuth()
	if err != nil {
		apiError(w, 503, err.Error())
		return
	}
	jsonResponse(w, 200, map[string]string{"status": state, "url": link})
}

func (a *App) startColabAuth() (string, string, error) {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if a.auth != nil {
		select {
		case <-a.auth.done:
			a.auth.cancel()
			a.auth = nil
		default:
			return "awaiting_code", a.auth.url, nil
		}
	}
	colab, err := a.ensureColab(a.ctx, "")
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	cmd := exec.CommandContext(ctx, colab, "sessions")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return "", "", err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		cancel()
		return "", "", err
	}
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		cancel()
		return "", "", err
	}
	writer.Close()
	flow := &colabAuthFlow{stdin: stdin, done: make(chan error, 1), cancel: cancel}
	links := make(chan string, 1)
	go func() {
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 32*1024), 1024*1024)
		for scanner.Scan() {
			if found := googleAuthURL.FindString(scanner.Text()); found != "" {
				parsed, parseErr := url.Parse(found)
				if parseErr == nil && parsed.Scheme == "https" && parsed.Hostname() == "accounts.google.com" {
					select {
					case links <- found:
					default:
					}
				}
			}
		}
		flow.done <- cmd.Wait()
		close(flow.done)
	}()
	select {
	case link := <-links:
		flow.url = link
		a.auth = flow
		return "awaiting_code", link, nil
	case err := <-flow.done:
		cancel()
		if err == nil {
			return "connected", "", nil
		}
		return "", "", errors.New("Colab 授权无法启动；请检查网络或运行 go run . colab sessions")
	case <-time.After(30 * time.Second):
		cancel()
		return "", "", errors.New("等待 Google 授权链接超时")
	}
}

func (a *App) finishColabAuth(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apiError(w, 403, "请求来源无效")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var payload struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&payload) != nil {
		apiError(w, 400, "授权码格式无效")
		return
	}
	code := strings.TrimSpace(payload.Code)
	if len(code) < 8 || len(code) > 4096 || strings.ContainsAny(code, "\r\n") {
		apiError(w, 400, "授权码格式无效")
		return
	}
	a.authMu.Lock()
	flow := a.auth
	a.auth = nil
	a.authMu.Unlock()
	if flow == nil {
		apiError(w, 409, "请先获取授权链接")
		return
	}
	defer flow.cancel()
	if _, err := io.WriteString(flow.stdin, code+"\n"); err != nil {
		apiError(w, 503, "授权会话已结束，请重新连接")
		return
	}
	flow.stdin.Close()
	select {
	case err := <-flow.done:
		if err != nil {
			apiError(w, 401, "Google 授权未完成，请检查授权码后重新连接")
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "connected"})
	case <-time.After(45 * time.Second):
		apiError(w, 504, "等待 Google 授权确认超时")
	}
}
