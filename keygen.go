package charm

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"golang.org/x/crypto/ssh"
)

var (
	// MissingSSHKeysErr indicates we're missing
	MissingSSHKeysErr = errors.New("missing one or more keys; did you forget to generate them?")
)

// FilesystemError is used to signal there was a problem at the
// filesystem-level. For example, when we're unable to create a directory to
// store new SSH keys in.
type FilesystemErr struct {
	error
}

// Error implements the error interface
func (e FilesystemErr) Error() string {
	return e.error.Error()
}

// SSHKeyPair holds a pair of SSH keys and associated methods
type SSHKeyPair struct {
	PrivateKeyPEM []byte
	PublicKey     []byte
	keyDir        string
	filename      string // private key filename; public key will have .pub appended
}

func (s SSHKeyPair) privateKeyPath() string {
	return filepath.Join(s.keyDir, s.filename)
}

func (s SSHKeyPair) publicKeyPath() string {
	return filepath.Join(s.keyDir, s.filename+".pub")
}

// NewSSHKeyPair generates an SSHKeyPair, which contains a pair of SSH keys.
func NewSSHKeyPair() (*SSHKeyPair, error) {
	s := &SSHKeyPair{}
	if err := s.GenerateRSAKeys(4096); err != nil {
		return nil, err
	}
	if err := s.WriteKeys(); err != nil {
		return nil, err
	}
	return s, nil
}

// newSSHKeyPairWithBitSize returns an SSH key pair with a given bit size. This
// is implemented for testing only. In production, use NewSSHKeyPair.
func newSSHKeyPairWithBitSize(bitSize int) (*SSHKeyPair, error) {
	s := &SSHKeyPair{}
	if err := s.GenerateRSAKeys(bitSize); err != nil {
		return nil, err
	}
	return s, nil
}

// GenerateEd25519Keys creates a pair of EdD25519 keys suitable for SSH auth
func (s *SSHKeyPair) GenerateEd25519Keys() error {
	// Generate keys
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	// Get ASN.1 DER format
	x509Encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}

	// Encode PEM
	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: x509Encoded,
	})

	// Prepare public key
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return err
	}

	s.PrivateKeyPEM = pemBlock
	s.PublicKey = ssh.MarshalAuthorizedKey(publicKey) // serialize for public key file on disk
	s.keyDir = "~/.ssh"
	s.filename = "id_ed25519"
	return nil
}

func (s *SSHKeyPair) GenerateRSAKeys(bitSize int) error {

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, bitSize)
	if err != nil {
		return err
	}

	// Validate private key
	err = privateKey.Validate()
	if err != nil {
		return err
	}

	// Get ASN.1 DER format
	x509Encoded := x509.MarshalPKCS1PrivateKey(privateKey)

	// Private key in PEM format
	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   x509Encoded,
	})

	// Generate public key
	publicRSAKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return err
	}

	s.PrivateKeyPEM = pemBlock
	s.PublicKey = ssh.MarshalAuthorizedKey(publicRSAKey)
	s.keyDir = "~/.ssh"
	s.filename = "id_rsa"
	return nil
}

// PrepFilesystem makes sure state of the filesystem is as it needs to be in
// order to write our keys to disk. It will create and/or set permissions on
// the SSH directory we're going to write our keys to (generally ~/.ssh) as
// well as make sure that no files exist at the location in which we're going
// to write out keys.
func (s *SSHKeyPair) PrepFilesystem() error {
	var err error

	s.keyDir, err = homedir.Expand(s.keyDir)
	if err != nil {
		return err
	}

	info, err := os.Stat(s.keyDir)
	if os.IsNotExist(err) {
		// Directory doesn't exist: create it
		return os.Mkdir(s.keyDir, 0700)
	}
	if err != nil {
		// There was another error statting the directory; something is awry
		return FilesystemErr{err}
	}
	if !info.IsDir() {
		// It exist but it's not a directory
		return FilesystemErr{fmt.Errorf("%s is not a directory", s.keyDir)}
	}
	if info.Mode().Perm() != 0700 {
		// Permissions are wrong: fix 'em
		if err := os.Chmod(s.keyDir, 0700); err != nil {
			return FilesystemErr{err}
		}
	}

	// Make sure the files we're going to write to don't already exist
	if fileExists(s.privateKeyPath()) {
		return FilesystemErr{fmt.Errorf("file %s already exists", s.privateKeyPath())}
	}
	if fileExists(s.publicKeyPath()) {
		return FilesystemErr{fmt.Errorf("file %s already exists", s.publicKeyPath())}
	}

	// The directory looks good as-is. This should be a rare case.
	return nil
}

// WriteKeys writes the SSH key pair to disk
func (s *SSHKeyPair) WriteKeys() error {
	if len(s.PrivateKeyPEM) == 0 || len(s.PublicKey) == 0 {
		return MissingSSHKeysErr
	}

	if err := s.PrepFilesystem(); err != nil {
		return err
	}

	// Write keys to disk
	if err := writeKeyToFile(s.PrivateKeyPEM, s.privateKeyPath()); err != nil {
		return err
	}
	if err := writeKeyToFile(s.PublicKey, s.publicKeyPath()); err != nil {
		return err
	}

	return nil
}

// writeKeyToFile write a key to a given path with appropriate permissions
func writeKeyToFile(keyBytes []byte, path string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ioutil.WriteFile(path, keyBytes, 0600)
	}
	return FilesystemErr{fmt.Errorf("file %s already exists", path)}
}
