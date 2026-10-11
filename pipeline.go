package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func (a *App) runQueue() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case id := <-a.queue:
			job, ok := a.snapshot(id)
			if !ok || job.Status != "queued" {
				continue
			}
			a.process(job)
		}
	}
}

func (a *App) process(job Job) {
	a.change(job.ID, func(j *Job) {
		j.Status, j.Stage, j.Detail = "running", "preparing", "正在准备执行环境"
		j.Error = ""
	})
	dir := a.jobDir(job.ID)
	configPath := filepath.Join(dir, "config.json")
	resultPath := filepath.Join(dir, "result.json")
	configData, _ := json.Marshal(job.Options)
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		a.fail(job.ID, err)
		return
	}
	source := filepath.Join(dir, job.InputFile)
	var err error
	switch job.Options.Engine {
	case "colab":
		err = a.runColab(job.ID, source, configPath, resultPath)
	case "local":
		err = a.runLocal(job.ID, source, configPath, resultPath)
	default:
		err = a.runColab(job.ID, source, configPath, resultPath)
		if err != nil && a.ctx.Err() == nil {
			colabErr := err
			log.Printf("colab job %s failed: %v; trying local GPU", job.ID, err)
			a.change(job.ID, func(j *Job) { j.Stage, j.Detail = "fallback", "Colab 执行失败，尝试本机 GPU" })
			if localErr := a.runLocal(job.ID, source, configPath, resultPath); localErr != nil {
				err = fmt.Errorf("Colab: %v；本机 GPU: %w", colabErr, localErr)
			} else {
				err = nil
			}
		}
	}
	if err != nil {
		a.fail(job.ID, err)
		return
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		a.fail(job.ID, fmt.Errorf("结果文件不存在: %w", err))
		return
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		a.fail(job.ID, fmt.Errorf("结果文件无效: %w", err))
		return
	}
	a.change(job.ID, func(j *Job) {
		j.Result = &result
		j.Status, j.Stage, j.Detail = "complete", "complete", fmt.Sprintf("已生成 %d 条字幕", len(result.Segments))
	})
}

func (a *App) fail(id string, err error) {
	log.Printf("job %s: %v", id, err)
	a.change(id, func(j *Job) { j.Status, j.Stage = "failed", "failed"; j.Error = err.Error(); j.Detail = "处理失败" })
}

func (a *App) runLocal(id, source, config, result string) error {
	python, err := pythonPath()
	if err != nil {
		return errors.New("未找到 Python；请安装 Python 与 worker/requirements.txt")
	}
	a.change(id, func(j *Job) { j.Engine, j.Stage, j.Detail = "local", "preparing", "正在使用本机 GPU" })
	script := filepath.Join(a.workerDir, "worker.py")
	return a.runCommand(a.ctx, id, python, script, source, config, result)
}

func (a *App) runColab(id, source, config, result string) (resultErr error) {
	colab, err := a.ensureColab(a.ctx, id)
	if err != nil {
		return err
	}
	session := "sub-" + id[:12]
	a.change(id, func(j *Job) { j.Engine, j.Stage, j.Detail = "colab", "preparing", "正在申请 Colab GPU" })
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := a.runCommand(cleanup, id, colab, "stop", "-s", session); err != nil {
			log.Printf("stop Colab session %s: %v", session, err)
		}
	}()
	if err := a.runCommand(a.ctx, id, colab, "new", "-s", session, "--gpu", "T4"); err != nil {
		return fmt.Errorf("创建 Colab 会话失败: %w", err)
	}
	steps := [][]string{
		{"install", "-s", session, "-r", filepath.Join(a.workerDir, "requirements.txt")},
		{"upload", "-s", session, source, "/content/subtitle_input" + filepath.Ext(source)},
		{"upload", "-s", session, config, "/content/subtitle_config.json"},
		{"upload", "-s", session, filepath.Join(a.workerDir, "worker.py"), "/content/subtitle_worker.py"},
	}
	for i, args := range steps {
		a.change(id, func(j *Job) {
			j.Stage = "preparing"
			j.Detail = []string{"正在安装模型依赖", "正在上传媒体", "正在上传任务设置", "正在上传处理脚本"}[i]
		})
		if err := a.runCommand(a.ctx, id, colab, args...); err != nil {
			return fmt.Errorf("Colab %s 失败: %w", args[0], err)
		}
	}
	bootstrap := filepath.Join(a.jobDir(id), "bootstrap.py")
	input := "/content/subtitle_input" + filepath.Ext(source)
	code := fmt.Sprintf("import runpy, sys\nsys.argv = ['subtitle_worker.py', %q, '/content/subtitle_config.json', '/content/subtitle_result.json']\nrunpy.run_path('/content/subtitle_worker.py', run_name='__main__')\n", input)
	if err := os.WriteFile(bootstrap, []byte(code), 0600); err != nil {
		return err
	}
	if err := a.runCommand(a.ctx, id, colab, "exec", "-s", session, "-f", bootstrap); err != nil {
		return fmt.Errorf("Colab 推理失败: %w", err)
	}
	if err := a.runCommand(a.ctx, id, colab, "download", "-s", session, "/content/subtitle_result.json", result); err != nil {
		return fmt.Errorf("下载字幕结果失败: %w", err)
	}
	return nil
}

func (a *App) runCommand(ctx context.Context, id, program string, args ...string) error {
	command := exec.CommandContext(ctx, program, args...)
	isColab := strings.HasPrefix(strings.ToLower(filepath.Base(program)), "colab")
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	var last bytes.Buffer
	var lastMu sync.Mutex
	consume := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var event struct {
				Stage  string `json:"stage"`
				Detail string `json:"detail"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Stage != "" {
				a.change(id, func(j *Job) { j.Stage, j.Detail = event.Stage, event.Detail })
			} else {
				if !isColab {
					log.Printf("[%s] %s", id, line)
				}
				lastMu.Lock()
				last.WriteString(line)
				last.WriteByte('\n')
				if last.Len() > 4096 {
					data := last.Bytes()
					last.Reset()
					last.Write(data[len(data)-2048:])
				}
				lastMu.Unlock()
			}
		}
	}
	done := make(chan struct{})
	go func() { consume(stderr); close(done) }()
	consume(output)
	<-done
	if err := command.Wait(); err != nil {
		detail := strings.TrimSpace(last.String())
		if isColab {
			lower := strings.ToLower(detail)
			switch {
			case strings.Contains(lower, "401"), strings.Contains(lower, "403"), strings.Contains(lower, "credential"), strings.Contains(lower, "unauthorized"):
				return errors.New("Colab 认证或权限不足；请用 colab sessions 检查登录状态")
			case strings.Contains(lower, "quota"), strings.Contains(lower, "accelerator"), strings.Contains(lower, "gpu"):
				return errors.New("Colab GPU 无法分配；请检查账号配额和可用资源")
			default:
				return fmt.Errorf("%w；请在本机检查 Colab CLI 日志", err)
			}
		}
		if len(detail) > 400 {
			detail = detail[len(detail)-400:]
		}
		if detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}
