package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type Fetch func(context.Context, io.Writer) error
type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("artifact exceeds download size limit")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func Download(ctx context.Context, directory, name string, limit int64, fetch Fetch) (path string, result error) {
	if !filepath.IsAbs(directory) || limit <= 0 || fetch == nil {
		return "", fmt.Errorf("invalid artifact destination")
	}
	name = SafeName(name)
	if name == "" {
		return "", fmt.Errorf("empty artifact filename")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe artifact directory")
	}
	file, err := os.CreateTemp(directory, ".ci-download-*")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	closed := false
	defer func() {
		if !closed {
			result = errors.Join(result, file.Close())
		}
		if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}()
	if err := fetch(ctx, &limitedWriter{writer: file, remaining: limit}); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		closed = true
		return "", err
	}
	closed = true
	path = filepath.Join(directory, name)
	if err := os.Link(temporary, path); err != nil {
		return "", fmt.Errorf("save artifact without overwriting existing files: %w", err)
	}
	return path, nil
}
func SafeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r) {
			return r
		}
		return '_'
	}, name)
	name = strings.Trim(name, "._")
	if len(name) > 160 {
		name = name[:160]
	}
	return name
}
