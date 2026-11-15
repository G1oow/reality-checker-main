package panel

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"RealityChecker/internal/version"
)

// 面板内的版本更新：查询 GitHub Releases 的最新 tag，下载对应主程序并原子替换
// 当前可执行文件。安装脚本 install.sh 与这里使用同一份 Release 产物。

const (
	// updateRepo 发布主程序的仓库
	updateRepo = "G1oow/reality-checker-main"
	// updateAppBase Release 资产前缀（实际资产名为 reality-checker-linux-<arch>）
	updateAppBase = "reality-checker"
	// updateAppName install.sh 下载的固定别名资产
	updateAppName = "reality-checker-main"
	// updateAPIURL 最新 release 的 API 地址（可用 RCM_UPDATE_API 覆盖）
	updateAPIURL = "https://api.github.com/repos/" + updateRepo + "/releases/latest"
	// updateLatestURL API 不可用时的回退入口：靠 302 的 Location 取 tag
	updateLatestURL = "https://github.com/" + updateRepo + "/releases/latest"
	// updateReleaseBase Release 资产下载基址（可用 RCM_UPDATE_BASE 覆盖，便于走镜像）
	updateReleaseBase = "https://github.com/" + updateRepo + "/releases/download"
)

// UpdateInfo 一次更新检查的结果
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
	Notes     string `json:"notes,omitempty"`
	Available bool   `json:"available"`
}

// Updater 检查并应用版本更新；字段可注入，便于测试固定下载源与目标路径
type Updater struct {
	Client      *http.Client
	APIURL      string
	LatestURL   string
	ReleaseBase string
	ExecPath    string
	Current     string
	GOARCH      string
}

// NewUpdater 按当前安装环境构造更新器
func NewUpdater() *Updater {
	updater := &Updater{
		Client:      &http.Client{Timeout: 5 * time.Minute},
		APIURL:      strings.TrimSpace(os.Getenv("RCM_UPDATE_API")),
		LatestURL:   updateLatestURL,
		ReleaseBase: strings.TrimRight(strings.TrimSpace(os.Getenv("RCM_UPDATE_BASE")), "/"),
		Current:     version.GetVersion(),
		GOARCH:      runtime.GOARCH,
	}
	if updater.APIURL == "" {
		updater.APIURL = updateAPIURL
	}
	if exe, err := os.Executable(); err == nil {
		updater.ExecPath = exe
	}
	return updater
}

// releaseMeta 解析出的 release 元数据
type releaseMeta struct {
	Tag      string
	Notes    string
	AssetURL string
}

// Check 查询最新版本并与当前版本比较
func (u *Updater) Check(ctx context.Context) (UpdateInfo, error) {
	info := UpdateInfo{Current: normalizeVersion(u.Current)}
	meta, err := u.fetchRelease(ctx)
	if err != nil {
		return info, err
	}
	info.Latest = normalizeVersion(meta.Tag)
	info.URL = u.downloadURL(meta)
	info.Notes = meta.Notes
	info.Available = versionNewer(info.Latest, info.Current)
	return info, nil
}

// Apply 下载最新版本并原子替换当前可执行文件；替换成功后旧文件立即移除
func (u *Updater) Apply(ctx context.Context, info UpdateInfo) error {
	if strings.TrimSpace(info.URL) == "" {
		return errors.New("缺少下载地址")
	}
	execPath := strings.TrimSpace(u.ExecPath)
	if execPath == "" {
		return errors.New("无法定位当前程序路径")
	}
	// 临时文件放在目标同目录，保证 rename 在同一文件系统内完成，替换是原子的
	dir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(dir, "."+filepath.Base(execPath)+".new-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := u.download(ctx, info.URL, tmpFile); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("设置执行权限失败: %w", err)
	}
	if err := validateLinuxBinary(tmpPath, u.GOARCH); err != nil {
		return err
	}
	if err := replaceExecutable(execPath, tmpPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// download 把 URL 内容写入目标文件
func (u *Updater) download(ctx context.Context, rawURL string, dst io.Writer) error {
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("下载地址无效: %w", err)
	}
	req.Header.Set("User-Agent", "reality-checker/"+version.GetVersion())
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	written, err := io.Copy(dst, resp.Body)
	if err != nil {
		return fmt.Errorf("下载中断: %w", err)
	}
	if written == 0 {
		return errors.New("下载内容为空")
	}
	return nil
}

// fetchRelease 取最新 release：优先 API，失败时回退到 releases/latest 的跳转地址
func (u *Updater) fetchRelease(ctx context.Context) (releaseMeta, error) {
	meta, apiErr := u.fetchReleaseAPI(ctx)
	if apiErr == nil {
		return meta, nil
	}
	meta, redirectErr := u.fetchReleaseByRedirect(ctx)
	if redirectErr == nil {
		return meta, nil
	}
	return releaseMeta{}, fmt.Errorf("查询最新版本失败：%s；%s", shortError(apiErr), shortError(redirectErr))
}

// fetchReleaseAPI 解析 GitHub Release API 的 JSON
func (u *Updater) fetchReleaseAPI(ctx context.Context) (releaseMeta, error) {
	apiURL := u.APIURL
	if apiURL == "" {
		apiURL = updateAPIURL
	}
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return releaseMeta{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "reality-checker/"+version.GetVersion())

	resp, err := client.Do(req)
	if err != nil {
		return releaseMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseMeta{}, fmt.Errorf("API 返回 HTTP %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return releaseMeta{}, fmt.Errorf("解析 release 失败: %w", err)
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return releaseMeta{}, errors.New("release 缺少版本号")
	}
	meta := releaseMeta{Tag: tag, Notes: strings.TrimSpace(payload.Body)}
	// 优先按架构取 reality-checker-linux-<arch>，回退 install.sh 用的固定别名
	for _, wanted := range []string{u.assetName(), updateAppName} {
		for _, asset := range payload.Assets {
			if asset.Name == wanted && strings.TrimSpace(asset.URL) != "" {
				meta.AssetURL = strings.TrimSpace(asset.URL)
				break
			}
		}
		if meta.AssetURL != "" {
			break
		}
	}
	return meta, nil
}

// fetchReleaseByRedirect 用 releases/latest 的 302 跳转取最新 tag（API 被限流时仍可用）
func (u *Updater) fetchReleaseByRedirect(ctx context.Context) (releaseMeta, error) {
	latestURL := u.LatestURL
	if latestURL == "" {
		latestURL = updateLatestURL
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return releaseMeta{}, err
	}
	req.Header.Set("User-Agent", "reality-checker/"+version.GetVersion())
	resp, err := client.Do(req)
	if err != nil {
		return releaseMeta{}, err
	}
	defer resp.Body.Close()
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location == "" {
		return releaseMeta{}, fmt.Errorf("跳转响应缺少 Location（HTTP %d）", resp.StatusCode)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return releaseMeta{}, err
	}
	tag := strings.TrimSpace(filepath.Base(parsed.Path))
	if tag == "" || tag == "." || tag == "/" || tag == "latest" {
		return releaseMeta{}, fmt.Errorf("无法从跳转地址解析版本号：%s", location)
	}
	return releaseMeta{Tag: tag}, nil
}

// assetName 返回当前 CPU 架构对应的 Release 资产名
func (u *Updater) assetName() string {
	arch := strings.TrimSpace(u.GOARCH)
	if arch == "" {
		arch = runtime.GOARCH
	}
	return updateAppBase + "-linux-" + arch
}

// downloadURL 返回主程序下载地址：显式基址 > Release 资产 > 默认 Release 路径
func (u *Updater) downloadURL(meta releaseMeta) string {
	if u.ReleaseBase != "" {
		return u.ReleaseBase + "/" + meta.Tag + "/" + u.assetName()
	}
	if meta.AssetURL != "" {
		return meta.AssetURL
	}
	return updateReleaseBase + "/" + meta.Tag + "/" + u.assetName()
}

// replaceExecutable 用新文件替换可执行文件：先备份，替换失败则回滚
func replaceExecutable(execPath, newPath string) error {
	backup := execPath + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(execPath, backup); err != nil {
		return fmt.Errorf("备份当前程序失败: %w", err)
	}
	if err := os.Rename(newPath, execPath); err != nil {
		if rollbackErr := os.Rename(backup, execPath); rollbackErr != nil {
			return fmt.Errorf("替换程序失败: %w（回滚也失败: %v，备份保留在 %s）", err, rollbackErr, backup)
		}
		return fmt.Errorf("替换程序失败: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

// validateLinuxBinary 校验下载内容是匹配本机架构的 Linux ELF 可执行文件
func validateLinuxBinary(path, goarch string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取下载文件失败: %w", err)
	}
	defer file.Close()

	header := make([]byte, 20)
	if _, err := io.ReadFull(file, header); err != nil {
		return errors.New("下载内容不是有效的 Linux 可执行文件（文件过短）")
	}
	if !bytes.Equal(header[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return errors.New("下载内容不是 Linux 可执行文件（ELF 头校验失败）")
	}
	// EI_DATA = 1 表示小端；只对 amd64 / arm64 做机器类型校验
	if header[5] != 1 {
		return nil
	}
	machine := binary.LittleEndian.Uint16(header[18:20])
	expected := map[string]uint16{"amd64": 0x3e, "arm64": 0xb7}[goarch]
	if expected != 0 && machine != expected {
		return fmt.Errorf("下载文件的架构（0x%02x）与本机 %s 不一致", machine, goarch)
	}
	return nil
}

// normalizeVersion 去掉 tag 的 v 前缀与首尾空白
func normalizeVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

// versionNewer 判断 latest 是否比 current 更新（无法解析当前版本时视为需要更新）
func versionNewer(latest, current string) bool {
	latestParts, ok := parseVersion(latest)
	if !ok {
		return false
	}
	currentParts, ok := parseVersion(current)
	if !ok {
		return true
	}
	for i := 0; i < len(latestParts) || i < len(currentParts); i++ {
		var latestPart, currentPart int
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if latestPart != currentPart {
			return latestPart > currentPart
		}
	}
	return false
}

// parseVersion 把 1.8.0 / v1.9.0-rc1 解析为数字段
func parseVersion(value string) ([]int, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(value)), "v")
	if trimmed == "" {
		return nil, false
	}
	segments := strings.Split(trimmed, ".")
	parts := make([]int, 0, len(segments))
	for _, segment := range segments {
		digits := 0
		for digits < len(segment) && segment[digits] >= '0' && segment[digits] <= '9' {
			digits++
		}
		if digits == 0 {
			return nil, false
		}
		number, err := strconv.Atoi(segment[:digits])
		if err != nil {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, true
}
