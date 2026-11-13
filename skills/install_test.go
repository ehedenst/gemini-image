// Package skills holds the agent skill in gemini-image/; this file tests its
// install script.
package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestInstallScript runs scripts/install.sh against a fake GitHub releases
// server whose v1.2.3 is fine and whose v1.2.4 has a wrong checksum.
func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs sh and curl")
	}
	var downloads atomic.Int32
	files := map[string][]byte{}
	for _, version := range []string{"v1.2.3", "v1.2.4"} {
		name := fmt.Sprintf("gemini-image_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
		archive := tarGz(t, strings.TrimSuffix(name, ".tar.gz")+"/gemini-image",
			"#!/bin/sh\necho 'gemini-image version "+version+"'\n")
		sum := sha256.Sum256(archive)
		if version == "v1.2.4" {
			sum[0]++
		}
		files[version+"/"+name] = archive
		files[version+"/checksums.txt"] = fmt.Appendf(nil, "%x  %s\n", sum, name)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/v1.2.3", http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/{version}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/releases/download/{version}/{file}", func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.PathValue("version")+"/"+r.PathValue("file")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.PathValue("file"), ".tar.gz") {
			downloads.Add(1)
		}
		_, _ = w.Write(data)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("GEMINI_IMAGE_REPO_URL", srv.URL)

	install := func(args ...string) (stdout, stderr string, code int) {
		var out, errOut bytes.Buffer
		cmd := exec.Command("sh", append([]string{"gemini-image/scripts/install.sh"}, args...)...)
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), errOut.String(), code
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "gemini-image")
	for run := 1; run <= 2; run++ { // the second run finds it installed
		stdout, stderr, code := install("--dir", dir)
		if code != 0 || stdout != bin+"\n" || downloads.Load() != 1 {
			t.Fatalf("run %d: exit %d, stdout %q, %d downloads; want 0, %q, 1\nstderr: %s",
				run, code, stdout, downloads.Load(), bin, stderr)
		}
	}
	if out, err := exec.Command(bin, "--version").Output(); err != nil || string(out) != "gemini-image version v1.2.3\n" {
		t.Errorf("installed binary: %q, %v", out, err)
	}

	bad := t.TempDir()
	if stdout, stderr, code := install("--version", "1.2.4", "--dir", bad); code != 1 || stdout != "" ||
		!strings.Contains(stderr, "checksum mismatch") {
		t.Errorf("wrong checksum: exit %d, stdout %q, stderr %q; want exit 1 and a checksum error", code, stdout, stderr)
	}
	if entries, _ := os.ReadDir(bad); len(entries) != 0 {
		t.Errorf("wrong checksum installed %v", entries)
	}

	if _, stderr, code := install("--version", "1.2", "--dir", bad); code != 2 {
		t.Errorf("--version 1.2: exit %d, want 2; stderr %q", code, stderr)
	}
}

// tarGz returns a .tar.gz with one executable file.
func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
