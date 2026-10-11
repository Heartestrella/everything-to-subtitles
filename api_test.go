package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobUploadProcessEditExport(t *testing.T) {
	if _, err := pythonPath(); err != nil {
		t.Skip("Python is unavailable")
	}
	root := t.TempDir()
	worker := filepath.Join(root, "worker")
	if err := os.Mkdir(worker, 0700); err != nil {
		t.Fatal(err)
	}
	script := `import json, sys
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump({"language":"en", "target_language":"zho_Hans", "segments":[{"start":0.25,"end":1.5,"text":"Hello","translation":"你好"}]}, f)
`
	if err := os.WriteFile(filepath.Join(worker, "worker.py"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := &App{jobs: map[string]*Job{}, queue: make(chan string, 4), dataDir: filepath.Join(root, "data"), workerDir: worker, ctx: ctx}
	if err := app.load(); err != nil {
		t.Fatal(err)
	}
	go app.runQueue()
	server := httptest.NewServer(app.routes())
	defer server.Close()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("options", `{"engine":"local","target_language":"zho_Hans"}`); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("fake media for stub worker")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/api/jobs", form.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("create: %d %s", response.StatusCode, data)
	}
	var created Job
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var completed Job
	for time.Now().Before(deadline) {
		res, err := http.Get(server.URL + "/api/jobs/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(res.Body).Decode(&completed)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "complete" {
			break
		}
		if completed.Status == "failed" {
			t.Fatalf("worker failed: %s", completed.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if completed.Status != "complete" || completed.Result == nil || len(completed.Result.Segments) != 1 {
		t.Fatalf("unexpected job: %+v", completed)
	}
	payload := strings.NewReader(`{"segments":[{"start":0.3,"end":1.6,"text":"Hello!","translation":"你好！"}]}`)
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/api/jobs/"+created.ID+"/segments", payload)
	request.Header.Set("Content-Type", "application/json")
	edited, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	edited.Body.Close()
	if edited.StatusCode != 200 {
		t.Fatalf("edit returned %d", edited.StatusCode)
	}
	exported, err := http.Get(server.URL + "/api/jobs/" + created.ID + "/export?format=srt&track=bilingual")
	if err != nil {
		t.Fatal(err)
	}
	defer exported.Body.Close()
	content, _ := io.ReadAll(exported.Body)
	if exported.StatusCode != 200 || !strings.Contains(string(content), "00:00:00,300 --> 00:00:01,600\nHello!\n你好！") {
		t.Fatalf("export: %d %q", exported.StatusCode, content)
	}
}
