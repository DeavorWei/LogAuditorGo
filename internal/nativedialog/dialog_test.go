package nativedialog

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeExts(t *testing.T) {
	got := NormalizeExts([]string{" .LOG ", "txt", "log", "", "TXT", "syslog"})
	want := []string{"log", "txt", "syslog"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeExts = %v, want %v", got, want)
	}
	if got := NormalizeExts(nil); len(got) != 0 {
		t.Fatalf("expected empty slice for nil input, got %v", got)
	}
}

type fakeOpener struct {
	files  []string
	folder string
	err    error
}

func (f fakeOpener) Supported() bool { return true }
func (f fakeOpener) OpenFiles(Options) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.files, nil
}
func (f fakeOpener) OpenFolder(Options) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.folder, nil
}

func TestSetForTestOverridesAndRestores(t *testing.T) {
	original := Default()
	fake := fakeOpener{files: []string{"D:/logs/a.log"}, folder: "D:/logs"}
	restore := SetForTest(fake)
	defer restore()

	if got, err := Default().OpenFiles(Options{Multi: true}); err != nil || len(got) != 1 || got[0] != "D:/logs/a.log" {
		t.Fatalf("fake OpenFiles = %v, %v", got, err)
	}
	if _, err := Default().OpenFolder(Options{}); err != nil {
		t.Fatalf("fake OpenFolder failed: %v", err)
	}

	restore()
	if reflect.TypeOf(Default()) != reflect.TypeOf(original) {
		t.Fatalf("Default() was not restored")
	}
}

func TestFakeOpenerErrorPropagation(t *testing.T) {
	sentinel := errors.New("boom")
	restore := SetForTest(fakeOpener{err: sentinel})
	defer restore()

	if _, err := Default().OpenFiles(Options{}); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
