# sphvideo

视频号下载工具，MyGo 原生界面。支持 Windows、macOS、Linux。

## 功能

- 视频：MP4，可选原始画质或界面列出的画质。
- 音频：MP3，需要系统已安装 `ffmpeg`。
- 封面：JPG。
- 图片合集：ZIP；有背景音乐时一并保存。

## 使用

1. 运行应用，点「启动」。
2. 打开微信 → 视频号 → 任意内容。
3. 在右下角面板选择画质和下载项。默认保存到 `下载/视频号`。

macOS 会请求确认证书信任。Linux 需要 GTK 3、`certutil`（libnss3-tools）、
已有的 Chromium/Electron NSS 证书库和 GNOME/KDE 代理设置工具。macOS 构建
未签名；若 Gatekeeper 拒绝，执行 `xattr -dr com.apple.quarantine sphvideo.app`。

## 原理

本地 TLS 代理只拦截 `channels.weixin.qq.com` 和 `res.wx.qq.com`，向页面
注入脚本，从视频号前端取得 `url`、`urlToken`、`decodeKey`、画质列表、
封面和图片。媒体文件前 128 KiB 使用 ISAAC64 密钥流加密；Go 端边下载边
解密。MP3 由 `ffmpeg` 从解密后的数据流转换，不写临时文件。

## 清理

- 除下载文件外，不写配置、日志、缓存或临时文件。
- 根证书：Windows 当前用户证书区；macOS 登录钥匙串；Linux `~/.pki/nssdb`。
  退出时删除；异常退出由 watchdog 删除。
- 系统代理原值只保存在内存，退出时恢复；异常退出由 watchdog 恢复。
- Windows 不需要管理员权限；macOS 证书确认由系统弹出；Linux 不需要 root。

## 目录

- `cmd/sphvideo`：入口
- `internal/app`：界面与状态
- `internal/proxy`：代理、证书、系统代理
- `internal/download`：下载、解密与转换
- `internal/inject`：页面与 JS 注入
- `locales`、`resources`：安装器语言与应用图标

## 构建

```powershell
just build   # dist/sphvideo.exe
just dist    # Windows、Linux、macOS 产物
```

## 许可与免责声明

- 本项目代码 MIT，见 [LICENSE](LICENSE)；第三方许可见
  [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
- 参考实现 [wx_channels_download](https://github.com/ltaoo/wx_channels_download)
  为 MIT + Commons Clause，禁止销售；本项目免费、非商业。
- 与腾讯、微信、视频号及上游作者无关联，未获授权或背书。
- 仅用于个人学习、研究和备份有权访问的内容；不得侵权、传播、销售或商用。
- 下载内容版权归权利人；遵守微信/视频号条款与当地法律。
- 不收集、上传微信账号、Cookie 或视频内容；不绕过付费或权限控制。
- 按现状提供，无担保；使用风险自负，作者不承担责任。
