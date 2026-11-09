//go:build darwin

package proxy

import (
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func loginKeychain() string {
	home, _ := os.UserHomeDir()
	db := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	if _, err := os.Stat(db); err == nil {
		return db
	}
	legacy := filepath.Join(home, "Library", "Keychains", "login.keychain")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return db
}

func installCA(der []byte) error {
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
	out, err := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", loginKeychain(), name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装证书失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeCA deletes every certificate this program may have installed, including
// leftovers from a crashed run.
func removeCA() error {
	keychain := loginKeychain()
	for i := 0; i < 10; i++ {
		out, err := exec.Command("security", "delete-certificate", "-c", caCommonName, keychain).CombinedOutput()
		if err == nil {
			continue
		}
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "could not be found") || strings.Contains(msg, "not found") {
			return nil
		}
		return fmt.Errorf("删除证书失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
