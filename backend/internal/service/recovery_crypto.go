package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"golang.org/x/crypto/scrypt"
	"io"
)

const recoveryMagic = "DTREC001"
const recoveryChunk = 1 << 20

func recoveryKey(pass string, salt []byte) (cipher.AEAD, error) {
	if len(pass) < 12 || len(pass) > 1024 {
		return nil, fmt.Errorf("recovery passphrase must contain 12–1024 bytes")
	}
	key, err := scrypt.Key([]byte(pass), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Independent authenticated frames bound memory usage; an authenticated empty
// final frame is mandatory, so truncation is never accepted as a complete backup.
func EncryptRecovery(out io.Writer, input io.Reader, pass string) error {
	header := make([]byte, len(recoveryMagic)+16+4)
	copy(header, recoveryMagic)
	if _, err := rand.Read(header[len(recoveryMagic):]); err != nil {
		return err
	}
	aead, err := recoveryKey(pass, header[8:24])
	if err != nil {
		return err
	}
	if _, err = out.Write(header); err != nil {
		return err
	}
	buffer := make([]byte, recoveryChunk)
	var index uint64
	for {
		n, readErr := io.ReadFull(input, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		nonce := make([]byte, 12)
		copy(nonce, header[24:])
		binary.BigEndian.PutUint64(nonce[4:], index)
		aad := append(append([]byte{}, header...), nonce[4:]...)
		encrypted := aead.Seal(nil, nonce, buffer[:n], aad)
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(encrypted)))
		if _, err = out.Write(size[:]); err != nil {
			return err
		}
		if _, err = out.Write(encrypted); err != nil {
			return err
		}
		index++
		if n == 0 {
			return nil
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			input = &emptyRecoveryReader{}
		}
	}
}

type emptyRecoveryReader struct{}

func (*emptyRecoveryReader) Read([]byte) (int, error) { return 0, io.EOF }
func DecryptRecovery(out io.Writer, input io.Reader, pass string, maximum int64) error {
	header := make([]byte, 28)
	if _, err := io.ReadFull(input, header); err != nil || string(header[:8]) != recoveryMagic {
		return fmt.Errorf("invalid encrypted recovery archive")
	}
	aead, err := recoveryKey(pass, header[8:24])
	if err != nil {
		return err
	}
	var index uint64
	var total int64
	for {
		var size [4]byte
		if _, err = io.ReadFull(input, size[:]); err != nil {
			return fmt.Errorf("encrypted archive is truncated")
		}
		n := binary.BigEndian.Uint32(size[:])
		if n < uint32(aead.Overhead()) || n > recoveryChunk+uint32(aead.Overhead()) {
			return fmt.Errorf("invalid encrypted frame")
		}
		frame := make([]byte, n)
		if _, err = io.ReadFull(input, frame); err != nil {
			return fmt.Errorf("encrypted archive is truncated")
		}
		nonce := make([]byte, 12)
		copy(nonce, header[24:])
		binary.BigEndian.PutUint64(nonce[4:], index)
		aad := append(append([]byte{}, header...), nonce[4:]...)
		plain, openErr := aead.Open(nil, nonce, frame, aad)
		if openErr != nil {
			return fmt.Errorf("wrong passphrase or damaged encrypted archive")
		}
		if len(plain) == 0 {
			var extra [1]byte
			if n, e := input.Read(extra[:]); n != 0 || e != io.EOF {
				return fmt.Errorf("unexpected data after encrypted archive")
			}
			return nil
		}
		total += int64(len(plain))
		if total > maximum {
			return fmt.Errorf("recovery archive exceeds configured size limit")
		}
		if _, err = out.Write(plain); err != nil {
			return err
		}
		index++
	}
}
