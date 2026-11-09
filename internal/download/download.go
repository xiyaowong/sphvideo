package download

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Request struct {
	URL    string `json:"url"`
	Key    string `json:"key"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Spec   string `json:"spec"`
}

func Save(req Request, dir string, proxy func(*http.Request) (*url.URL, error), status func(string)) (string, error) {
	raw := strings.TrimSpace(req.URL)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("无效的视频地址")
	}
	if !allowedMediaHost(u.Hostname()) {
		return "", fmt.Errorf("不支持的地址")
	}
	if req.Spec != "" {
		sep := "&"
		if u.RawQuery == "" {
			sep = "?"
		}
		raw += sep + "X-snsvideoflag=" + url.QueryEscape(req.Spec)
	} else {
		// Original quality: keep only the two parameters the media host needs.
		q := u.Query()
		if q.Get("encfilekey") != "" && q.Get("token") != "" {
			u.RawQuery = url.Values{
				"encfilekey": {q.Get("encfilekey")},
				"token":      {q.Get("token")},
			}.Encode()
			raw = u.String()
		}
	}
	var key uint64
	if strings.TrimSpace(req.Key) != "" {
		key, err = strconv.ParseUint(strings.TrimSpace(req.Key), 10, 64)
		if err != nil {
			return "", fmt.Errorf("无效的解密 key")
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	name := safeFilename(req.Title)
	if name == "" {
		name = "视频号_" + time.Now().Format("20060102_150405")
	}
	outPath := uniquePath(dir, name, ".mp4")

	if status != nil {
		status("正在下载：" + name)
	}
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

	client := &http.Client{
		Transport: &http.Transport{Proxy: proxy},
		Timeout:   0,
	}
	httpReq, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	httpReq.Header.Set("Referer", "https://channels.weixin.qq.com/")
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := decryptFileInPlace(outPath, key); err != nil {
		return "", fmt.Errorf("解密失败: %w", err)
	}
	ok = true
	if status != nil {
		status("已保存：" + outPath)
	}
	return outPath, nil
}

func allowedMediaHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "qq.com" || strings.HasSuffix(host, ".qq.com")
}

func safeFilename(title string) string {
	title = strings.TrimSpace(title)
	var b strings.Builder
	count := 0
	for _, r := range title {
		if count >= 120 {
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
	name := strings.TrimSpace(b.String())
	return strings.Trim(name, ".")
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
