# sphvideo

视频号视频下载工具，MyGo 原生界面。启动后，微信视频号页面右下角出现
「下载视频」按钮，文件保存为 mp4。

## 使用

1. 运行 `sphvideo.exe`，点「启动」。
2. 打开微信 → 视频号 → 任意普通视频。
3. 点右下角「下载视频」。默认保存到 `下载\视频号`。

## 原理

本地 TLS 代理只拦截 `channels.weixin.qq.com` 和 `res.wx.qq.com`，向视频号
页面注入按钮脚本，并从视频号前端拿到 `url + urlToken` 和 `decodeKey`。
视频号使用 ISAAC64 密钥流加密前 128 KiB，Go 端下载后解密保存。

## 清理

- 不写配置、日志、缓存或数据库，不创建临时文件。
- 一次性根证书只装入当前用户证书区，退出时由 Crypt32 静默删除。
- 系统代理原值只保存在内存，退出时恢复；异常退出由 watchdog 恢复。
- 不安装服务、驱动，不需要管理员权限。

## 构建

```powershell
just build        # dist/sphvideo.exe
just dist         # 二进制 + 中文安装包
```

或直接：

```powershell
go build -trimpath -tags mygo_noinspector -ldflags "-s -w -X github.com/egoist/mygo.production=1" -o dist/sphvideo.exe .
go tool mygo build -platform windows/amd64
```

## 许可、第三方与免责声明

- 本项目代码：MIT，见 [LICENSE](LICENSE)。
- 第三方许可全文：见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
  主要包含 MyGo（MIT）、Echo（MIT），以及参考实现
  [wx_channels_download](https://github.com/ltaoo/wx_channels_download)
  （MIT + Commons Clause）。
- 参考实现采用 MIT + Commons Clause，**禁止销售**；本项目同样免费、
  非商业，不得销售。
- 本项目与腾讯、微信、视频号及上述上游项目作者**无任何关联**，未获其
  授权、认可或背书。
- 仅用于个人学习、技术研究，以及备份自己有权访问的内容。不得用于侵犯
  著作权、隐私或商业秘密，不得传播、销售或用于商业用途。
- 下载内容的版权归原作者及权利人所有。请遵守微信/视频号服务条款和当地
  法律。
- 不收集、不上传微信账号、Cookie 或视频内容；不绕过付费、权限或其他
  访问控制。
- 软件按「现状」提供，不提供任何明示或默示担保。使用风险由使用者自行
  承担，作者不承担任何直接或间接责任。
