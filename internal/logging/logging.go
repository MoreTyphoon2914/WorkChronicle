package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"worktracker/internal/config"
)

type rotatingWriter struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
	file    *os.File
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func newWriter(path string, max int64, backups int) (*rotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &rotatingWriter{path: path, max: max, backups: backups, file: f}, nil
}
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, err := w.file.Stat(); err == nil && w.max > 0 && st.Size()+int64(len(p)) > w.max {
		w.rotate()
	}
	return w.file.Write(p)
}
func (w *rotatingWriter) rotate() {
	_ = w.file.Close()
	for i := w.backups - 1; i >= 1; i-- {
		_ = os.Rename(w.path+"."+itoa(i), w.path+"."+itoa(i+1))
	}
	if w.backups > 0 {
		_ = os.Rename(w.path, w.path+".1")
	} else {
		_ = os.Remove(w.path)
	}
	w.file, _ = os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
}
func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return "many"
}
func New(c config.Logging, stderr io.Writer) (*slog.Logger, io.Closer, error) {
	w, err := newWriter(c.File, c.MaxBytes, c.MaxBackups)
	if err != nil {
		return nil, nil, err
	}
	level := slog.LevelInfo
	if c.Level == "debug" {
		level = slog.LevelDebug
	} else if c.Level == "warn" {
		level = slog.LevelWarn
	} else if c.Level == "error" {
		level = slog.LevelError
	}
	out := io.MultiWriter(w, stderr)
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level})), w, nil
}
