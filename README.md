# sphvideo

视频号下载工具，MyGo 原生界面。支持 Windows、macOS、Linux。

非官方工具，与腾讯、微信及视频号无关联。免费、开源，仅用于个人学习、研究和备份有权访问的内容。

## 功能

- 视频：在视频号页面点下载按钮，选择原始视频或页面列出的画质。
- 封面、图片合集：使用页面提供的下载菜单。
- 下载任务在视频号页面内显示进度；主窗口显示当前任务和最近保存的文件。

## 使用

1. 运行应用，在系统提示中选择「是」授予管理员权限，然后点「启动」。
2. 完全退出微信后重新打开，进入视频号任意内容。
3. 详情页的操作栏会出现下载按钮；首页右侧也有下载入口。选择画质即可。
4. 文件默认保存到 `下载/视频号`；可在主窗口更改，仅本次运行有效。
5. 关闭窗口会停止服务、恢复网络并退出；没有托盘图标。

如果停止或退出后无法上网，在主窗口的「说明」里点「修复网络」。

macOS 会请求确认证书信任。Linux 需要 GTK 3、`certutil`（libnss3-tools）、
已有的 Chromium/Electron NSS 证书库和 GNOME/KDE 代理设置工具。macOS 构建
未签名；若 Gatekeeper 拒绝，执行 `xattr -dr com.apple.quarantine sphvideo.app`。

## 原理

本地拦截代理只处理 `channels.weixin.qq.com` 和 `res.wx.qq.com`。页面脚本
由代理注入；页面内的下载面板通过本机回环接口创建下载任务，Go 端下载后
按视频号规则解密。核心拦截、注入、页面运行时和下载引擎来自
[wx_channels_download](https://github.com/ltaoo/wx_channels_download)，
代码原样放在 `third_party/wx_channel`，只保留视频号相关部分。

## 清理

- 不写配置、日志、缓存或临时文件；下载任务是内存状态，退出即丢弃。
- 根证书：Windows 本机证书区（`LocalMachine\Root`），安装和删除都不询问；
  macOS 登录钥匙串；Linux `~/.pki/nssdb`。退出时删除，异常退出由 watchdog
  删除。也可在主窗口点「修复网络」手动恢复。
- 系统代理原值只保存在内存，退出时恢复；异常退出由 watchdog 恢复。
- Windows 需要管理员权限，启动时由系统提示；macOS 证书确认由系统弹出；
  Linux 不需要 root。

## 说明

- 仅用于个人学习、研究和备份有权访问的内容；不得侵权、传播、销售或商用。
- 下载内容版权归权利人；遵守微信/视频号条款与当地法律。
- 不收集、上传微信账号、Cookie 或视频内容；不绕过付费或权限控制。
- 不读取聊天记录，不修改微信客户端。
- 按现状提供，无担保；使用风险自负，作者不承担责任。
- 包含 [wx_channels_download](https://github.com/ltaoo/wx_channels_download)
  代码，其许可为 MIT + Commons Clause，禁止销售；本项目免费、非商业。

## 目录

- `cmd/sphvideo`：入口
- `internal/app`：原生界面与状态
- `internal/proxy`：系统代理、临时证书与清理 watchdog
- `third_party/wx_channel`：上游视频号实现（拦截、注入、下载）
- `locales`、`resources`：安装器语言与应用图标

## 构建

```powershell
just build   # dist/sphvideo.exe
just dist    # Windows、Linux、macOS 产物
```

构建需要标签 `embed_inject,embed_frontend_inject,sqlite_only`；`just` 已通过
`GOFLAGS` 设置。直接运行 `go build` 时请一并传入：

```powershell
$env:GOFLAGS = "-tags=embed_inject,embed_frontend_inject,sqlite_only"
go build ./cmd/sphvideo
```

## 许可

本项目代码 MIT，见 [LICENSE](LICENSE)；第三方许可见
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
