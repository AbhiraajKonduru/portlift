package main

import (
	"bytes"
	"context"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    options
		wantErr bool
	}{
		{"nothing", nil, options{}, false},
		{"port", []string{"3000"}, options{target: "3000"}, false},
		{"flag after port", []string{"3000", "--qr"}, options{target: "3000", qr: true}, false},
		{"flag before port", []string{"-q", "3000"}, options{target: "3000", quiet: true}, false},
		{"long quiet", []string{"--quiet"}, options{quiet: true}, false},
		{"removed flag", []string{"--no-qr"}, options{}, true},
		{"two targets", []string{"3000", "4000"}, options{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseArgs error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseArgs = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseTargetRejectsBadInput(t *testing.T) {
	for _, target := range []string{"0", "70000", "localhost", "ftp://localhost:21", "http://"} {
		if got, err := parseTarget(context.Background(), target); err == nil {
			t.Errorf("parseTarget(%q) = %q, want an error", target, got)
		}
	}
}

func TestParseTargetKeepsURL(t *testing.T) {
	const target = "http://localhost:8080"
	if got, err := parseTarget(context.Background(), target); err != nil || got != target {
		t.Errorf("parseTarget(%q) = %q, %v", target, got, err)
	}
}

func TestCRLFWriter(t *testing.T) {
	var buf bytes.Buffer
	n, err := crlfWriter{&buf}.Write([]byte("a\nb\n"))
	if err != nil || n != 4 {
		t.Errorf("Write = %d, %v; want 4, nil", n, err)
	}
	if got := buf.String(); got != "a\r\nb\r\n" {
		t.Errorf("wrote %q", got)
	}
}
