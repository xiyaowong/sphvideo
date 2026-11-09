//go:build linux

package proxy

import (
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func nssDB() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pki", "nssdb")
}

func ensureNSS() (string, error) {
	if _, err := exec.LookPath("certutil"); err != nil {
		return "", fmt.Errorf("需要 certutil（请安装 libnss3-tools）")
	}
	dir := nssDB()
	if _, err := os.Stat(filepath.Join(dir, "cert9.db")); err != nil {
		return "", fmt.Errorf("未找到 NSS 证书库；请先运行 Chromium/Electron 应用")
	}
	return dir, nil
}

func installCA(der []byte) error {
	dir, err := ensureNSS()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "sphvideo-ca-*.pem")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := exec.Command("certutil", "-d", "sql:"+dir, "-A", "-t", "C,,", "-n", caCommonName, "-i", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装证书失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeCA deletes every certificate this program may have installed, including
// leftovers from a crashed run.
func removeCA() error {
	if _, err := exec.LookPath("certutil"); err != nil {
		return nil
	}
	dir := nssDB()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	for i := 0; i < 10; i++ {
		out, err := exec.Command("certutil", "-d", "sql:"+dir, "-D", "-n", caCommonName).CombinedOutput()
		if err == nil {
			continue
		}
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "could not find") || strings.Contains(msg, "not found") || strings.Contains(msg, "no such") {
			return nil
		}
		return fmt.Errorf("删除证书失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
