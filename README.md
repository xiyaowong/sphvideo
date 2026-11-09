# sphvideo

视频号视频下载工具，MyGo 原生界面。启动后，微信视频号页面右下角出现
「下载视频」按钮，文件保存为 mp4。

## 使用

1. 运行 `sphvideo.exe`，点「启动」。
2. 打开微信 → 视频号 → 任意普通视频。
3. 点右下角「下载视频」。默认保存到 `下载\视频号`。

## 原理

本地 TLS 代理只拦截 `channels.weixin.qq.com` 和 `res.wx.qq.com`，向视频号
页面注入按钮脚本，从视频号前端取得 `url + urlToken` 和 `decodeKey`。媒体
文件前 128 KiB 使用 ISAAC64 密钥流加密，Go 端下载后解密保存。

## 清理

- 除下载文件外，不写配置、日志、缓存、数据库或临时文件。
- 一次性根证书只装入当前用户证书区，退出时由 Crypt32 静默删除。
- 系统代理原值只保存在内存，退出时恢复；异常退出由 watchdog 恢复。
- 不安装服务或驱动，不需要管理员权限。

## 目录

- `cmd/sphvideo`：入口
- `internal/app`：界面与状态
- `internal/proxy`：代理、证书、系统代理
- `internal/download`：下载与解密
- `internal/inject`：页面与 JS 注入
- `locales`、`resources`：安装器语言与应用图标

## 构建

```powershell
just build   # dist/sphvideo.exe
just dist    # 二进制 + 中文安装包
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
