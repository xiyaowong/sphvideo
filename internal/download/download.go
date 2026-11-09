package download

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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

func Save(req Request, dir string, proxy proxyFunc, status func(string)) (string, error) {
	switch strings.TrimSpace(req.Kind) {
	case "", "video":
		return saveVideo(req, dir, proxy, status)
	case "mp3":
		return saveMP3(req, dir, proxy, status)
	case "cover":
		return saveCover(req, dir, proxy, status)
	case "images":
		return saveImages(req, dir, proxy, status)
	default:
		return "", fmt.Errorf("未知下载类型")
	}
}

func saveVideo(req Request, dir string, proxy proxyFunc, status func(string)) (string, error) {
	urls, key, name, err := prepareMedia(req, dir)
	if err != nil {
		return "", err
	}
	outPath := uniquePath(dir, name, ".mp4")

	statusf(status, "正在下载："+name)
	resp, err := getAny(urls, proxy)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(outPath)
		}
	}()
	if _, err := io.Copy(f, DecryptingReader(resp.Body, key)); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	statusf(status, "已保存："+outPath)
	return outPath, nil
}

func saveMP3(req Request, dir string, proxy proxyFunc, status func(string)) (string, error) {
	ffmpeg, err := ffmpegPath()
	if err != nil {
		return "", err
	}
	urls, key, name, err := prepareMedia(req, dir)
	if err != nil {
		return "", err
	}
	outPath := uniquePath(dir, name, ".mp3")

	statusf(status, "正在下载并转换 MP3："+name)
	resp, err := getAny(urls, proxy)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(outPath)
		}
	}()

	cmd := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-y",
		"-i", "pipe:0", "-vn", "-codec:a", "libmp3lame", "-b:a", "192k",
		"-f", "mp3", "pipe:1")
	cmd.Stdin = DecryptingReader(resp.Body, key)
	cmd.Stdout = f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return "", fmt.Errorf("MP3 转换失败: %v %s", err, msg)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	statusf(status, "已保存："+outPath)
	return outPath, nil
}

func saveCover(req Request, dir string, proxy proxyFunc, status func(string)) (string, error) {
	cover := strings.TrimSpace(req.Cover)
	if cover == "" {
		return "", fmt.Errorf("没有封面地址")
	}
	if err := validateURL(cover); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	name := safeFilename(req.Title)
	if name == "" {
		name = "视频号_" + time.Now().Format("20060102_150405")
	}
	outPath := uniquePath(dir, name+"_封面", ".jpg")

	statusf(status, "正在下载封面："+name)
	resp, err := get(cover, proxy)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(outPath)
		}
	}()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	statusf(status, "已保存："+outPath)
	return outPath, nil
}

func saveImages(req Request, dir string, proxy proxyFunc, status func(string)) (string, error) {
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
		return "", fmt.Errorf("没有可下载的图片")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	name := safeFilename(req.Title)
	if name == "" {
		name = "视频号_" + time.Now().Format("20060102_150405")
	}
	outPath := uniquePath(dir, name, ".zip")

	statusf(status, "正在下载图片："+name)
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
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
		w, err := zw.Create(fmt.Sprintf("%02d.jpg", i+1))
		if err != nil {
			return "", err
		}
		resp, err := get(raw, proxy)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(w, resp.Body)
		resp.Body.Close()
		if copyErr != nil {
			return "", fmt.Errorf("写入失败: %w", copyErr)
		}
	}
	if bgm != "" {
		w, err := zw.Create("bgm.mp3")
		if err != nil {
			return "", err
		}
		resp, err := get(bgm, proxy)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(w, resp.Body)
		resp.Body.Close()
		if copyErr != nil {
			return "", fmt.Errorf("写入失败: %w", copyErr)
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	statusf(status, "已保存："+outPath)
	return outPath, nil
}

func ffmpegPath() (string, error) {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	// GUI apps on macOS do not inherit the shell PATH.
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg", "/usr/bin/ffmpeg"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("MP3 需要 ffmpeg，请先安装")
}

func prepareMedia(req Request, dir string) (urls []string, key uint64, name string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, "", fmt.Errorf("创建目录失败: %w", err)
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
		return nil, fmt.Errorf("无效的媒体地址")
	}
	if !allowedMediaHost(u.Hostname()) {
		return nil, fmt.Errorf("不支持的地址")
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
		return fmt.Errorf("无效的媒体地址")
	}
	if !allowedMediaHost(u.Hostname()) {
		return fmt.Errorf("不支持的地址")
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
		return 0, fmt.Errorf("无效的解密 key")
	}
	return key, nil
}

func getAny(urls []string, proxy proxyFunc) (*http.Response, error) {
	var last error
	for _, raw := range urls {
		resp, err := get(raw, proxy)
		if err == nil {
			return resp, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("无效的媒体地址")
	}
	return nil, last
}

func get(raw string, proxy proxyFunc) (*http.Response, error) {
	client := &http.Client{
		Transport: &http.Transport{Proxy: proxy},
		Timeout:   0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !allowedMediaHost(req.URL.Hostname()) {
				return fmt.Errorf("不支持的地址")
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://channels.weixin.qq.com/")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func statusf(status func(string), msg string) {
	if status != nil {
		status(msg)
	}
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
