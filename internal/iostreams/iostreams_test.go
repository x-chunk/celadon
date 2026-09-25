package iostreams

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadLineReadsSuccessiveLines(t *testing.T) {
	s := Test(strings.NewReader("one\r\ntwo\nthree"), &bytes.Buffer{}, &bytes.Buffer{})
	for _, want := range []string{"one", "two", "three"} {
		got, err := s.ReadLine()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("ReadLine = %q, want %q", got, want)
		}
	}
	if _, err := s.ReadLine(); !errors.Is(err, io.EOF) {
		t.Errorf("ReadLine past the end = %v, want EOF", err)
	}
}

func TestReadAllAfterReadLine(t *testing.T) {
	s := Test(strings.NewReader("pass\nthe rest\nof it"), &bytes.Buffer{}, &bytes.Buffer{})
	if _, err := s.ReadLine(); err != nil {
		t.Fatal(err)
	}
	b, err := s.ReadAll(1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "the rest\nof it" {
		t.Errorf("ReadAll = %q", b)
	}
}

func TestReadAllIsCapped(t *testing.T) {
	s := Test(strings.NewReader(strings.Repeat("x", 11)), &bytes.Buffer{}, &bytes.Buffer{})
	if _, err := s.ReadAll(10); err == nil {
		t.Error("input past the cap was accepted")
	}
}

func TestSecretNeedsATerminal(t *testing.T) {
	s := Test(nil, &bytes.Buffer{}, &bytes.Buffer{})
	if _, err := s.Secret("Key: "); !errors.Is(err, ErrNoTTY) {
		t.Errorf("Secret without a terminal = %v", err)
	}
	if _, err := s.Confirm("Sure?"); !errors.Is(err, ErrNoTTY) {
		t.Errorf("Confirm without a terminal = %v", err)
	}
}

func TestSecretOnATerminal(t *testing.T) {
	var errOut bytes.Buffer
	s := Test(nil, &bytes.Buffer{}, &errOut)
	s.InTTY, s.ErrTTY = true, true
	s.SetSecretReader(func() (string, error) { return "hunter2\r\n", nil })
	got, err := s.Secret("Passphrase: ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2" {
		t.Errorf("Secret = %q", got)
	}
	if !strings.Contains(errOut.String(), "Passphrase: ") || strings.Contains(errOut.String(), "hunter2") {
		t.Errorf("the prompt was %q", errOut.String())
	}
}

func TestConfirm(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "sure\n": false} {
		s := Test(strings.NewReader(in), &bytes.Buffer{}, &bytes.Buffer{})
		s.InTTY, s.ErrTTY = true, true
		got, err := s.Confirm("Delete?")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("Confirm(%q) = %v", in, got)
		}
	}
}
