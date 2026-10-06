package storage

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func newTestStore(test *testing.T) *Local {
	test.Helper()
	store, err := NewLocal(test.TempDir())
	if err != nil {
		test.Fatal(err)
	}
	return store
}

func TestPutThenOpenReturnsTheSameBytes(test *testing.T) {
	store := newTestStore(test)

	size, err := store.Put("scan.jpg", strings.NewReader("hello film"))
	if err != nil || size != int64(len("hello film")) {
		test.Fatalf("Put = %d, %v", size, err)
	}
	content, openedSize, err := store.Open("scan.jpg")
	if err != nil {
		test.Fatal(err)
	}
	defer content.Close()
	data, _ := io.ReadAll(content)
	if string(data) != "hello film" || openedSize != size {
		test.Fatalf("data=%q size=%d", data, openedSize)
	}
}

func TestOpenMissingFileReturnsErrNotFound(test *testing.T) {
	if _, _, err := newTestStore(test).Open("missing.jpg"); !errors.Is(err, ErrNotFound) {
		test.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesTheFileAndToleratesMissingOnes(test *testing.T) {
	store := newTestStore(test)
	if _, err := store.Put("a.jpg", strings.NewReader("x")); err != nil {
		test.Fatal(err)
	}
	if err := store.Delete("a.jpg"); err != nil {
		test.Fatal(err)
	}
	if _, _, err := store.Open("a.jpg"); !errors.Is(err, ErrNotFound) {
		test.Fatalf("file still present: %v", err)
	}
	if err := store.Delete("a.jpg"); err != nil {
		test.Fatalf("deleting a missing file must not fail: %v", err)
	}
}

func TestKeysCannotEscapeTheStorageDirectory(test *testing.T) {
	store := newTestStore(test)
	for _, key := range []string{"", ".", "..", "../outside", "nested/file", `back\slash`} {
		if _, err := store.Put(key, strings.NewReader("x")); err == nil {
			test.Errorf("Put(%q) should be rejected", key)
		}
		if _, _, err := store.Open(key); err == nil || errors.Is(err, ErrNotFound) {
			test.Errorf("Open(%q) should be rejected as invalid, got %v", key, err)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestPutRemovesPartialFilesWhenTheUploadFails(test *testing.T) {
	store := newTestStore(test)
	if _, err := store.Put("partial.jpg", failingReader{}); err == nil {
		test.Fatal("expected an error")
	}
	if _, _, err := store.Open("partial.jpg"); !errors.Is(err, ErrNotFound) {
		test.Fatalf("partial file was left behind: %v", err)
	}
}
