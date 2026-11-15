package data

import (
	"RealityChecker/internal/logger"
	"RealityChecker/internal/network"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DataFile 数据文件配置
type DataFile struct {
	Name         string
	URL          string
	FallbackURLs []string
	LocalPath    string
}

// Downloader 数据文件下载器
type Downloader struct {
	proxyURL   string
	timeout    time.Duration
	retries    int
	retryDelay time.Duration
}

// NewDownloader 创建下载器（可指定代理地址）
func NewDownloader(proxyURL ...string) *Downloader {
	p := ""
	if len(proxyURL) > 0 {
		p = strings.TrimSpace(proxyURL[0])
	}
	return &Downloader{
		proxyURL:   p,
		timeout:    30 * time.Second,
		retries:    3,
		retryDelay: 2 * time.Second,
	}
}

// EnsureDataFiles 确保所有数据文件存在且最新
func (d *Downloader) EnsureDataFiles() error {
	logger.Step("数据文件检查")
	if d.proxyURL != "" {
		logger.Info("数据文件下载：经代理 %s", d.proxyURL)
	}

	// 定义需要下载的数据文件（静态规则 cdn_keywords/hot_websites/exclude_rules 已通过 go:embed 内置，无需远程下载）
	files := []DataFile{
		{
			Name: "Country.mmdb",
			URL:  "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/Country.mmdb",
			FallbackURLs: []string{
				"https://github.com/Loyalsoldier/geoip/releases/latest/download/Country.mmdb",
			},
			LocalPath: "data/Country.mmdb",
		},
	}

	// 确保data目录存在
	if err := os.MkdirAll("data", 0755); err != nil {
		return fmt.Errorf("创建data目录失败: %v", err)
	}

	// 检查并下载每个文件
	for _, file := range files {
		if err := d.ensureFile(file); err != nil {
			logger.Warn("数据文件 %s 不可用：%v，将继续运行并在扫描时降级处理", file.Name, err)
		}
	}

	logger.Success("数据文件检查完成")
	return nil
}

// ensureFile 确保单个文件存在且最新
func (d *Downloader) ensureFile(file DataFile) error {
	// 确保父目录存在
	if dir := filepath.Dir(file.LocalPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	// 检查文件是否存在
	exists, err := d.fileExists(file.LocalPath)
	if err != nil {
		return fmt.Errorf("检查文件 %s 失败: %v", file.Name, err)
	}

	// 如果文件不存在，直接下载；下载失败则尝试回退内置版本
	if !exists {
		logger.Step("下载数据文件：%s", file.Name)
		if err := d.downloadWithRetry(file); err != nil {
			if file.Name == "Country.mmdb" {
				if embData, embErr := GetEmbeddedCountryMMDB(); embErr == nil && len(embData) > 0 {
					logger.Warn("内置版本回退：%s 下载失败，已释放内置版本至 %s", file.Name, file.LocalPath)
					writeErr := os.WriteFile(file.LocalPath, embData, 0644)
					if writeErr == nil {
						return nil
					}
					logger.Warn("内置版本写入失败：%s → %s（%v），将继续在内存中使用内置版本", file.Name, file.LocalPath, writeErr)
					return nil
				}
			}
			return err
		}
		return nil
	}

	// 检查文件是否需要更新（超过15天）
	needsUpdate, err := d.needsUpdate(file.LocalPath)
	if err != nil {
		return fmt.Errorf("检查文件 %s 更新时间失败: %v", file.Name, err)
	}

	// 如果需要更新，下载新文件
	if needsUpdate {
		logger.Step("更新数据文件：%s", file.Name)
		if err := d.downloadWithRetry(file); err != nil {
			logger.Warn("更新失败：%s（%v），继续沿用现有本地版本", file.Name, err)
			return nil
		}
	}

	return nil
}

// fileExists 检查文件是否存在且非空
func (d *Downloader) fileExists(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err == nil {
		if fi.Size() == 0 {
			return false, nil
		}
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// needsUpdate 检查文件是否需要更新（超过15天）
func (d *Downloader) needsUpdate(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	// 检查文件修改时间是否超过15天（GeoIP 数据库建议半月或月度更新，降低频繁下载开销）
	fifteenDaysAgo := time.Now().Add(-15 * 24 * time.Hour)
	return info.ModTime().Before(fifteenDaysAgo), nil
}

// downloadWithRetry 带重试与备用源的下载
func (d *Downloader) downloadWithRetry(file DataFile) error {
	urls := append([]string{file.URL}, file.FallbackURLs...)

	for i := 0; i < d.retries; i++ {
		if i > 0 {
			logger.Info("下载重试：%d/%d", i, d.retries)
			time.Sleep(d.retryDelay)
		}

		for _, targetURL := range urls {
			err := d.downloadFileFromURL(file, targetURL)
			if err == nil {
				return nil // 成功
			}
			logger.Warn("下载失败：%s（源 %s，%v）", file.Name, targetURL, err)
		}
	}

	// 如果有内置版本可回退，则不在此打印手动下载说明（由调用方回退内置版本并提示）
	if file.Name == "Country.mmdb" && HasEmbeddedCountryMMDB() {
		return fmt.Errorf("下载 %s 失败，已重试 %d 次", file.Name, d.retries)
	}

	// 所有重试都失败了且无内置回退，显示手动下载说明
	d.showManualDownloadInstructions()
	return fmt.Errorf("下载 %s 失败，已重试 %d 次", file.Name, d.retries)
}

// downloadFileFromURL 从指定 URL 下载单个文件
func (d *Downloader) downloadFileFromURL(file DataFile, downloadURL string) error {
	logger.Info("下载数据文件：%s，来源 %s", file.Name, downloadURL)
	tr, err := network.MakeHTTPTransport(d.proxyURL, d.timeout)
	if err != nil {
		return fmt.Errorf("配置下载传输失败: %w", err)
	}

	// 创建HTTP客户端
	client := &http.Client{
		Transport: tr,
		Timeout:   d.timeout,
	}

	// 构造 Request 并注入常见 User-Agent
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 创建临时文件
	tmpFile := file.LocalPath + ".tmp"
	out, err := os.Create(tmpFile)
	if err != nil {
		return err
	}
	defer out.Close()

	// 复制数据
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		os.Remove(tmpFile) // 清理临时文件
		return err
	}

	// 关闭文件
	out.Close()

	// 原子性替换原文件
	if err := os.Rename(tmpFile, file.LocalPath); err != nil {
		os.Remove(tmpFile) // 清理临时文件
		return err
	}

	return nil
}

// showManualDownloadInstructions 显示手动下载说明
func (d *Downloader) showManualDownloadInstructions() {
	logger.Warn("GeoIP 数据库不可用：归属地查询将使用降级模式")
	logger.Detail("CDN 特征库、热门大站与排除规则已内置，无需额外下载")
	logger.Blank()
	logger.Plain("补齐 GeoIP 数据库：可手动下载 Country.mmdb 到 data/ 目录")
	logger.Plain("  https://github.com/Loyalsoldier/geoip/releases/latest/download/Country.mmdb")
	logger.Blank()
	logger.Plain("补齐后下次运行时将自动加载")
}
