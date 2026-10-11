package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Segment struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Text        string  `json:"text"`
	Translation string  `json:"translation"`
}
type Result struct {
	Language       string    `json:"language"`
	TargetLanguage string    `json:"target_language"`
	Alignment      string    `json:"alignment,omitempty"`
	AlignmentNote  string    `json:"alignment_note,omitempty"`
	Segments       []Segment `json:"segments"`
}
type Options struct {
	Mode             string `json:"mode"`
	Engine           string `json:"engine"`
	Model            string `json:"model"`
	SourceLanguage   string `json:"source_language"`
	TargetLanguage   string `json:"target_language"`
	ComputeType      string `json:"compute_type"`
	BatchSize        int    `json:"batch_size"`
	TranslationModel string `json:"translation_model"`
}
type Job struct {
	ID        string    `json:"id"`
	FileName  string    `json:"file_name"`
	InputFile string    `json:"input_file"`
	Status    string    `json:"status"`
	Stage     string    `json:"stage"`
	Detail    string    `json:"detail"`
	Engine    string    `json:"engine"`
	Options   Options   `json:"options"`
	Result    *Result   `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type App struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	queue     chan string
	dataDir   string
	workerDir string
	webDir    string
	ctx       context.Context
	authMu    sync.Mutex
	auth      *colabAuthFlow
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "colab" {
		if err := runColabCLI(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	address := flag.String("listen", "127.0.0.1:8765", "WebUI address")
	dataDir := flag.String("data", "data", "persistent job directory")
	workerDir := flag.String("worker", "worker", "Python worker directory")
	webDir := flag.String("web", "web/dist", "built Vue directory")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := &App{jobs: make(map[string]*Job), queue: make(chan string, 256), dataDir: *dataDir,
		workerDir: *workerDir, webDir: *webDir, ctx: ctx}
	if err := app.load(); err != nil {
		log.Fatal(err)
	}
	go app.runQueue()
	server := &http.Server{Addr: *address, Handler: app.routes(), ReadHeaderTimeout: 15 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Subtitle Studio: http://%s", *address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (a *App) load() error {
	if err := os.MkdirAll(a.dataDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(a.dataDir)
	if err != nil {
		return err
	}
	pending := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.dataDir, entry.Name(), "job.json"))
		if err != nil {
			continue
		}
		var job Job
		if json.Unmarshal(data, &job) != nil || job.ID != entry.Name() {
			continue
		}
		if job.Status == "running" {
			job.Status, job.Stage, job.Detail = "queued", "queued", "服务重启后重新排队"
		}
		a.jobs[job.ID] = &job
		if job.Status == "queued" {
			pending = append(pending, job.ID)
		}
	}
	go func() {
		for _, id := range pending {
			select {
			case a.queue <- id:
			case <-a.ctx.Done():
				return
			}
		}
	}()
	return nil
}
func (a *App) jobDir(id string) string { return filepath.Join(a.dataDir, id) }
func (a *App) saveLocked(job *Job) error {
	job.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(a.jobDir(job.ID), "job.json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func (a *App) change(id string, fn func(*Job)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if job := a.jobs[id]; job != nil {
		fn(job)
		if err := a.saveLocked(job); err != nil {
			log.Printf("save %s: %v", id, err)
		}
	}
}
func (a *App) snapshot(id string) (Job, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	job, ok := a.jobs[id]
	if !ok {
		return Job{}, false
	}
	copy := *job
	if job.Result != nil {
		result := *job.Result
		result.Segments = append([]Segment(nil), result.Segments...)
		copy.Result = &result
	}
	return copy, true
}
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("POST /api/colab/connect", a.connectColab)
	mux.HandleFunc("POST /api/colab/code", a.finishColabAuth)
	mux.HandleFunc("GET /api/jobs", a.listJobs)
	mux.HandleFunc("POST /api/jobs", a.createJob)
	mux.HandleFunc("GET /api/jobs/{id}", a.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/retry", a.retryJob)
	mux.HandleFunc("GET /api/jobs/{id}/media", a.media)
	mux.HandleFunc("PUT /api/jobs/{id}/segments", a.editSegments)
	mux.HandleFunc("GET /api/jobs/{id}/export", a.export)
	mux.HandleFunc("/", a.frontend)
	return mux
}
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	_, colab := a.installedColab()
	_, python := pythonPath()
	_, ffmpeg := exec.LookPath("ffmpeg")
	jsonResponse(w, 200, map[string]any{"colab_cli": colab, "colab_managed": true, "python": python == nil, "ffmpeg": ffmpeg == nil})
}
func (a *App) listJobs(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	jobs := make([]Job, 0, len(a.jobs))
	for _, job := range a.jobs {
		copy := *job
		copy.Result = nil
		jobs = append(jobs, copy)
	}
	a.mu.RUnlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	jsonResponse(w, 200, jobs)
}
func idValid(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func (a *App) getJob(w http.ResponseWriter, r *http.Request) {
	job, ok := a.snapshot(r.PathValue("id"))
	if !ok {
		apiError(w, 404, "任务不存在")
		return
	}
	jsonResponse(w, 200, job)
}

func (a *App) retryJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	job := a.jobs[id]
	if job == nil || job.Status != "failed" {
		a.mu.Unlock()
		apiError(w, 409, "只能重试失败的任务")
		return
	}
	previous := *job
	job.Status, job.Stage, job.Detail, job.Error = "queued", "queued", "等待重新处理", ""
	job.Result = nil
	if err := a.saveLocked(job); err != nil {
		*job = previous
		a.mu.Unlock()
		apiError(w, 500, "无法保存任务状态")
		return
	}
	copy := *job
	a.mu.Unlock()
	select {
	case a.queue <- id:
		jsonResponse(w, 200, copy)
	case <-a.ctx.Done():
		apiError(w, 503, "服务正在关闭")
	}
}
func cleanOptions(o *Options) error {
	if o.Mode == "" {
		o.Mode = "transcribe"
	}
	if o.Mode != "transcribe" && o.Mode != "subtitle" {
		return errors.New("无效的处理模式")
	}
	if o.Engine == "" {
		o.Engine = "auto"
	}
	if o.Model == "" {
		o.Model = "medium"
	}
	if o.TargetLanguage == "" {
		o.TargetLanguage = "zho_Hans"
	}
	if o.ComputeType == "" {
		o.ComputeType = "float16"
	}
	if o.BatchSize == 0 {
		o.BatchSize = 8
	}
	if o.TranslationModel == "" {
		o.TranslationModel = "facebook/nllb-200-distilled-600M"
	}
	if o.Engine != "auto" && o.Engine != "colab" && o.Engine != "local" {
		return errors.New("无效的执行端")
	}
	if o.BatchSize < 1 || o.BatchSize > 32 {
		return errors.New("批大小应为 1–32")
	}
	if o.ComputeType != "float16" && o.ComputeType != "int8" {
		return errors.New("无效的计算精度")
	}
	if o.Model != "small" && o.Model != "medium" && o.Model != "large-v3" {
		return errors.New("无效的识别模型")
	}
	langs := map[string]bool{"": true, "en": true, "zh": true, "ja": true, "ko": true, "fr": true, "de": true, "es": true, "ru": true, "pt": true, "it": true, "ar": true, "hi": true, "id": true, "vi": true, "th": true, "tr": true, "uk": true}
	targets := map[string]bool{"zho_Hans": true, "zho_Hant": true, "eng_Latn": true, "jpn_Jpan": true, "kor_Hang": true, "fra_Latn": true, "deu_Latn": true, "spa_Latn": true, "rus_Cyrl": true, "por_Latn": true, "ita_Latn": true, "arb_Arab": true, "hin_Deva": true, "ind_Latn": true, "vie_Latn": true, "tha_Thai": true, "tur_Latn": true, "ukr_Cyrl": true}
	if !langs[o.SourceLanguage] || !targets[o.TargetLanguage] {
		return errors.New("不支持的语言选项")
	}
	if o.TranslationModel != "facebook/nllb-200-distilled-600M" {
		return errors.New("暂不支持自定义翻译模型")
	}
	return nil
}
func (a *App) createJob(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		apiError(w, 400, "无法读取媒体文件，或文件超过 4 GB")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	var options Options
	if err := json.Unmarshal([]byte(r.FormValue("options")), &options); err != nil {
		apiError(w, 400, "任务参数不是有效 JSON")
		return
	}
	if err := cleanOptions(&options); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	source, header, err := r.FormFile("file")
	subtitle, subtitleHeader, subtitleErr := r.FormFile("subtitle")
	if err != nil && subtitleErr != nil && options.Mode != "subtitle" {
		apiError(w, 400, "请选择音频或视频文件")
		return
	}
	if source != nil {
		defer source.Close()
	}
	if source == nil && subtitle == nil {
		apiError(w, 400, "请选择音频或视频文件")
		return
	}
	var ext string
	if header != nil {
		ext = strings.ToLower(filepath.Ext(header.Filename))
	}
	allowed := map[string]bool{".mp4": true, ".mkv": true, ".mov": true, ".webm": true, ".mp3": true, ".wav": true, ".m4a": true, ".flac": true, ".aac": true, ".ogg": true}
	if source != nil && !allowed[ext] {
		apiError(w, 400, "不支持该媒体格式")
		return
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		apiError(w, 500, "无法创建任务 ID")
		return
	}
	id := hex.EncodeToString(idBytes)
	dir := a.jobDir(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		apiError(w, 500, "无法创建任务目录")
		return
	}
	name := ""
	if source != nil {
		name = "input" + ext
		file, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		if e != nil {
			apiError(w, 500, "无法保存媒体文件")
			return
		}
		size, copyErr := io.Copy(file, source)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || size == 0 {
			_ = os.RemoveAll(dir)
			apiError(w, 400, "上传失败或文件为空")
			return
		}
	}
	now := time.Now()
	fileName := "导入字幕"
	if header != nil {
		fileName = filepath.Base(header.Filename)
	} else if subtitleHeader != nil {
		fileName = filepath.Base(subtitleHeader.Filename)
	}
	job := &Job{ID: id, FileName: fileName, InputFile: name,
		Status: "queued", Stage: "queued", Detail: "等待执行", Options: options, CreatedAt: now, UpdatedAt: now}
	if options.Mode == "subtitle" {
		if subtitle != nil {
			data, readErr := io.ReadAll(io.LimitReader(subtitle, 16<<20))
			subtitle.Close()
			if readErr != nil {
				apiError(w, 400, "无法读取字幕文件")
				return
			}
			segments, parseErr := parseSubtitleFile(string(data), filepath.Ext(subtitleHeader.Filename))
			if parseErr != nil {
				apiError(w, 400, parseErr.Error())
				return
			}
			job.Status, job.Stage, job.Detail = "complete", "complete", fmt.Sprintf("已导入 %d 条字幕", len(segments))
			job.Result = &Result{Segments: segments, Alignment: "imported"}
		} else {
			job.Status, job.Stage, job.Detail = "complete", "complete", "只处理模式，等待手动添加字幕"
			job.Result = &Result{Segments: nil, Alignment: "manual"}
		}
	}
	a.mu.Lock()
	a.jobs[id] = job
	err = a.saveLocked(job)
	a.mu.Unlock()
	if err != nil {
		apiError(w, 500, "无法保存任务记录")
		return
	}
	if options.Mode != "subtitle" {
		a.queue <- id
	}
	jsonResponse(w, 201, job)
}
func (a *App) media(w http.ResponseWriter, r *http.Request) {
	job, ok := a.snapshot(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(a.jobDir(job.ID), job.InputFile))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, job.FileName, info.ModTime(), file)
}
func (a *App) editSegments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload struct {
		Segments []Segment `json:"segments"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		apiError(w, 400, "字幕数据无效")
		return
	}
	if len(payload.Segments) > 50000 {
		apiError(w, 400, "字幕条数过多")
		return
	}
	for _, segment := range payload.Segments {
		if segment.Start < 0 || segment.End <= segment.Start || segment.End > 86400*7 || len(segment.Text) > 4000 || len(segment.Translation) > 4000 {
			apiError(w, 400, "字幕时间或内容无效")
			return
		}
	}
	a.mu.Lock()
	job := a.jobs[id]
	if job == nil || job.Status != "complete" || job.Result == nil {
		a.mu.Unlock()
		apiError(w, 409, "任务尚未完成")
		return
	}
	original := job.Result.Segments
	job.Result.Segments = payload.Segments
	if err := a.saveLocked(job); err != nil {
		job.Result.Segments = original
		a.mu.Unlock()
		apiError(w, 500, "保存字幕失败")
		return
	}
	copy := *job
	result := *job.Result
	result.Segments = append([]Segment(nil), job.Result.Segments...)
	copy.Result = &result
	a.mu.Unlock()
	jsonResponse(w, 200, copy)
}
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	job, ok := a.snapshot(r.PathValue("id"))
	if !ok || job.Result == nil {
		apiError(w, 404, "尚无可导出的字幕")
		return
	}
	format := r.URL.Query().Get("format")
	track := r.URL.Query().Get("track")
	if format != "srt" && format != "vtt" && format != "ass" {
		apiError(w, 400, "仅支持 ASS、SRT 或 VTT")
		return
	}
	if track != "original" && track != "translation" && track != "bilingual" {
		apiError(w, 400, "请选择字幕轨道")
		return
	}
	content := renderSubtitles(job.Result.Segments, format, track)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-%s.%s\"", job.ID, track, format))
	_, _ = io.WriteString(w, content)
}
func (a *App) frontend(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(a.webDir, filepath.FromSlash(strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	index := filepath.Join(a.webDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.Error(w, "先在 web 目录运行 npm install && npm run build", 503)
		return
	}
	http.ServeFile(w, r, index)
}
func pythonPath() (string, error) {
	if path := os.Getenv("SUBTITLE_PYTHON"); path != "" {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		return exec.LookPath("python")
	}
	return exec.LookPath("python3")
}
func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
