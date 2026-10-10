// Package cloudflared finds, installs and runs the cloudflared binary that
// carries the tunnel.
package cloudflared

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Version is the cloudflared release portlift downloads when none is
// installed. The checksums below are the ones published with that release.
const Version = "2026.10.0"

const InstallDocs = "https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"

type asset struct {
	name   string
	size   int64
	sha256 string
}

var assets = map[string]asset{
	"windows/amd64": {"cloudflared-windows-amd64.exe", 55365048, "86aee4017b26625cee8484c113558f48effa4cd47f7aa05fcf425604e5d2b23c"},
	"windows/386":   {"cloudflared-windows-386.exe", 37705864, "0630a8779e9823a1a3b091698b8e71874e0f7b205559219f52fdd301466b5546"},
	"linux/amd64":   {"cloudflared-linux-amd64", 40129756, "d33ff2d14475178d2012c2c56beba87389ac5ded27649519f198a7d3134a99db"},
	"linux/arm64":   {"cloudflared-linux-arm64", 37687584, "e6422b9d4f72d3194bc5a38676f13667c06666523217b842a877d72a80b5ac08"},
	"linux/arm":     {"cloudflared-linux-arm", 36574787, "1dbe8e4ec17e74bb7f49cf91db6a4903bd0f9fe41984556c7503e40b765fd099"},
	"linux/386":     {"cloudflared-linux-386", 37429396, "f6fbd789e6ce9c824d4d560cbbfad2753d55ce398ece16b4c9fbf17271dceab3"},
	"darwin/amd64":  {"cloudflared-darwin-amd64.tgz", 21741581, "903845b81828c8cb3c5d13d816a2de71c06a3da5785469df8eb0e1b736d92f9f"},
	"darwin/arm64":  {"cloudflared-darwin-arm64.tgz", 19809074, "a2f79ff7b9420aa537d74af239f376da170bbabeb529aec416002adac6a72e70"},
}

// ErrUnsupported means there is no cloudflared build to download for this
// platform.
var ErrUnsupported = errors.New("no cloudflared download for " + runtime.GOOS + "/" + runtime.GOARCH)

// Locate returns the cloudflared to run: the one on PATH if there is one,
// otherwise a copy portlift downloaded earlier.
func Locate() (string, bool) {
	if path, err := exec.LookPath("cloudflared"); err == nil {
		return path, true
	}
	if path, err := CachePath(); err == nil {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

// CachePath is where a downloaded cloudflared is kept.
func CachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := "cloudflared-" + Version
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, "portlift", name), nil
}

// DownloadSize is the size in bytes of the download for this platform.
func DownloadSize() (int64, error) {
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return 0, ErrUnsupported
	}
	return a.size, nil
}

// Download fetches the pinned cloudflared release from GitHub, checks it
// against the published checksum and installs it at CachePath. progress, if
// not nil, is called as bytes arrive.
func Download(ctx context.Context, progress func(done, total int64)) (string, error) {
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", ErrUnsupported
	}
	dest, err := CachePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}

	url := "https://github.com/cloudflare/cloudflared/releases/download/" + Version + "/" + a.name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), "download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	sum := sha256.New()
	body := io.Reader(resp.Body)
	if progress != nil {
		body = &progressReader{r: body, total: a.size, report: progress}
	}
	if _, err := io.Copy(io.MultiWriter(tmp, sum), body); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != a.sha256 {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", a.name, got, a.sha256)
	}

	binary := tmp
	if strings.HasSuffix(a.name, ".tgz") {
		if binary, err = extract(tmp, filepath.Dir(dest)); err != nil {
			return "", err
		}
		defer os.Remove(binary.Name())
		defer binary.Close()
	}
	if err := binary.Chmod(0o755); err != nil && runtime.GOOS != "windows" {
		return "", err
	}
	if err := binary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(binary.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}

// extract pulls the cloudflared binary out of a release tarball into a new
// temporary file in dir.
func extract(archive *os.File, dir string) (*os.File, error) {
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("cloudflared not found in the downloaded archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "cloudflared" {
			continue
		}
		out, err := os.CreateTemp(dir, "extract-*")
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(out.Name())
			return nil, err
		}
		return out, nil
	}
}

type progressReader struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	p.report(p.done, p.total)
	return n, err
}

// UserConfig returns the path of a cloudflared config file in the user's home
// folder, or "" if there is none. Cloudflare documents that quick tunnels are
// not supported while one exists.
func UserConfig() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"config.yml", "config.yaml"} {
		path := filepath.Join(home, ".cloudflared", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
