package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dalibre/internal/plugin"
)

func TestAssetPlatform(t *testing.T) {
	for name, want := range map[string]string{
		"pandoc-3.6.4-arm64-macOS.zip":           "darwin-arm64",
		"pandoc-3.6.4-x86_64-macOS.zip":          "darwin-amd64",
		"pandoc-3.6.4-linux-amd64.tar.gz":        "linux-amd64",
		"pandoc-3.6.4-windows-x86_64.zip":        "windows-amd64",
		"zlib_0.0.12_darwin_arm64.tar.gz":        "darwin-arm64",
		"zlib_0.0.12_windows_x86_64.zip":         "windows-amd64",
		"tool-aarch64-unknown-linux-gnu.tar.gz":  "linux-arm64",
		"tool-x86_64-pc-windows-msvc.zip":        "windows-amd64",
		"pandoc-3.6.4-windows-x86_64.msi":        "",
		"pandoc-3.6.4-1-amd64.deb":               "",
		"checksums.txt":                          "",
		"zlib_0.0.12_darwin_arm64.tar.gz.sha256": "",
	} {
		if got, _ := assetPlatform(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	_, musl := assetPlatform("tool-x86_64-unknown-linux-musl.tar.gz")
	_, gnu := assetPlatform("tool-x86_64-unknown-linux-gnu.tar.gz")
	if musl >= gnu {
		t.Error("musl should rank below glibc")
	}
}

func TestParseFormula(t *testing.T) {
	src := `class Zlib < Formula
  desc "A CLI for Z-Library"
  homepage "https://github.com/heartleo/zlib"
  version "0.0.12"
  license "MIT"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/heartleo/zlib/releases/download/v0.0.12/zlib_0.0.12_darwin_x86_64.tar.gz"
      sha256 "70faa00bafc805a0f9291c7c516930e541921fe880de41235b7ad0af721c564b"
    end
    if Hardware::CPU.arm?
      url "https://github.com/heartleo/zlib/releases/download/v0.0.12/zlib_0.0.12_darwin_arm64.tar.gz"
      sha256 "1111111111111111111111111111111111111111111111111111111111111111"
    end
  end

  on_linux do
    if Hardware::CPU.intel? && Hardware::CPU.is_64_bit?
      url "https://github.com/heartleo/zlib/releases/download/v0.0.12/zlib_0.0.12_linux_x86_64.tar.gz"
      sha256 "2222222222222222222222222222222222222222222222222222222222222222"
    end
  end
end
`
	tool, err := parseFormula([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if tool.Version != "0.0.12" || tool.License != "MIT" || len(tool.Platforms) != 3 {
		t.Fatalf("%+v", tool)
	}
	if b := tool.Platforms["darwin-arm64"]; !strings.HasSuffix(b.URL, "darwin_arm64.tar.gz") || b.SHA256[0] != '1' {
		t.Errorf("darwin-arm64: %+v", b)
	}
	if b := tool.Platforms["linux-amd64"]; b.SHA256[0] != '2' {
		t.Errorf("linux-amd64: %+v", b)
	}
}

func TestSetManifestField(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plugin.json")
	os.WriteFile(file, []byte(`{"id":"x","name":"X","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["e.com"]}}`), 0o644)
	tools := map[string]plugin.Tool{"x": {Version: "1.0", Platforms: map[string]plugin.Build{"darwin-arm64": {URL: "https://e.com/x", SHA256: strings.Repeat("0", 64)}}}}
	if err := setManifestField(file, "tools", tools); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	s := string(data)
	if !strings.HasPrefix(s, "{\n  \"id\": \"x\",") || !strings.Contains(s, `"tools": {`) || strings.Index(s, `"permissions"`) > strings.Index(s, `"tools"`) {
		t.Errorf("order or shape:\n%s", s)
	}
	if _, err := plugin.ParseManifest(data); err != nil {
		t.Error(err)
	}
}
