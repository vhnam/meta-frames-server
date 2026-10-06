// Package storage keeps uploaded scan files. Only a local-disk implementation exists;
// swap it for object storage before deploying anywhere with an ephemeral filesystem.
package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrNotFound = errors.New("storage: not found")

type Store interface {
	// Put writes content under key and returns the number of bytes written.
	Put(key string, content io.Reader) (int64, error)
	Open(key string) (io.ReadCloser, int64, error)
	Delete(key string) error
}

type Local struct{ directory string }

func NewLocal(directory string) (*Local, error) {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	return &Local{directory: directory}, nil
}

// pathFor maps a key to a file inside the directory, refusing keys that could escape it.
func (local *Local) pathFor(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, `/\`) || key == "." || key == ".." {
		return "", errors.New("storage: invalid key")
	}
	return filepath.Join(local.directory, key), nil
}

func (local *Local) Put(key string, content io.Reader) (int64, error) {
	filePath, err := local.pathFor(key)
	if err != nil {
		return 0, err
	}
	file, err := os.Create(filePath)
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(file, content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(filePath)
		return 0, err
	}
	return written, nil
}

func (local *Local) Open(key string) (io.ReadCloser, int64, error) {
	filePath, err := local.pathFor(key)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

func (local *Local) Delete(key string) error {
	filePath, err := local.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
