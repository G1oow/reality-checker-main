package data

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNeedsUpdate(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.mmdb")

	// 创建一个新文件
	if err := os.WriteFile(filePath, []byte("dummy content"), 0644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	d := NewDownloader()

	// 刚创建的文件不需要更新
	needsUpdate, err := d.needsUpdate(filePath)
	if err != nil {
		t.Fatalf("needsUpdate 失败: %v", err)
	}
	if needsUpdate {
		t.Errorf("刚创建的文件不应该需要更新")
	}

	// 将修改时间改为 16 天前
	sixteenDaysAgo := time.Now().Add(-16 * 24 * time.Hour)
	if err := os.Chtimes(filePath, sixteenDaysAgo, sixteenDaysAgo); err != nil {
		t.Fatalf("修改文件时间戳失败: %v", err)
	}

	needsUpdate, err = d.needsUpdate(filePath)
	if err != nil {
		t.Fatalf("needsUpdate 失败: %v", err)
	}
	if !needsUpdate {
		t.Errorf("超过 15 天的文件应该需要更新")
	}
}

func TestEnsureFileFallbackToEmbedded(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "fallback", "Country.mmdb")

	d := NewDownloader()
	d.retries = 1
	d.retryDelay = 0
	d.timeout = 500 * time.Millisecond

	// 使用必定失败的 URL
	file := DataFile{
		Name:      "Country.mmdb",
		URL:       "http://127.0.0.1:0/unreachable/Country.mmdb",
		LocalPath: targetPath,
	}

	err := d.ensureFile(file)
	if err != nil {
		t.Fatalf("ensureFile 期望回退内置版本成功，但返回错误: %v", err)
	}

	fi, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("回退写入的本地文件不存在: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("回退写入的文件大小为 0")
	}

	// 验证生成的文件能被 OpenGeoIPReader 打开并正常解析
	reader, err := OpenGeoIPReader(targetPath)
	if err != nil {
		t.Fatalf("无法加载回退释放的 GeoIP 数据库: %v", err)
	}
	defer reader.Close()
}

func TestEnsureFileFallbackToEmbeddedAsset(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "rules", "cdn_keywords.txt")

	d := NewDownloader()
	d.retries = 1
	d.retryDelay = 0
	d.timeout = 500 * time.Millisecond

	file := DataFile{
		Name:          "cdn_keywords.txt",
		URL:           "http://127.0.0.1:0/unreachable/cdn_keywords.txt",
		LocalPath:     targetPath,
		EmbeddedAsset: "cdn_keywords.txt",
	}

	if err := d.ensureFile(file); err != nil {
		t.Fatalf("ensureFile 期望回退内置资源成功，但返回错误: %v", err)
	}

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("回退写入的本地文件不存在: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("回退写入的本地文件内容为空")
	}

	want, err := assetsFS.ReadFile("assets/cdn_keywords.txt")
	if err != nil {
		t.Fatalf("读取内置 cdn_keywords.txt 失败: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("回退写入内容与内置资源不一致: got %d 字节, want %d 字节", len(got), len(want))
	}

	if _, err := os.Stat(targetPath + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("回退写入后残留临时文件 %s.tmp", targetPath)
	}
}

func TestOpenRuleReaderFallbackToEmbeddedGFWList(t *testing.T) {
	reader, err := OpenRuleReader(filepath.Join(t.TempDir(), "missing", "gfwlist.conf"), "gfwlist.conf")
	if err != nil {
		t.Fatalf("OpenRuleReader 回退内置 gfwlist.conf 失败: %v", err)
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取内置 gfwlist.conf 失败: %v", err)
	}
	if len(content) == 0 {
		t.Fatalf("内置 gfwlist.conf 内容为空")
	}
	if !strings.Contains(string(content), "payload:") {
		t.Errorf("内置 gfwlist.conf 缺少 payload 段")
	}
}
