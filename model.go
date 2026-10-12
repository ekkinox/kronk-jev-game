package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/ardanlabs/kronk/sdk/tools/libs"
	"github.com/ardanlabs/kronk/sdk/tools/models"
)

const defaultModel = "https://huggingface.co/chaoliangUNSW/Jev-Style-0.8B-Decision-v3-GGUF/resolve/main/Jev-Style-0.8B-Decision-v3-Q8_0.gguf"

// modelSource is the URL of a GGUF file on Hugging Face, of a model kronk
// recognizes as a decision model.
var modelSource = cmp.Or(os.Getenv("MODEL"), defaultModel)

// load installs llama.cpp and the model on first use, then loads the model.
func load() (*Brain, error) {
	// No deadline: the first run can download a model of several GB.
	ctx := context.Background()
	start := time.Now()

	// Keep kronk files in their own folder: kronk versions rewrite the catalog
	// in formats older ones can't read.
	if os.Getenv("KRONK_BASE_PATH") == "" {
		if home, err := os.UserHomeDir(); err == nil {
			os.Setenv("KRONK_BASE_PATH", filepath.Join(home, ".kronk-game"))
		}
	}

	lib, err := libs.New(libs.WithDetect(ctx, downloadLog), libs.WithValidation(true))
	if err != nil {
		return nil, err
	}
	if _, err := lib.Download(ctx, downloadLog); err != nil {
		return nil, fmt.Errorf("install llama.cpp: %w", err)
	}
	if err := kronk.Init(kronk.WithLibPath(lib.LibsPath())); err != nil {
		return nil, fmt.Errorf("init kronk: %w", err)
	}

	mdls, err := models.New()
	if err != nil {
		return nil, fmt.Errorf("init models: %w", err)
	}
	// Download only the model file: for some Clef files, kronk would also fetch
	// a vision projector that decisions never use.
	mp, err := mdls.DownloadURLs(ctx, downloadLog, []string{modelSource}, "", "")
	if err != nil {
		return nil, fmt.Errorf("install model: %w", err)
	}

	// A decision is one short prefill, so a small context window is plenty.
	// Pin it: on a GPU, auto-tune otherwise sizes it to the model's maximum and
	// allocates a KV cache the game never uses. One call is in flight at a time, so
	// one sequence is enough.
	krn, err := kronk.New(
		model.WithModelFiles(mp.ModelFiles),
		model.WithAutoTune(true),
		model.WithContextWindow(4096),
		model.WithNSeqMax(1),
	)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	if !krn.ModelInfo().IsDecisionModel {
		krn.Unload(context.Background())
		return nil, fmt.Errorf("model %q is not a decision model", modelSource)
	}

	// The first call after loading is slower. Make it here, so it does not
	// show as the model's first reaction. Its latency seeds the average that
	// projects the state.
	b := &Brain{krn: krn, Model: krn.ModelInfo().ID, Processor: lib.Processor()}
	b.ask = b.askModel
	d, err := b.ask(ctx, State{Runner: Running, Next: "rock", Distance: 10})
	if err != nil {
		krn.Unload(context.Background())
		return nil, fmt.Errorf("warm-up call: %w", err)
	}
	b.recent = d.Latency

	fmt.Printf("%s on %s, ready in %.1fs\n", b.Model, b.Processor, time.Since(start).Seconds())
	return b, nil
}

// Close unloads the model.
func (b *Brain) Close() {
	b.krn.Unload(context.Background())
}

// downloadLog shows kronk's download progress on a first run and drops its
// other logs.
func downloadLog(ctx context.Context, msg string, args ...any) {
	if strings.Contains(msg, "Downloading") {
		kronk.FmtLogger(ctx, msg, args...)
	}
}
