package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeAutoIndexKeepsOnlySafeFiles(t *testing.T) {
	input := `[{"name":"access.log","type":"file","mtime":"2026-07-10T01:00:00Z","size":12},{"name":"subdir","type":"directory","size":0},{"name":"../secret","type":"file","size":3}]`
	files, err := decodeAutoIndex(strings.NewReader(input), 10)
	if err != nil {
		t.Fatalf("decodeAutoIndex: %v", err)
	}
	if len(files) != 1 || files[0].Name != "access.log" || files[0].Size != 12 {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestDecodeAutoIndexLimitsEntries(t *testing.T) {
	_, err := decodeAutoIndex(strings.NewReader(`[{"name":"one.log","type":"file","size":1},{"name":"two.log","type":"file","size":1}]`), 1)
	if !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("expected ErrListTooLarge, got %v", err)
	}
}

func TestJoinAgentURLEscapesFileName(t *testing.T) {
	url, err := joinAgentURL("http://10.0.0.5:8080", "api", "app log.log")
	if err != nil {
		t.Fatalf("joinAgentURL: %v", err)
	}
	if url != "http://10.0.0.5:8080/logs/api/app%20log.log" {
		t.Fatalf("unexpected URL %q", url)
	}
}
