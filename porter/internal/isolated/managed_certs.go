package isolated

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Copy only public certificates from known OS trust bundles, never host /etc
// or a TLS key directory. Model credentials and Agent files are not consulted.
func managedCertificates(w *Workspace) error {
	var source []byte
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"} {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return ErrWorkspace
		}
		st, err := f.Stat()
		var owner *syscall.Stat_t
		if err == nil {
			owner, _ = st.Sys().(*syscall.Stat_t)
		}
		if err != nil || owner == nil || owner.Uid != 0 || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 || st.Size() > 8<<20 {
			f.Close()
			return ErrWorkspace
		}
		source, err = io.ReadAll(io.LimitReader(f, 8<<20+1))
		f.Close()
		if err != nil || len(source) > 8<<20 {
			return ErrWorkspace
		}
		break
	}
	if len(source) == 0 {
		return ErrUnsupported
	}
	certificates, err := publicCertificateBundle(source)
	if err != nil {
		return err
	}
	dir := filepath.Join(w.dir, "etc", "ssl", "certs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrWorkspace
	}
	path := filepath.Join(dir, "ca-certificates.crt")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		old, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(old, certificates) {
			return ErrWorkspace
		}
		return nil
	}
	if err != nil {
		return ErrWorkspace
	}
	_, err = f.Write(certificates)
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil || closed != nil {
		return ErrWorkspace
	}
	return nil
}

func publicCertificateBundle(source []byte) ([]byte, error) {
	var certificates bytes.Buffer
	for len(source) > 0 {
		block, rest := pem.Decode(source)
		if block == nil {
			break
		}
		source = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, ErrWorkspace
		}
		if err := pem.Encode(&certificates, &pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes}); err != nil {
			return nil, ErrWorkspace
		}
	}
	if certificates.Len() == 0 {
		return nil, ErrWorkspace
	}
	return certificates.Bytes(), nil
}
