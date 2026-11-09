//go:build windows

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const caCommonName = "SphVideo Temp CA"

const (
	certStoreProvSystemW = 10
	certSystemStoreUser  = 0x00010000
	certStoreAddReplace  = 3
	certEncoding         = 0x00000001 | 0x00010000
)

var (
	crypt32                       = windows.NewLazySystemDLL("crypt32.dll")
	procCertOpenStore             = crypt32.NewProc("CertOpenStore")
	procCertCloseStore            = crypt32.NewProc("CertCloseStore")
	procCertCreateCertificateCtx  = crypt32.NewProc("CertCreateCertificateContext")
	procCertFreeCertificateCtx    = crypt32.NewProc("CertFreeCertificateContext")
	procCertAddCertificateToStore = crypt32.NewProc("CertAddCertificateContextToStore")
	procCertDeleteCertificate     = crypt32.NewProc("CertDeleteCertificateFromStore")
	procCertEnumCertificates      = crypt32.NewProc("CertEnumCertificatesInStore")
)

type certContext struct {
	encodingType  uint32
	certEncoded   *byte
	certEncodedLn uint32
	certInfo      uintptr
	store         windows.Handle
}

//go:nocheckptr
func certContextFromHandle(p uintptr) *certContext {
	return (*certContext)(unsafe.Pointer(p))
}

func generateCA() (certPEM, keyPEM []byte, der []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: caCommonName, Organization: []string{"SphVideo"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	raw, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, raw, nil
}

func openRootStore() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("Root")
	if err != nil {
		return 0, err
	}
	h, _, callErr := procCertOpenStore.Call(
		certStoreProvSystemW, 0, 0, certSystemStoreUser, uintptr(unsafe.Pointer(name)),
	)
	if h == 0 {
		return 0, fmt.Errorf("CertOpenStore: %v", callErr)
	}
	return windows.Handle(h), nil
}

func installCA(der []byte) error {
	store, err := openRootStore()
	if err != nil {
		return err
	}
	defer procCertCloseStore.Call(uintptr(store), 0)

	ctx, _, callErr := procCertCreateCertificateCtx.Call(
		certEncoding, uintptr(unsafe.Pointer(&der[0])), uintptr(len(der)),
	)
	if ctx == 0 {
		return fmt.Errorf("CertCreateCertificateContext: %v", callErr)
	}
	defer procCertFreeCertificateCtx.Call(ctx)

	ok, _, callErr := procCertAddCertificateToStore.Call(uintptr(store), ctx, certStoreAddReplace, 0)
	if ok == 0 {
		return fmt.Errorf("CertAddCertificateContextToStore: %v", callErr)
	}
	return nil
}

// removeCA deletes every certificate this program may have installed, including
// leftovers from a crashed run.
func removeCA() error {
	store, err := openRootStore()
	if err != nil {
		return err
	}
	defer procCertCloseStore.Call(uintptr(store), 0)

	var prev uintptr
	for {
		ctx, _, _ := procCertEnumCertificates.Call(uintptr(store), prev)
		if ctx == 0 {
			return nil
		}
		c := certContextFromHandle(ctx)
		if c.certEncoded != nil && c.certEncodedLn > 0 {
			raw := unsafe.Slice(c.certEncoded, c.certEncodedLn)
			if cert, err := x509.ParseCertificate(raw); err == nil && cert.Subject.CommonName == caCommonName {
				procCertDeleteCertificate.Call(ctx)
				prev = 0
				continue
			}
		}
		prev = ctx
	}
}
