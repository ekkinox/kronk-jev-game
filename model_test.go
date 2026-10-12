package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDefaultModel(t *testing.T) {
	t.Parallel()
	if !strings.HasPrefix(defaultModel, "https://huggingface.co/") || !strings.HasSuffix(defaultModel, ".gguf") {
		t.Errorf("default model %q is not a GGUF file on Hugging Face", defaultModel)
	}
	if !strings.Contains(defaultModel, "/resolve/") {
		t.Errorf("default model %q is a page, not a download link", defaultModel)
	}
}

func TestDownloadLog(t *testing.T) {
	out := captureStdout(t, func() {
		downloadLog(context.Background(), "Downloading model", "progress", "42%")
		downloadLog(context.Background(), "loading library", "path", "/tmp")
	})
	if !strings.Contains(out, "Downloading model") || !strings.Contains(out, "42%") {
		t.Errorf("download progress not shown, got %q", out)
	}
	if strings.Contains(out, "loading library") {
		t.Errorf("other logs shown, got %q", out)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
