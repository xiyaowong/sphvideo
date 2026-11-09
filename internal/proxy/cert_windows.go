//go:build windows

package proxy

import (
	"crypto/x509"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	certStoreProvSystemW   = 10
	certSystemStoreUser    = 0x00010000
	certSystemStoreMachine = 0x00020000
	certStoreAddReplace    = 3
	certEncoding           = 0x00000001 | 0x00010000
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

func openRootStore(flags uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("Root")
	if err != nil {
		return 0, err
	}
	h, _, callErr := procCertOpenStore.Call(
		certStoreProvSystemW, 0, 0, uintptr(flags), uintptr(unsafe.Pointer(name)),
	)
	if h == 0 {
		return 0, fmt.Errorf("CertOpenStore: %v", callErr)
	}
	return windows.Handle(h), nil
}

// installCA writes to the machine's root store: that needs the elevated token
// and adds the certificate without the confirmation the user store shows.
func installCA(der []byte) error {
	store, err := openRootStore(certSystemStoreMachine)
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
// leftovers from a crashed run and from older versions that used the user store.
func removeCA() error {
	machine := removeFromStore(certSystemStoreMachine)
	user := removeFromStore(certSystemStoreUser)
	return errors.Join(machine, user)
}

func removeFromStore(flags uint32) error {
	store, err := openRootStore(flags)
	if err != nil {
		return err
	}
	defer procCertCloseStore.Call(uintptr(store), 0)

	var prev uintptr
	failures := 0
	for {
		ctx, _, _ := procCertEnumCertificates.Call(uintptr(store), prev)
		if ctx == 0 {
			return nil
		}
		c := certContextFromHandle(ctx)
		if c.certEncoded != nil && c.certEncodedLn > 0 {
			raw := unsafe.Slice(c.certEncoded, c.certEncodedLn)
			if cert, err := x509.ParseCertificate(raw); err == nil && cert.Subject.CommonName == caCommonName {
				ok, _, callErr := procCertDeleteCertificate.Call(ctx)
				if ok != 0 {
					prev = 0
					failures = 0
					continue
				}
				// The delete call frees the context even when it fails.
				failures++
				if failures > 5 {
					return fmt.Errorf("CertDeleteCertificateFromStore: %v", callErr)
				}
				prev = 0
				continue
			}
		}
		prev = ctx
	}
}
