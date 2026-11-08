package service

import (
	"bytes"
	"testing"
)

func TestRecoveryEncryptionAuthenticatesAllFrames(t *testing.T) {
	original := bytes.Repeat([]byte("sensitive-data"), 150000)
	var encrypted bytes.Buffer
	if err := EncryptRecovery(&encrypted, bytes.NewReader(original), "separate-recovery-key-123"); err != nil {
		t.Fatal(err)
	}
	var plain bytes.Buffer
	if err := DecryptRecovery(&plain, bytes.NewReader(encrypted.Bytes()), "separate-recovery-key-123", int64(len(original))); err != nil || !bytes.Equal(original, plain.Bytes()) {
		t.Fatal("round trip failed", err)
	}
	for _, bad := range [][]byte{encrypted.Bytes()[:encrypted.Len()-1], encrypted.Bytes()[:40]} {
		if DecryptRecovery(&bytes.Buffer{}, bytes.NewReader(bad), "separate-recovery-key-123", 1<<30) == nil {
			t.Fatal("accepted truncation")
		}
	}
	corrupt := append([]byte{}, encrypted.Bytes()...)
	corrupt[50] ^= 1
	if DecryptRecovery(&bytes.Buffer{}, bytes.NewReader(corrupt), "separate-recovery-key-123", 1<<30) == nil {
		t.Fatal("accepted tampering")
	}
	if DecryptRecovery(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()), "wrong-password-123", 1<<30) == nil {
		t.Fatal("accepted wrong key")
	}
	if DecryptRecovery(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()), "separate-recovery-key-123", 10) == nil {
		t.Fatal("ignored extraction bound")
	}
}
