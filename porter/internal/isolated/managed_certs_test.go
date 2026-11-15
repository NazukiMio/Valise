package isolated

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestManagedTrustBundleCopiesOnlyPublicCertificates(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Unix(0, 0), NotAfter: time.Unix(2000000000, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateMarker := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("fixture-private-marker")})
	result, err := publicCertificateBundle(append(append([]byte{}, privateMarker...), public...))
	if err != nil || !bytes.Equal(result, public) || bytes.Contains(result, []byte("PRIVATE KEY")) {
		t.Fatal("non-certificate bytes entered runtime trust material", err)
	}
	if _, err := publicCertificateBundle(privateMarker); err == nil {
		t.Fatal("non-certificate source accepted")
	}
}
