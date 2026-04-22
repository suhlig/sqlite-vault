package backup

import (
	"errors"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
)

// WithPassphrase configures symmetric encryption using the given passphrase.
// It fails if the passphrase is unset or empty.
func (s *Service) WithPassphrase(pw string) (*Service, error) {
	if pw == "" {
		return s, errors.New("refusing to proceed with an empty passphrase")
	}

	r, err := age.NewScryptRecipient(pw)

	if err != nil {
		return s, fmt.Errorf("creating scrypt recipient: %w", err)
	}

	recipients := []age.Recipient{r}

	s.encryptor = func(inPath, outPath string) error {
		return encryptFile(inPath, outPath, recipients)
	}

	return s, nil
}

func encryptFile(inPath, outPath string, recips []age.Recipient) error {
	in, err := os.Open(inPath)

	if err != nil {
		return fmt.Errorf("opening input file %q: %w", inPath, err)
	}

	defer func() {
		_ = in.Close()
	}()

	tmp := outPath + ".tmp"

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)

	if err != nil {
		return fmt.Errorf("creating temp output file %q: %w", tmp, err)
	}

	ew, err := age.Encrypt(out, recips...)

	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("initializing age writer: %w", err)
	}

	_, copyErr := io.Copy(ew, in)
	closeErr := ew.Close()
	outCloseErr := out.Close()

	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("copying plaintext into age writer: %w", copyErr)
	}

	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("closing age writer: %w", closeErr)
	}

	if outCloseErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("closing temp output file: %w", outCloseErr)
	}

	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming temp output file to %q: %w", outPath, err)
	}

	return nil
}
