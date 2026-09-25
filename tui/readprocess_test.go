package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadProcessCancellationKillsInheritedPipesAndKeepsCheckpoint(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env python3\nimport subprocess, pathlib, time\nsubprocess.Popen(['sleep', '30'])\npathlib.Path('checkpoint').write_text('committed')\ntime.sleep(30)\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "enrich-one"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	p := &readProcess{}
	done := make(chan error, 1)
	go func() { _, err := runReadScript(p, root, "enrich-one"); done <- err }()
	t.Cleanup(p.stop)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "checkpoint")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("script never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel left child pipes alive")
	}
	if b, err := os.ReadFile(filepath.Join(root, "checkpoint")); err != nil || string(b) != "committed" {
		t.Fatalf("checkpoint lost: %q %v", b, err)
	}
}
