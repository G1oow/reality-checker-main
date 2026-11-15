# AGENTS.md

本文件约定在本仓库内做自动化/代理操作时的**发布规范**。
提交信息格式见 [`src/.agent/rules/guidline.md`](./src/.agent/rules/guidline.md)（Conventional Commits），此处不重复。

## 发布规范（强制）

1. **唯一发布入口是 Actions**：仓库页 → Actions → `Build and Release` → **Run workflow**。
   不允许再用「本地 `build.sh` + 手工上传 Releases」的方式发布正式版本。
2. **只能从 `main` 触发**：workflow 会硬性 checkout `main`，其它分支触发会直接报错退出。
   发布前先把改动合入 `main`。
3. **版本号只在 tag 里维护**：
   - `bump=auto` 由 Conventional Commits 推断：有 `!:` 或 `BREAKING CHANGE` → `major`，有 `feat:` → `minor`，其余 → `patch`；
   - 需要跳级时显式选 `bump`，或用 `version` 直接指定；
   - **不要手工修改** `src/internal/version/version.go` 的 `Version`：发布时会自动回写，并提交 `chore(release): vX.Y.Z [skip ci]`。
4. **不要手工创建或编辑 Release**：更新说明由 GitHub 按 PR/提交自动生成，分类规则在 [`.github/release.yml`](./.github/release.yml)。
   想让分类更清晰，就给 PR 打 label：`feat` / `fix` / `refactor` / `perf` / `docs`。
5. **不要改 Release 资产名**：`install.sh` 依赖 `releases/latest/download/reality-checker-main` 这个固定资产名，
   workflow 里的 `Prepare release asset aliases` 步骤负责产出别名，删除它会导致一键安装失败。
6. **tag 不可变**：tag 一旦推送就不再覆盖。发错了就追加新版本，禁止 force-push tag 或删除已有 Release 后重发同名版本。
7. **旧入口仍可用但不推荐**：`git push origin vX.Y.Z` 依然会构建并发布，
   但它不会回写 `version.go`，会造成版本号与 tag 漂移，只作为应急手段。

## 发布前检查清单

- [ ] 改动已合入 `main`，`go test ./...` 与 `go build ./...` 通过
- [ ] commit message 符合 Conventional Commits（直接决定 `bump=auto` 的结果）
- [ ] 没有破坏 `build.sh` 的构建顺序（先前端 `web/dist` 再编译 Go）与产物资产名
- [ ] 发布后验证安装：`curl -fsSL .../install.sh | bash` 能装到新版本，`./reality-checker-main version` 显示预期版本号

## 相关文件

| 文件 | 作用 |
|---|---|
| `.github/workflows/build.yml` | 一键发布：推算版本 → 回写 `version.go` → 构建 → 打 tag → 发 Release |
| `.github/release.yml` | Release 更新说明的自动分类规则 |
| `src/build.sh` | 本地/CI 共用的构建脚本（前端优先，注入 ldflags 版本信息） |
| `install.sh` | 从 `releases/latest/download` 取主程序的一键安装脚本 |
