package panel

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeLinuxBinary 构造带 ELF 头的最小二进制内容（machine 为 ELF e_machine）
func fakeLinuxBinary(machine uint16) []byte {
	content := make([]byte, 64)
	copy(content, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(content[18:20], machine)
	return content
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		latest   string
		current  string
		expected bool
	}{
		{"1.9.0", "1.8.0", true},
		{"v1.9.0", "1.8.0", true},
		{"1.8.0", "1.8.0", false},
		{"v1.8.0", "1.8.0", false},
		{"1.8.1", "1.8.0", true},
		{"1.8.0", "1.8.1", false},
		{"2.0", "1.9.9", true},
		{"1.10.0", "1.9.0", true},
		{"1.9.0-rc1", "1.8.0", true},
		{"dev", "1.8.0", false},
		{"1.9.0", "unknown", true},
	}
	for _, testCase := range cases {
		if got := versionNewer(testCase.latest, testCase.current); got != testCase.expected {
			t.Fatalf("versionNewer(%q, %q) = %v，期望 %v", testCase.latest, testCase.current, got, testCase.expected)
		}
	}
}

func TestUpdaterCheckReportsLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"tag_name":"v1.9.0","body":"- 新增面板更新与卸载","assets":[{"name":"reality-checker-main","browser_download_url":"%s/dl"}]}`, serverURL(r))
	}))
	defer server.Close()

	updater := &Updater{
		Client:    server.Client(),
		APIURL:    server.URL + "/api",
		LatestURL: server.URL + "/latest",
		Current:   "1.8.0",
		GOARCH:    "amd64",
	}
	info, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("检查更新失败: %v", err)
	}
	if !info.Available {
		t.Fatalf("1.9.0 应判定为有更新：%+v", info)
	}
	if info.Latest != "1.9.0" || info.Current != "1.8.0" {
		t.Fatalf("版本号解析错误：%+v", info)
	}
	if info.URL != server.URL+"/dl" {
		t.Fatalf("下载地址应取 Release 资产，实际 %q", info.URL)
	}
	if info.Notes == "" {
		t.Fatalf("应保留 Release 说明：%+v", info)
	}
}

func TestUpdaterCheckReportsUpToDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.8.0"}`)
	}))
	defer server.Close()

	updater := &Updater{Client: server.Client(), APIURL: server.URL, LatestURL: server.URL + "/latest", Current: "1.8.0"}
	info, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("检查更新失败: %v", err)
	}
	if info.Available {
		t.Fatalf("同版本不应提示更新：%+v", info)
	}
}

func TestUpdaterFallsBackToRedirectWhenAPIFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			http.Error(w, "rate limited", http.StatusForbidden)
		case "/latest":
			w.Header().Set("Location", "https://github.com/G1oow/reality-checker-main/releases/tag/v2.0.0")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	updater := &Updater{Client: server.Client(), APIURL: server.URL + "/api", LatestURL: server.URL + "/latest", Current: "1.8.0"}
	info, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("API 失败时应回退到跳转地址: %v", err)
	}
	if info.Latest != "2.0.0" || !info.Available {
		t.Fatalf("回退结果不正确：%+v", info)
	}
}

func TestUpdaterApplyReplacesExecutable(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "reality-checker-main")
	if err := os.WriteFile(execPath, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("准备旧程序失败: %v", err)
	}
	newContent := fakeLinuxBinary(0x3e)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newContent)
	}))
	defer server.Close()

	updater := &Updater{Client: server.Client(), ExecPath: execPath, Current: "1.8.0", GOARCH: "amd64"}
	if err := updater.Apply(context.Background(), UpdateInfo{Latest: "1.9.0", URL: server.URL + "/dl"}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	content, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("读取替换后的程序失败: %v", err)
	}
	if string(content) != string(newContent) {
		t.Fatalf("程序内容未替换：%q", content)
	}
	if _, err := os.Stat(execPath + ".old"); !os.IsNotExist(err) {
		t.Fatalf("替换成功后不应保留备份文件")
	}
	// Windows 不保留 Unix 执行位，只在 Linux 目标上校验 0755
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(execPath); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("替换后的程序应可执行：%v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("临时文件应被清理，目录残留 %d 个文件", len(entries))
	}
}

func TestUpdaterApplyRejectsNonELFContent(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "reality-checker-main")
	original := []byte("old binary")
	if err := os.WriteFile(execPath, original, 0o755); err != nil {
		t.Fatalf("准备旧程序失败: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>404</html>")
	}))
	defer server.Close()

	updater := &Updater{Client: server.Client(), ExecPath: execPath, GOARCH: "amd64"}
	err := updater.Apply(context.Background(), UpdateInfo{Latest: "1.9.0", URL: server.URL + "/dl"})
	if err == nil {
		t.Fatalf("非 ELF 内容应被拒绝")
	}
	content, readErr := os.ReadFile(execPath)
	if readErr != nil || string(content) != string(original) {
		t.Fatalf("校验失败时不应改动原程序：%q（%v）", content, readErr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("校验失败后应清理临时文件，目录残留 %d 个文件", len(entries))
	}
}

func TestUpdaterApplyRejectsWrongArchitecture(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "reality-checker-main")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("准备旧程序失败: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fakeLinuxBinary(0xb7)) // arm64
	}))
	defer server.Close()

	updater := &Updater{Client: server.Client(), ExecPath: execPath, GOARCH: "amd64"}
	if err := updater.Apply(context.Background(), UpdateInfo{URL: server.URL + "/dl"}); err == nil {
		t.Fatalf("架构不一致应被拒绝")
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func TestUpdaterPrefersArchitectureAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.9.0","assets":[
			{"name":"reality-checker-linux-arm64","browser_download_url":"https://example.test/arm64"},
			{"name":"reality-checker-main","browser_download_url":"https://example.test/amd64"}]}`)
	}))
	defer server.Close()

	updater := &Updater{Client: server.Client(), APIURL: server.URL, Current: "1.8.0", GOARCH: "arm64"}
	info, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("检查更新失败: %v", err)
	}
	if info.URL != "https://example.test/arm64" {
		t.Fatalf("应优先选择本机架构资产，实际 %q", info.URL)
	}
}
