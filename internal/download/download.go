package download

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Request struct {
	Kind   string   `json:"kind"`
	URL    string   `json:"url"`
	Key    string   `json:"key"`
	Title  string   `json:"title"`
	Author string   `json:"author"`
	Spec   string   `json:"spec"`
	Cover  string   `json:"cover"`
	Files  []string `json:"files"`
	BGM    string   `json:"bgm"`
}

type proxyFunc func(*http.Request) (*url.URL, error)

type progressFunc func(written, total int64, detail string)

func Save(ctx context.Context, req Request, dir string, proxy proxyFunc, report progressFunc) (string, error) {
	switch strings.TrimSpace(req.Kind) {
	case "", "video":
		return saveVideo(ctx, req, dir, proxy, report)
	case "cover":
		return saveCover(ctx, req, dir, proxy, report)
	case "images":
		return saveImages(ctx, req, dir, proxy, report)
	default:
		return "", userErr("不支持的下载类型", nil)
	}
}

func saveVideo(ctx context.Context, req Request, dir string, proxy proxyFunc, report progressFunc) (string, error) {
	urls, key, name, err := prepareMedia(req, dir)
	if err != nil {
		return "", err
	}
	outPath := uniquePath(dir, name, ".mp4")

	resp, err := getAny(ctx, urls, proxy)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fileErr(err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(outPath)
		}
	}()

	report(0, resp.ContentLength, "正在下载视频…")
	src := &progressReader{r: DecryptingReader(resp.Body, key), total: resp.ContentLength, report: report}
	if _, err := io.Copy(f, src); err != nil {
		return "", ioErr(err)
	}
	if err := f.Close(); err != nil {
		return "", fileErr(err)
	}
	if err := validateVideo(outPath); err != nil {
		return "", err
	}
	ok = true
	report(src.written, resp.ContentLength, "")
	return outPath, nil
}

func saveCover(ctx context.Context, req Request, dir string, proxy proxyFunc, report progressFunc) (string, error) {
	cover := strings.TrimSpace(req.Cover)
	if cover == "" {
		return "", userErr("没有可下载的封面", nil)
	}
	if err := validateURL(cover); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", dirErr(err)
	}
	name := safeFilename(req.Title)
	if name == "" {
		name = "视频号_" + time.Now().Format("20060102_150405")
	}

	resp, err := get(ctx, cover, proxy)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	head, _ := br.Peek(16)
	ext := imageExt(head, resp.Header.Get("Content-Type"), cover)
	if ext == "" {
		return "", userErr("封面地址已失效，请回到该内容重新点击下载", nil)
	}
	outPath := uniquePath(dir, name+"_封面", ext)

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fileErr(err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(outPath)
		}
	}()

	report(0, resp.ContentLength, "正在下载封面…")
	src := &progressReader{r: br, total: resp.ContentLength, report: report}
	if _, err := io.Copy(f, src); err != nil {
		return "", ioErr(err)
	}
	if err := f.Close(); err != nil {
		return "", fileErr(err)
	}
	if !validImageFile(outPath, ext) {
		return "", userErr("封面地址已失效，请回到该内容重新点击下载", nil)
	}
	ok = true
	report(src.written, resp.ContentLength, "")
	return outPath, nil
}

func saveImages(ctx context.Context, req Request, dir string, proxy proxyFunc, report progressFunc) (string, error) {
	files := make([]string, 0, len(req.Files))
	for _, raw := range req.Files {
		raw = strings.TrimSpace(raw)
		if raw == "" || validateURL(raw) != nil {
			continue
		}
		files = append(files, raw)
	}
	bgm := strings.TrimSpace(req.BGM)
	if validateURL(bgm) != nil {
		bgm = ""
	}
	if len(files) == 0 && bgm == "" {
		return "", userErr("没有可下载的图片", nil)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", dirErr(err)
	}
	name := safeFilename(req.Title)
	if name == "" {
		name = "视频号_" + time.Now().Format("20060102_150405")
	}
	outPath := uniquePath(dir, name, ".zip")
	total := int64(len(files))
	if bgm != "" {
		total++
	}

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fileErr(err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(outPath)
		}
	}()

	zw := zip.NewWriter(f)
	for i, raw := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		report(int64(i), total, fmt.Sprintf("正在下载第 %d/%d 张图片", i+1, len(files)))
		resp, err := get(ctx, raw, proxy)
		if err != nil {
			return "", err
		}
		br := bufio.NewReader(resp.Body)
		head, _ := br.Peek(16)
		ext := imageExt(head, resp.Header.Get("Content-Type"), raw)
		if ext == "" {
			resp.Body.Close()
			return "", userErr(fmt.Sprintf("第 %d 张图片下载失败，请重新点击下载", i+1), nil)
		}
		w, err := zw.Create(fmt.Sprintf("%02d%s", i+1, ext))
		if err != nil {
			resp.Body.Close()
			return "", fileErr(err)
		}
		_, copyErr := io.Copy(w, br)
		resp.Body.Close()
		if copyErr != nil {
			return "", ioErr(copyErr)
		}
		report(int64(i+1), total, "")
	}
	if bgm != "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		report(total-1, total, "正在下载背景音乐…")
		resp, err := get(ctx, bgm, proxy)
		if err != nil {
			return "", err
		}
		br := bufio.NewReader(resp.Body)
		head, _ := br.Peek(16)
		ext := audioExt(head, resp.Header.Get("Content-Type"), bgm)
		if ext == "" {
			resp.Body.Close()
			return "", userErr("背景音乐下载失败，请重新点击下载", nil)
		}
		w, err := zw.Create("bgm" + ext)
		if err != nil {
			resp.Body.Close()
			return "", fileErr(err)
		}
		_, copyErr := io.Copy(w, br)
		resp.Body.Close()
		if copyErr != nil {
			return "", ioErr(copyErr)
		}
		report(total, total, "")
	}
	if err := zw.Close(); err != nil {
		return "", fileErr(err)
	}
	if err := f.Close(); err != nil {
		return "", fileErr(err)
	}
	ok = true
	return outPath, nil
}

func prepareMedia(req Request, dir string) (urls []string, key uint64, name string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, "", dirErr(err)
	}
	if urls, err = mediaURLs(req.URL, req.Spec); err != nil {
		return nil, 0, "", err
	}
	if key, err = parseKey(req.Key); err != nil {
		return nil, 0, "", err
	}
	name = safeFilename(req.Title)
	if name == "" {
		name = "视频号_" + time.Now().Format("20060102_150405")
	}
	return urls, key, name, nil
}

func mediaURLs(raw, spec string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, userErr("视频地址无效，请回到该视频重新点击下载", nil)
	}
	if !allowedMediaHost(u.Hostname()) {
		return nil, userErr("不支持的视频地址", nil)
	}
	if spec != "" {
		sep := "&"
		if u.RawQuery == "" {
			sep = "?"
		}
		return []string{raw + sep + "X-snsvideoflag=" + url.QueryEscape(spec)}, nil
	}
	// Original quality: try the stripped URL the page uses, then the full URL
	// as a fallback for servers that require the extra signature parameters.
	q := u.Query()
	if q.Get("encfilekey") != "" && q.Get("token") != "" {
		stripped := *u
		stripped.RawQuery = url.Values{
			"encfilekey": {q.Get("encfilekey")},
			"token":      {q.Get("token")},
		}.Encode()
		if s := stripped.String(); s != raw {
			return []string{s, raw}, nil
		}
	}
	return []string{raw}, nil
}

func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return userErr("内容地址无效", nil)
	}
	if !allowedMediaHost(u.Hostname()) {
		return userErr("不支持的内容地址", nil)
	}
	return nil
}

func parseKey(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	key, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, userErr("视频地址已失效，请回到该视频重新点击下载", err)
	}
	return key, nil
}

func getAny(ctx context.Context, urls []string, proxy proxyFunc) (*http.Response, error) {
	var last error
	for _, raw := range urls {
		resp, err := get(ctx, raw, proxy)
		if err == nil {
			return resp, nil
		}
		last = err
	}
	if last == nil {
		last = userErr("视频地址无效，请回到该视频重新点击下载", nil)
	}
	return nil, last
}

func get(ctx context.Context, raw string, proxy proxyFunc) (*http.Response, error) {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: proxy,
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !allowedMediaHost(req.URL.Hostname()) {
				return userErr("不支持的跳转地址", nil)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, userErr("内容地址无效", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://channels.weixin.qq.com/")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, userErr("网络超时，请检查网络后重试", err)
		}
		return nil, userErr("网络中断，请检查网络后重试", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
			return nil, userErr("链接已过期，请回到原内容重新点击下载", nil)
		}
		if resp.StatusCode >= 500 {
			return nil, userErr("视频号服务器暂时不可用，请稍后重试", nil)
		}
		return nil, userErr(fmt.Sprintf("下载失败（服务器返回 %d），请稍后重试", resp.StatusCode), nil)
	}
	return resp, nil
}

type progressReader struct {
	r       io.Reader
	written int64
	total   int64
	report  progressFunc
	last    time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.written += int64(n)
		p.maybeReport()
	}
	return n, err
}

func (p *progressReader) maybeReport() {
	if p.report == nil {
		return
	}
	if p.total > 0 && p.written >= p.total {
		p.report(p.written, p.total, "")
		return
	}
	now := time.Now()
	if now.Sub(p.last) < 250*time.Millisecond {
		return
	}
	p.last = now
	p.report(p.written, p.total, "")
}

func validateVideo(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fileErr(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fileErr(err)
	}
	if st.Size() < 1024 {
		return userErr("下载到的文件无效，链接可能已过期。请回到该视频重新点击下载。", nil)
	}
	var head [12]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return userErr("下载到的文件无效，链接可能已过期。请回到该视频重新点击下载。", err)
	}
	if head[0] == '<' || head[0] == '{' {
		return userErr("下载到的文件无效，链接可能已过期。请回到该视频重新点击下载。", nil)
	}
	switch string(head[4:8]) {
	case "ftyp", "styp", "moov", "mdat", "free", "skip", "wide", "uuid":
		return nil
	}
	return userErr("下载到的文件无效，链接可能已过期。请回到该视频重新点击下载。", nil)
}

func validImageFile(path, ext string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [16]byte
	n, _ := io.ReadFull(f, head[:])
	if ext == ".svg" {
		trimmed := bytes.TrimSpace(head[:n])
		return len(trimmed) > 0 && trimmed[0] == '<'
	}
	return imageExtFromHead(head[:n]) != ""
}

func imageExt(head []byte, contentType, raw string) string {
	trimmed := bytes.TrimSpace(head)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		if extFromContentType(contentType) == ".svg" {
			return ".svg"
		}
		return ""
	}
	if ext := imageExtFromHead(head); ext != "" {
		return ext
	}
	// An MP4 or another ISO media file is not an image, however its URL ends.
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		return ""
	}
	if ext := extFromContentType(contentType); ext != "" {
		return ext
	}
	return extFromURL(raw, ".jpg", ".png", ".webp", ".gif", ".avif", ".heic", ".bmp", ".svg")
}

func imageExtFromHead(head []byte) string {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return ".jpg"
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return ".png"
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return ".gif"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return ".webp"
	case len(head) >= 2 && string(head[:2]) == "BM":
		return ".bmp"
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		switch string(head[8:12]) {
		case "avif", "avis":
			return ".avif"
		case "heic", "heix", "mif1", "msf1":
			return ".heic"
		}
	}
	return ""
}

func extFromContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "image/heic", "image/heif":
		return ".heic"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	}
	return ""
}

func audioExt(head []byte, contentType, raw string) string {
	trimmed := bytes.TrimSpace(head)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return ""
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch ct {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "audio/aac":
		return ".aac"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	}
	if len(head) >= 4 && string(head[:4]) == "fLaC" {
		return ".flac"
	}
	if len(head) >= 3 && string(head[:3]) == "ID3" {
		return ".mp3"
	}
	return extFromURL(raw, ".mp3", ".m4a", ".aac", ".ogg", ".wav", ".flac")
}

func extFromURL(raw string, allowed ...string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return allowed[0]
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	for _, want := range allowed {
		if ext == want {
			return ext
		}
	}
	return allowed[0]
}

func dirErr(err error) error {
	return userErr("无法创建保存目录，请更换保存目录", err)
}

func fileErr(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return userErr("无法写入保存目录，请更换保存目录", err)
	}
	if isNoSpace(err) {
		return userErr("磁盘空间不足，请清理后重试", err)
	}
	return userErr("无法保存文件，请更换保存目录", err)
}

func ioErr(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if isNoSpace(err) {
		return userErr("磁盘空间不足，请清理后重试", err)
	}
	return userErr("下载中断，请检查网络后重试", err)
}

func isNoSpace(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no space") ||
		strings.Contains(msg, "not enough space") ||
		strings.Contains(msg, "disk full")
}

func allowedMediaHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "qq.com" || strings.HasSuffix(host, ".qq.com") ||
		host == "qpic.cn" || strings.HasSuffix(host, ".qpic.cn")
}

func safeFilename(title string) string {
	title = strings.TrimSpace(title)
	var b strings.Builder
	count := 0
	for _, r := range title {
		if count >= 400 {
			break
		}
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			continue
		}
		b.WriteRune(r)
		count++
	}
	name := strings.Trim(strings.TrimSpace(b.String()), ".")
	name = strings.TrimRight(fitUTF8(name, 120), ". ")
	if name == "" {
		return ""
	}
	switch strings.ToUpper(strings.SplitN(name, ".", 2)[0]) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name = "_" + name
	}
	return name
}

func fitUTF8(s string, max int) string {
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		if size <= 0 {
			return s[:max]
		}
		s = s[:len(s)-size]
	}
	return s
}

func uniquePath(dir, name, ext string) string {
	p := filepath.Join(dir, name+ext)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	for i := 1; i < 10000; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", name, i, ext))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, time.Now().UnixNano(), ext))
}
