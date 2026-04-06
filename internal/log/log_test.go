package log

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_DefaultTextHandler(t *testing.T) {
	logger := New(Config{Level: "info", Format: "text", Output: "stderr"})
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNew_JSONHandler(t *testing.T) {
	logger := New(Config{Level: "info", Format: "json", Output: "stderr"})
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}
	for _, tt := range tests {
		got := parseLevel(tt.input)
		if got != tt.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseOutput_Stderr(t *testing.T) {
	w := parseOutput("stderr")
	if w != os.Stderr {
		t.Error("expected os.Stderr")
	}
}

func TestParseOutput_Stdout(t *testing.T) {
	w := parseOutput("stdout")
	if w != os.Stdout {
		t.Error("expected os.Stdout")
	}
}

func TestParseOutput_Empty(t *testing.T) {
	w := parseOutput("")
	if w != os.Stderr {
		t.Error("expected os.Stderr for empty string")
	}
}

func TestParseOutput_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	w := parseOutput(path)
	if w == os.Stderr || w == os.Stdout {
		t.Error("expected file writer")
	}

	// Write something to verify it works
	f, ok := w.(*os.File)
	if !ok {
		t.Fatal("expected *os.File")
	}
	f.Write([]byte("test log line\n"))
	f.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test log line") {
		t.Error("expected log content in file")
	}
}

func TestParseOutput_InvalidFile(t *testing.T) {
	// Use a path guaranteed to be invalid on all platforms
	w := parseOutput(string([]byte{0}) + "/invalid")
	if w != os.Stderr {
		t.Error("expected fallback to os.Stderr")
	}
}

func TestLoggerOutput(t *testing.T) {
	// Verify the logger actually writes structured output
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)

	logger.Info("test message", "key", "value")
	output := buf.String()

	if !strings.Contains(output, "test message") {
		t.Errorf("expected 'test message' in output: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected 'key=value' in output: %s", output)
	}
}
